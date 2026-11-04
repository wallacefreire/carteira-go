// Package radar aplica o filtro do Wallace aos indicadores de uma ação.
// Nenhum critério elimina a ação sozinho: cada um vira ✓ ou ✗, e o radar
// ordena pela quantidade de critérios cumpridos.
package radar

import (
	"cmp"
	"fmt"
	"math"
	"slices"
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
	DividaMax float64 // dívida líquida ÷ EBITDA de 12 meses (não vale para bancos)
	ValorMin  float64 // valor de mercado mínimo, em R$
}

// Padrao são os limites escolhidos pelo Wallace em 28/10/2026 (ROIC mais
// baixo porque é média de 5 anos, que inclui anos ruins). Em 03/11/2026,
// pensando em segurar por muitos anos: ROE 12%, dívida até 2× o EBITDA e
// valor de mercado de pelo menos R$ 1 bi.
var Padrao = Limites{PLMax: 15, ROEMin: 0.12, ROICMin: 0.08, BetaMax: 1.2, CorrelMax: 0.6, DividaMax: 2, ValorMin: 1e9}

// Pesos da fila, que desempata ações com a mesma nota (03/11/2026).
const (
	pesoQualidade = 0.4  // ROIC médio de 3 anos (ROE em banco)
	pesoPreco     = 0.4  // 75% P/L e 25% P/VP
	pesoBeta      = 0.2  // menor beta primeiro
	pesoPLNoPreco = 0.75 // o resto do preço é o P/VP
)

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
	PL             float64  // para a fila
	EVEbitda       string   // só informativo: não conta na nota
	PVP            string   // só informativo
	Setor          string   // preenchido por quem chama (vem do cadastro da CVM)
	Fila           float64  // menor = melhor; +Inf sem dados para a fila

	pvp, beta, qualidade float64
	temFila              bool
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
	av.adicionar(criterio("Dívida", m12.Valores["dl_ebitda"], "x", func(v float64) bool { return v <= lim.DividaMax }))
	vm := Criterio{Nome: "Valor merc.", Valor: "-"}
	if r.ValorMercado > 0 {
		vm.Valor = formato.Numero(r.ValorMercado/1e9, 1) + " bi"
		vm.OK = r.ValorMercado >= lim.ValorMin
	}
	av.adicionar(vm)
	av.adicionar(criterio("Beta", m12.Valores["beta"], "x", func(v float64) bool { return v <= lim.BetaMax }))
	av.adicionar(criterio("Correl.", m12.Valores["correlacao"], "x", func(v float64) bool { return v <= lim.CorrelMax }))
	av.EVEbitda = criterio("EV/EBITDA", m12.Valores["ev_ebitda"], "x", func(float64) bool { return true }).Valor
	pvp := m12.Valores["pvp"]
	av.PVP = criterio("P/VP", pvp, "x", func(float64) bool { return true }).Valor

	// Dados da fila: P/L e P/VP positivos, beta e qualidade dos 3 últimos anos.
	chave := "roic"
	if r.Financeira {
		chave = "roe"
	}
	var soma float64
	n := 0
	for _, c := range anuais[max(0, len(anuais)-3):] {
		if v := c.Valores[chave]; v.OK {
			soma += v.Numero
			n++
		}
	}
	beta := m12.Valores["beta"]
	if pl.OK && pl.Numero > 0 && pvp.OK && pvp.Numero > 0 && beta.OK && n > 0 {
		av.pvp, av.beta, av.qualidade, av.temFila = pvp.Numero, beta.Numero, soma/float64(n), true
	}
	return av
}

// Ordenar calcula a fila e ordena: maior nota primeiro; na mesma nota, menor
// fila. A fila soma a posição de cada ação (1 = melhor) entre as que têm os
// dados: 40% qualidade (ROIC médio de 3 anos; ROE em banco), 40% preço (75%
// P/L, 25% P/VP) e 20% beta. Sem dados, a ação vai para o fim da sua nota.
func Ordenar(avs []Avaliacao) {
	var com []int
	for i := range avs {
		avs[i].Fila = math.Inf(1)
		if avs[i].temFila {
			com = append(com, i)
		}
	}
	posQ := posicoes(avs, com, func(a Avaliacao) float64 { return -a.qualidade })
	posPL := posicoes(avs, com, func(a Avaliacao) float64 { return a.PL })
	posPVP := posicoes(avs, com, func(a Avaliacao) float64 { return a.pvp })
	posB := posicoes(avs, com, func(a Avaliacao) float64 { return a.beta })
	for _, i := range com {
		preco := pesoPLNoPreco*posPL[i] + (1-pesoPLNoPreco)*posPVP[i]
		avs[i].Fila = pesoQualidade*posQ[i] + pesoPreco*preco + pesoBeta*posB[i]
	}
	slices.SortStableFunc(avs, func(x, y Avaliacao) int {
		if c := cmp.Compare(y.Nota(), x.Nota()); c != 0 {
			return c
		}
		return cmp.Compare(x.Fila, y.Fila)
	})
}

// posicoes devolve a posição (1 = menor chave) de cada índice de com; empate
// fica com a mesma posição.
func posicoes(avs []Avaliacao, com []int, chave func(Avaliacao) float64) map[int]float64 {
	ordem := slices.Clone(com)
	slices.SortStableFunc(ordem, func(a, b int) int { return cmp.Compare(chave(avs[a]), chave(avs[b])) })
	pos := map[int]float64{}
	p := 0
	for k, i := range ordem {
		if k == 0 || chave(avs[i]) != chave(avs[ordem[k-1]]) {
			p = k + 1
		}
		pos[i] = float64(p)
	}
	return pos
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
