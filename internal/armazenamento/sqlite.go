// Package armazenamento guarda operações, proventos, cotações e configurações num arquivo SQLite.
package armazenamento

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // driver SQLite em Go puro (não precisa de compilador C no Windows)

	"carteira/internal/carteira"
)

const formatoData = "2006-01-02"

// A coluna chave é UNIQUE: reimportar o mesmo extrato não duplica nada.
// Lançamentos manuais têm chave NULL, e o SQLite aceita vários NULL numa coluna UNIQUE.
const esquema = `
CREATE TABLE IF NOT EXISTS operacoes (
	id         INTEGER PRIMARY KEY,
	data       TEXT NOT NULL,
	ticker     TEXT NOT NULL,
	tipo       TEXT NOT NULL,
	quantidade REAL NOT NULL,
	preco      REAL NOT NULL,
	descricao  TEXT NOT NULL DEFAULT '',
	origem     TEXT NOT NULL,
	chave      TEXT UNIQUE
);
CREATE TABLE IF NOT EXISTS proventos (
	id     INTEGER PRIMARY KEY,
	data   TEXT NOT NULL,
	ticker TEXT NOT NULL,
	tipo   TEXT NOT NULL,
	valor  REAL NOT NULL,
	origem TEXT NOT NULL,
	chave  TEXT UNIQUE
);
CREATE TABLE IF NOT EXISTS observacao (
	ticker TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS ocultos (
	ticker TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS cotacoes (
	ticker     TEXT PRIMARY KEY,
	preco      REAL NOT NULL,
	horario    TEXT NOT NULL,
	buscado_em TEXT NOT NULL,
	fonte      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS config (
	chave TEXT PRIMARY KEY,
	valor TEXT NOT NULL
);
`

// Banco é a conexão com o arquivo SQLite.
type Banco struct{ db *sql.DB }

// Abrir abre (ou cria) o banco no caminho dado e garante que as tabelas existem.
func Abrir(caminho string) (*Banco, error) {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", caminho)
	if err != nil {
		return nil, err
	}
	// Uma conexão só: o SQLite trava o arquivo para escrita, e o programa é de um usuário.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(esquema + esquemaCVM + esquemaPrecos); err != nil {
		db.Close()
		return nil, fmt.Errorf("criar tabelas: %w", err)
	}
	if err := adicionarColuna(db, "operacoes", "ticker_anterior", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, err
	}
	// Tabela criada antes de a B3 trazer os proventos por espécie.
	if err := adicionarColuna(db, "b3_eventos", "especie", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, err
	}
	return &Banco{db: db}, nil
}

// adicionarColuna cria a coluna em bancos antigos que ainda não a têm.
func adicionarColuna(db *sql.DB, tabela, coluna, tipo string) error {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, tabela, coluna).Scan(&n)
	if err != nil || n > 0 {
		return err
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, tabela, coluna, tipo))
	return err
}

// Fechar fecha o arquivo.
func (b *Banco) Fechar() error { return b.db.Close() }

// executor é o que *sql.DB e *sql.Tx têm em comum para gravar.
type executor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func chaveNula(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// inserirOperacao devolve inserida=false quando a chave já existia.
func inserirOperacao(ex executor, op carteira.Operacao) (id int64, inserida bool, err error) {
	r, err := ex.Exec(`INSERT OR IGNORE INTO operacoes (data, ticker, tipo, quantidade, preco, descricao, origem, chave, ticker_anterior)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Data.Format(formatoData), op.Ticker, string(op.Tipo), op.Quantidade, op.Preco, op.Descricao, op.Origem, chaveNula(op.Chave), op.TickerAnterior)
	if err != nil {
		return 0, false, err
	}
	if n, err := r.RowsAffected(); err != nil || n == 0 {
		return 0, false, err
	}
	id, err = r.LastInsertId()
	return id, true, err
}

// InserirOperacao grava um lançamento manual e devolve o ID.
func (b *Banco) InserirOperacao(op carteira.Operacao) (int64, error) {
	id, _, err := inserirOperacao(b.db, op)
	return id, err
}

// Importar grava as operações e proventos de um extrato numa transação só:
// ou entra tudo, ou nada. Linhas já importadas antes são puladas.
func (b *Banco) Importar(ops []carteira.Operacao, provs []carteira.Provento) (novasOps, novosProvs int, err error) {
	tx, err := b.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback() // não faz nada depois do Commit

	for _, op := range ops {
		_, nova, err := inserirOperacao(tx, op)
		if err != nil {
			return 0, 0, fmt.Errorf("gravar operação %s de %s: %w", op.Ticker, op.Data.Format(formatoData), err)
		}
		if nova {
			novasOps++
		}
	}
	for _, p := range provs {
		r, err := tx.Exec(`INSERT OR IGNORE INTO proventos (data, ticker, tipo, valor, origem, chave) VALUES (?, ?, ?, ?, ?, ?)`,
			p.Data.Format(formatoData), p.Ticker, p.Tipo, p.Valor, p.Origem, chaveNula(p.Chave))
		if err != nil {
			return 0, 0, fmt.Errorf("gravar provento %s de %s: %w", p.Ticker, p.Data.Format(formatoData), err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			novosProvs++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return novasOps, novosProvs, nil
}

// Operacoes lista as operações em ordem de data. Com ticker vazio, lista todas.
func (b *Banco) Operacoes(ticker string) ([]carteira.Operacao, error) {
	q := `SELECT id, data, ticker, tipo, quantidade, preco, descricao, origem, COALESCE(chave, ''), ticker_anterior FROM operacoes`
	var args []any
	if ticker != "" {
		q += ` WHERE ticker = ? OR ticker_anterior = ?`
		args = append(args, ticker, ticker)
	}
	q += ` ORDER BY data, id`

	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ops []carteira.Operacao
	for rows.Next() {
		var op carteira.Operacao
		var data, tipo string
		if err := rows.Scan(&op.ID, &data, &op.Ticker, &tipo, &op.Quantidade, &op.Preco, &op.Descricao, &op.Origem, &op.Chave, &op.TickerAnterior); err != nil {
			return nil, err
		}
		if op.Data, err = time.Parse(formatoData, data); err != nil {
			return nil, fmt.Errorf("operação #%d com data inválida %q", op.ID, data)
		}
		op.Tipo = carteira.Tipo(tipo)
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// RemoverOperacao apaga uma operação. Devolve false se o ID não existe.
func (b *Banco) RemoverOperacao(id int64) (bool, error) {
	r, err := b.db.Exec(`DELETE FROM operacoes WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// TotalProventos soma todos os proventos gravados.
func (b *Banco) TotalProventos() (float64, error) {
	var total float64
	err := b.db.QueryRow(`SELECT COALESCE(SUM(valor), 0) FROM proventos`).Scan(&total)
	return total, err
}

// Proventos devolve os proventos recebidos, em ordem de data.
func (b *Banco) Proventos() ([]carteira.Provento, error) {
	rows, err := b.db.Query(`SELECT id, data, ticker, tipo, valor, origem FROM proventos ORDER BY data, ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []carteira.Provento
	for rows.Next() {
		var p carteira.Provento
		var data string
		if err := rows.Scan(&p.ID, &data, &p.Ticker, &p.Tipo, &p.Valor, &p.Origem); err != nil {
			return nil, err
		}
		if p.Data, err = time.Parse(formatoData, data); err != nil {
			return nil, err
		}
		l = append(l, p)
	}
	return l, rows.Err()
}

// Observar põe um ativo na lista de observação (repetir não dá erro).
func (b *Banco) Observar(ticker string) error {
	_, err := b.db.Exec(`INSERT OR IGNORE INTO observacao (ticker) VALUES (?)`, ticker)
	return err
}

// Desobservar tira um ativo da lista. Devolve false se ele não estava lá.
func (b *Banco) Desobservar(ticker string) (bool, error) {
	r, err := b.db.Exec(`DELETE FROM observacao WHERE ticker = ?`, ticker)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// Ocultar marca um ativo como de terceiros: fica fora do total e dos pesos.
func (b *Banco) Ocultar(ticker string) error {
	_, err := b.db.Exec(`INSERT OR IGNORE INTO ocultos (ticker) VALUES (?)`, ticker)
	return err
}

// Mostrar desfaz Ocultar. Devolve false se o ativo não estava oculto.
func (b *Banco) Mostrar(ticker string) (bool, error) {
	r, err := b.db.Exec(`DELETE FROM ocultos WHERE ticker = ?`, ticker)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// Ocultos devolve os ativos ocultos.
func (b *Banco) Ocultos() (map[string]bool, error) {
	rows, err := b.db.Query(`SELECT ticker FROM ocultos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		m[t] = true
	}
	return m, rows.Err()
}

// Observacao lista os ativos observados em ordem alfabética.
func (b *Banco) Observacao() ([]string, error) {
	rows, err := b.db.Query(`SELECT ticker FROM observacao ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tickers = append(tickers, t)
	}
	return tickers, rows.Err()
}

// Cotacao é a última cotação salva de um ativo.
type Cotacao struct {
	Ticker    string
	Preco     float64
	Horario   time.Time // horário do preço no mercado
	BuscadoEm time.Time // quando o programa buscou na fonte
	Fonte     string
}

// Cotacao devolve a cotação salva; ok=false se o ativo nunca foi consultado.
func (b *Banco) Cotacao(ticker string) (c Cotacao, ok bool, err error) {
	var horario, buscado string
	err = b.db.QueryRow(`SELECT ticker, preco, horario, buscado_em, fonte FROM cotacoes WHERE ticker = ?`, ticker).
		Scan(&c.Ticker, &c.Preco, &horario, &buscado, &c.Fonte)
	if errors.Is(err, sql.ErrNoRows) {
		return Cotacao{}, false, nil
	}
	if err != nil {
		return Cotacao{}, false, err
	}
	if c.Horario, err = time.Parse(time.RFC3339Nano, horario); err != nil {
		return Cotacao{}, false, err
	}
	if c.BuscadoEm, err = time.Parse(time.RFC3339Nano, buscado); err != nil {
		return Cotacao{}, false, err
	}
	return c, true, nil
}

// SalvarCotacao grava ou substitui a cotação de um ativo.
func (b *Banco) SalvarCotacao(c Cotacao) error {
	_, err := b.db.Exec(`INSERT INTO cotacoes (ticker, preco, horario, buscado_em, fonte) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ticker) DO UPDATE SET preco = excluded.preco, horario = excluded.horario,
			buscado_em = excluded.buscado_em, fonte = excluded.fonte`,
		c.Ticker, c.Preco, c.Horario.UTC().Format(time.RFC3339Nano), c.BuscadoEm.UTC().Format(time.RFC3339Nano), c.Fonte)
	return err
}

// ConfigNumero lê uma configuração numérica, ou devolve o padrão se ela não foi definida.
func (b *Banco) ConfigNumero(chave string, padrao float64) (float64, error) {
	var s string
	err := b.db.QueryRow(`SELECT valor FROM config WHERE chave = ?`, chave).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return padrao, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

// SalvarConfigNumero grava uma configuração numérica.
func (b *Banco) SalvarConfigNumero(chave string, v float64) error {
	_, err := b.db.Exec(`INSERT INTO config (chave, valor) VALUES (?, ?)
		ON CONFLICT(chave) DO UPDATE SET valor = excluded.valor`,
		chave, strconv.FormatFloat(v, 'g', -1, 64))
	return err
}
