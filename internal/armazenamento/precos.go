package armazenamento

import (
	"fmt"
	"time"

	"carteira/internal/fontes/b3"
)

// Preços diários do COTAHIST e eventos (proventos, desdobramentos) da B3.
// WITHOUT ROWID guarda a tabela ordenada pela chave (ativo, data), que é
// como ela é lida, e ocupa menos espaço.
const esquemaPrecos = `
CREATE TABLE IF NOT EXISTS precos (
	ticker     TEXT NOT NULL,
	data       TEXT NOT NULL,
	isin       TEXT NOT NULL,
	fechamento REAL NOT NULL,
	PRIMARY KEY (ticker, data)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS b3_eventos (
	empresa  TEXT NOT NULL,
	isin     TEXT NOT NULL,
	especie  TEXT NOT NULL DEFAULT '',
	tipo     TEXT NOT NULL,
	data_com TEXT NOT NULL,
	valor    REAL NOT NULL,
	dinheiro INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS b3_eventos_empresa ON b3_eventos (empresa);
`

// SubstituirPrecos troca, numa transação, todos os preços de um ano.
func (b *Banco) SubstituirPrecos(ano int, precos []b3.Preco) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	de, ate := fmt.Sprintf("%d-01-01", ano), fmt.Sprintf("%d-12-31", ano)
	if _, err := tx.Exec(`DELETE FROM precos WHERE data BETWEEN ? AND ?`, de, ate); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT OR REPLACE INTO precos (ticker, data, isin, fechamento) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range precos {
		if p.Data.Year() != ano {
			return fmt.Errorf("preço de %s fora do ano %d", p.Data.Format(formatoData), ano)
		}
		if _, err := st.Exec(p.Ticker, p.Data.Format(formatoData), p.ISIN, p.Fechamento); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Precos devolve o histórico de um ativo em ordem de data.
func (b *Banco) Precos(ticker string) ([]b3.Preco, error) {
	rows, err := b.db.Query(`SELECT data, isin, fechamento FROM precos WHERE ticker = ? ORDER BY data`, ticker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []b3.Preco
	for rows.Next() {
		p := b3.Preco{Ticker: ticker}
		var data string
		if err := rows.Scan(&data, &p.ISIN, &p.Fechamento); err != nil {
			return nil, err
		}
		if p.Data, err = time.Parse(formatoData, data); err != nil {
			return nil, err
		}
		l = append(l, p)
	}
	return l, rows.Err()
}

// SubstituirEventos troca os eventos guardados de uma empresa.
func (b *Banco) SubstituirEventos(empresa string, evs []b3.Evento) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM b3_eventos WHERE empresa = ?`, empresa); err != nil {
		return err
	}
	for _, e := range evs {
		if _, err := tx.Exec(`INSERT INTO b3_eventos (empresa, isin, especie, tipo, data_com, valor, dinheiro) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			empresa, e.ISIN, e.Especie, e.Tipo, e.DataCom.Format(formatoData), e.Valor, e.Dinheiro); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Eventos devolve os eventos guardados de uma empresa, em ordem de data.
func (b *Banco) Eventos(empresa string) ([]b3.Evento, error) {
	rows, err := b.db.Query(`SELECT isin, especie, tipo, data_com, valor, dinheiro FROM b3_eventos WHERE empresa = ? ORDER BY data_com`, empresa)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []b3.Evento
	for rows.Next() {
		var e b3.Evento
		var data string
		if err := rows.Scan(&e.ISIN, &e.Especie, &e.Tipo, &data, &e.Valor, &e.Dinheiro); err != nil {
			return nil, err
		}
		if e.DataCom, err = time.Parse(formatoData, data); err != nil {
			return nil, err
		}
		l = append(l, e)
	}
	return l, rows.Err()
}
