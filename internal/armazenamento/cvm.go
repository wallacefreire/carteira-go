package armazenamento

import (
	"database/sql"
	"fmt"
	"time"

	"carteira/internal/fontes/cvm"
)

// Tabelas com o que foi lido da CVM. Cada linha guarda o arquivo de origem,
// para que reimportar um .zip troque só os dados dele.
const esquemaCVM = `
CREATE TABLE IF NOT EXISTS cvm_contas (
	arquivo      TEXT NOT NULL,
	cnpj         TEXT NOT NULL,
	consolidado  INTEGER NOT NULL,
	demonstracao TEXT NOT NULL,
	dt_refer     TEXT NOT NULL,
	versao       INTEGER NOT NULL,
	dt_ini       TEXT NOT NULL,
	dt_fim       TEXT NOT NULL,
	chave        TEXT NOT NULL,
	codigo       TEXT NOT NULL,
	descricao    TEXT NOT NULL,
	valor        REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS cvm_contas_cnpj ON cvm_contas (cnpj);
CREATE INDEX IF NOT EXISTS cvm_contas_arquivo ON cvm_contas (arquivo);
CREATE TABLE IF NOT EXISTS cvm_acoes (
	arquivo        TEXT NOT NULL,
	cnpj           TEXT NOT NULL,
	dt_refer       TEXT NOT NULL,
	versao         INTEGER NOT NULL,
	ordinarias     REAL NOT NULL,
	preferenciais  REAL NOT NULL,
	tesouraria_on  REAL NOT NULL,
	tesouraria_pn  REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS cvm_acoes_cnpj ON cvm_acoes (cnpj);
CREATE TABLE IF NOT EXISTS cvm_tickers (
	ticker             TEXT PRIMARY KEY,
	cnpj               TEXT NOT NULL,
	nome               TEXT NOT NULL,
	tipo               TEXT NOT NULL,
	acoes_por_unidade  REAL NOT NULL
);
`

func dataOuVazio(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(formatoData)
}

// SubstituirBalancos troca, numa transação, tudo o que veio de um arquivo da CVM.
func (b *Banco) SubstituirBalancos(arquivo string, contas []cvm.Conta, comps []cvm.Composicao) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, t := range []string{"cvm_contas", "cvm_acoes"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE arquivo = ?`, arquivo); err != nil {
			return err
		}
	}
	st, err := tx.Prepare(`INSERT INTO cvm_contas (arquivo, cnpj, consolidado, demonstracao, dt_refer, versao, dt_ini, dt_fim, chave, codigo, descricao, valor)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, c := range contas {
		if _, err := st.Exec(arquivo, c.CNPJ, c.Consolidado, c.Demonstracao, dataOuVazio(c.DtRefer), c.Versao,
			dataOuVazio(c.DtIni), dataOuVazio(c.DtFim), c.Chave, c.Codigo, c.Descricao, c.Valor); err != nil {
			return err
		}
	}
	for _, c := range comps {
		if _, err := tx.Exec(`INSERT INTO cvm_acoes (arquivo, cnpj, dt_refer, versao, ordinarias, preferenciais, tesouraria_on, tesouraria_pn)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, arquivo, c.CNPJ, dataOuVazio(c.DtRefer), c.Versao,
			c.Ordinarias, c.Preferenciais, c.TesourariaOrdinaria, c.TesourariaPref); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SalvarTickers grava os códigos de negociação (substitui os que já existiam).
func (b *Banco) SalvarTickers(tickers []cvm.Ticker) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tickers {
		if _, err := tx.Exec(`INSERT INTO cvm_tickers (ticker, cnpj, nome, tipo, acoes_por_unidade) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(ticker) DO UPDATE SET cnpj = excluded.cnpj, nome = excluded.nome, tipo = excluded.tipo,
				acoes_por_unidade = excluded.acoes_por_unidade`,
			t.Codigo, t.CNPJ, t.Nome, t.Tipo, t.AcoesPorUnidade); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TickerCVM devolve a empresa de um código de negociação; ok=false se não existe.
func (b *Banco) TickerCVM(codigo string) (t cvm.Ticker, ok bool, err error) {
	err = b.db.QueryRow(`SELECT ticker, cnpj, nome, tipo, acoes_por_unidade FROM cvm_tickers WHERE ticker = ?`, codigo).
		Scan(&t.Codigo, &t.CNPJ, &t.Nome, &t.Tipo, &t.AcoesPorUnidade)
	if err == sql.ErrNoRows {
		return cvm.Ticker{}, false, nil
	}
	return t, err == nil, err
}

// TickersCVM devolve todos os códigos de bolsa do cadastro da CVM (ações e units).
func (b *Banco) TickersCVM() ([]cvm.Ticker, error) {
	rows, err := b.db.Query(`SELECT ticker, cnpj, nome, tipo, acoes_por_unidade FROM cvm_tickers ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []cvm.Ticker
	for rows.Next() {
		var t cvm.Ticker
		if err := rows.Scan(&t.Codigo, &t.CNPJ, &t.Nome, &t.Tipo, &t.AcoesPorUnidade); err != nil {
			return nil, err
		}
		l = append(l, t)
	}
	return l, rows.Err()
}

// Balancos devolve as contas e a composição do capital de uma empresa.
func (b *Banco) Balancos(cnpj string) ([]cvm.Conta, []cvm.Composicao, error) {
	rows, err := b.db.Query(`SELECT cnpj, consolidado, demonstracao, dt_refer, versao, dt_ini, dt_fim, chave, codigo, descricao, valor
		FROM cvm_contas WHERE cnpj = ?`, cnpj)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var contas []cvm.Conta
	for rows.Next() {
		var c cvm.Conta
		var refer, ini, fim string
		if err := rows.Scan(&c.CNPJ, &c.Consolidado, &c.Demonstracao, &refer, &c.Versao, &ini, &fim, &c.Chave, &c.Codigo, &c.Descricao, &c.Valor); err != nil {
			return nil, nil, err
		}
		c.DtRefer, _ = time.Parse(formatoData, refer)
		c.DtIni, _ = time.Parse(formatoData, ini) // vazio no balanço patrimonial → data zero
		c.DtFim, _ = time.Parse(formatoData, fim)
		contas = append(contas, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows2, err := b.db.Query(`SELECT cnpj, dt_refer, versao, ordinarias, preferenciais, tesouraria_on, tesouraria_pn FROM cvm_acoes WHERE cnpj = ?`, cnpj)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()
	var comps []cvm.Composicao
	for rows2.Next() {
		var c cvm.Composicao
		var refer string
		if err := rows2.Scan(&c.CNPJ, &refer, &c.Versao, &c.Ordinarias, &c.Preferenciais, &c.TesourariaOrdinaria, &c.TesourariaPref); err != nil {
			return nil, nil, err
		}
		c.DtRefer, _ = time.Parse(formatoData, refer)
		comps = append(comps, c)
	}
	return contas, comps, rows2.Err()
}

// ConfigTexto lê uma configuração de texto; ok=false se não foi definida.
func (b *Banco) ConfigTexto(chave string) (string, bool, error) {
	var s string
	err := b.db.QueryRow(`SELECT valor FROM config WHERE chave = ?`, chave).Scan(&s)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return s, err == nil, err
}

// SalvarConfigTexto grava uma configuração de texto.
func (b *Banco) SalvarConfigTexto(chave, valor string) error {
	_, err := b.db.Exec(`INSERT INTO config (chave, valor) VALUES (?, ?) ON CONFLICT(chave) DO UPDATE SET valor = excluded.valor`, chave, valor)
	if err != nil {
		return fmt.Errorf("gravar config %s: %w", chave, err)
	}
	return nil
}
