// Package radar aplica o filtro do Wallace aos indicadores de uma ação.
// Nenhum critério elimina a ação sozinho: cada um vira ✓ ou ✗, e o radar
// ordena pela quantidade de critérios cumpridos.
package radar

import (
	"fmt"
	"strings"

	"carteira/internal/formato"
	"carteira/internal/indicadores"
)

// Limites do filtro (configuráveis com "carteira filtro").
type Limites struct {
	PLMax     float64 // P/L dos últimos 12 meses, entre 0 e PLMax
	ROEMin    float64 // fração, média de 5 anos
	ROICMin   float64 // fração, média de 5 anos (não vale para bancos)
	BetaMax   float64 // beta semanal de 2 anos
	CorrelMax float64 // correlação semanal de 2 anos com a carteira
}

// Padrao são os limites escolhidos pelo Wallace em 28/10/2026.
var Padrao = Limites{PLMax: 15, ROEMin: 0.12, ROICMin: 0.10, BetaMax: 1.2, CorrelMax: 0.6}

// Criterio é um item do filtro já avaliado.
type Criterio struct {
	Nome      string
	Valor     string // valor formatado ("7,9", "18,2%"), ou "-" sem dado
	OK        bool
	NaoAplica bool // ex.: ROIC de banco; não conta no total
}

// Avaliacao é o resultado do filtro para uma ação.
type Avaliacao struct {
	Ticker, Nome   string
	Criterios      []Criterio
	Cumpridos      int
	Total          int      // critérios que se aplicam
	HistoricoCurto bool     // menos de 5 anos de balanço
	Trimestres     []string // trimestres com prejuízo (alerta, não reprova)
	PL             float64  // para desempatar
}

// Nota é a fração de critérios cumpridos.
func (a Avaliacao) Nota() float64 {
	if a.Total == 0 {
		return 0
	}
	return float64(a.Cumpridos) / float64(a.Total)
}

// Avaliar aplica os limites aos indicadores calculados.
func Avaliar(r indicadores.Resultado, lim Limites) Avaliacao {
	av := Avaliacao{Ticker: r.Ticker, Nome: r.Nome, Trimestres: r.PrejuizoTrimestral}
	var anuais []indicadores.Coluna
	var m12, media indicadores.Coluna
	for _, c := range r.Colunas {
		switch c.Rotulo {
		case "12m":
			m12 = c
		case "Média 5a":
			media = c
		default:
			anuais = append(anuais, c)
		}
	}
	av.HistoricoCurto = len(anuais) < 5

	// 1. Lucro em todos os anos.
	lucro := Criterio{Nome: "Lucro 5a"}
	prejuizos := 0
	semDado := len(anuais) == 0
	for _, c := range anuais {
		v := c.Valores["lucro"]
		if !v.OK {
			semDado = true
		} else if v.Numero <= 0 {
			prejuizos++
		}
	}
	switch {
	case semDado:
		lucro.Valor = "-"
	case prejuizos > 0:
		lucro.Valor = fmt.Sprintf("%d prej.", prejuizos)
	default:
		lucro.Valor = fmt.Sprintf("%d anos", len(anuais))
		lucro.OK = true
	}
	av.adicionar(lucro)

	pl := m12.Valores["pl"]
	av.PL = pl.Numero
	av.adicionar(criterio("P/L", pl, "x", func(v float64) bool { return v > 0 && v <= lim.PLMax }))
	av.adicionar(criterio("ROE 5a", media.Valores["roe"], "%", func(v float64) bool { return v >= lim.ROEMin }))
	av.adicionar(criterio("ROIC 5a", media.Valores["roic"], "%", func(v float64) bool { return v >= lim.ROICMin }))
	av.adicionar(criterio("Beta", m12.Valores["beta"], "x", func(v float64) bool { return v <= lim.BetaMax }))
	av.adicionar(criterio("Correl.", m12.Valores["correlacao"], "x", func(v float64) bool { return v <= lim.CorrelMax }))
	return av
}

func criterio(nome string, v indicadores.Valor, unidade string, passa func(float64) bool) Criterio {
	c := Criterio{Nome: nome, Valor: "-"}
	if !v.OK {
		c.NaoAplica = strings.HasPrefix(v.Motivo, "não se aplica")
		if c.NaoAplica {
			c.Valor = "n/a"
		}
		return c
	}
	if unidade == "%" {
		c.Valor = formato.Pct(v.Numero)
	} else {
		c.Valor = formato.Numero(v.Numero, 2)
	}
	c.OK = passa(v.Numero)
	return c
}

func (a *Avaliacao) adicionar(c Criterio) {
	a.Criterios = append(a.Criterios, c)
	if c.NaoAplica {
		return
	}
	a.Total++
	if c.OK {
		a.Cumpridos++
	}
}
