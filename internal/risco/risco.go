// Package risco calcula retornos, volatilidade, beta e correlação a partir
// de séries de preços, e ajusta os preços por proventos e desdobramentos.
package risco

import (
	"math"
	"slices"
	"time"
)

// Ponto é um valor numa data (preço de fechamento ou retorno).
type Ponto struct {
	Data  time.Time
	Valor float64
}

// Evento muda o preço sem mudar o valor do investimento: provento em
// dinheiro (R$ por ação) ou mudança no número de ações.
type Evento struct {
	DataCom    time.Time // último dia com direito
	Provento   float64   // R$ por ação; zero se for evento em ações
	FatorAcoes float64   // em quantas ações cada uma vira; zero se for provento
	Descricao  string
}

// Ajuste é um evento que de fato mudou a série.
type Ajuste struct {
	Data      time.Time
	Provento  bool
	Descricao string
}

// Ajustar devolve os preços corrigidos para trás, como fazem os sites de
// cotação: os preços até a data-com são multiplicados por (1 − provento ÷
// preço da data-com) no caso de provento, e divididos pelo fator no caso de
// desdobramento, grupamento ou bonificação. Assim o retorno do dia seguinte
// à data-com mostra o que o acionista ganhou, e não a queda mecânica do preço.
// Os preços precisam estar em ordem de data.
func Ajustar(precos []Ponto, eventos []Evento) ([]Ponto, []Ajuste) {
	ajustado := slices.Clone(precos)
	var feitos []Ajuste
	for _, e := range eventos {
		// k = último pregão com direito ao evento.
		k := -1
		for i, p := range precos {
			if p.Data.After(e.DataCom) {
				break
			}
			k = i
		}
		// Sem pregão antes ou depois do evento, ele não aparece em nenhum retorno.
		if k < 0 || k == len(precos)-1 {
			continue
		}
		var fator float64
		switch {
		case e.Provento > 0:
			r := e.Provento / precos[k].Valor
			if r >= 0.5 { // provento maior que metade do preço: dado suspeito
				continue
			}
			fator = 1 - r
		case e.FatorAcoes > 0:
			fator = 1 / e.FatorAcoes
		default:
			continue
		}
		for i := 0; i <= k; i++ {
			ajustado[i].Valor *= fator
		}
		feitos = append(feitos, Ajuste{Data: e.DataCom, Provento: e.Provento > 0, Descricao: e.Descricao})
	}
	slices.SortFunc(feitos, func(a, b Ajuste) int { return a.Data.Compare(b.Data) })
	return ajustado, feitos
}

// Entre devolve os pontos com data de de até ate (inclusive).
func Entre(s []Ponto, de, ate time.Time) []Ponto {
	var r []Ponto
	for _, p := range s {
		if !p.Data.Before(de) && !p.Data.After(ate) {
			r = append(r, p)
		}
	}
	return r
}

// Semanal fica com o último fechamento de cada semana. A data de cada ponto
// vira a segunda-feira da semana, para alinhar ativos que não negociaram no
// mesmo dia.
func Semanal(s []Ponto) []Ponto {
	var r []Ponto
	for _, p := range s {
		seg := segunda(p.Data)
		if n := len(r); n > 0 && r[n-1].Data.Equal(seg) {
			r[n-1].Valor = p.Valor
			continue
		}
		r = append(r, Ponto{seg, p.Valor})
	}
	return r
}

func segunda(d time.Time) time.Time {
	dias := (int(d.Weekday()) + 6) % 7 // segunda = 0
	return time.Date(d.Year(), d.Month(), d.Day()-dias, 0, 0, 0, 0, time.UTC)
}

// Retornos: preço ÷ preço anterior − 1, na data do preço mais novo.
func Retornos(s []Ponto) []Ponto {
	var r []Ponto
	for i := 1; i < len(s); i++ {
		if s[i-1].Valor > 0 {
			r = append(r, Ponto{s[i].Data, s[i].Valor/s[i-1].Valor - 1})
		}
	}
	return r
}

// Alinhar devolve os valores das duas séries nas datas que elas têm em comum.
func Alinhar(a, b []Ponto) (x, y []float64) {
	pb := map[time.Time]float64{}
	for _, p := range b {
		pb[p.Data] = p.Valor
	}
	for _, p := range a {
		if v, ok := pb[p.Data]; ok {
			x = append(x, p.Valor)
			y = append(y, v)
		}
	}
	return x, y
}

// Valores tira as datas.
func Valores(s []Ponto) []float64 {
	r := make([]float64, len(s))
	for i, p := range s {
		r[i] = p.Valor
	}
	return r
}

func media(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// Covariancia amostral (divide por n − 1), como COVARIANCIA.S no Excel.
func Covariancia(x, y []float64) float64 {
	if len(x) < 2 || len(x) != len(y) {
		return math.NaN()
	}
	mx, my := media(x), media(y)
	var s float64
	for i := range x {
		s += (x[i] - mx) * (y[i] - my)
	}
	return s / float64(len(x)-1)
}

// Desvio é o desvio-padrão amostral, como DESVPAD no Excel.
func Desvio(x []float64) float64 { return math.Sqrt(Covariancia(x, x)) }

// Correlacao de Pearson, como CORREL no Excel.
func Correlacao(x, y []float64) float64 {
	return Covariancia(x, y) / (Desvio(x) * Desvio(y))
}

// Beta = covariância(ativo, mercado) ÷ variância(mercado).
func Beta(ativo, mercado []float64) float64 {
	return Covariancia(ativo, mercado) / Covariancia(mercado, mercado)
}

// RetornoCarteira soma, em cada data, peso × retorno de cada ativo. Numa data
// em que algum ativo não tem retorno (ainda não negociava), os pesos dos
// outros são reescalados para somar 100%.
func RetornoCarteira(retornos map[string][]Ponto, pesos map[string]float64) []Ponto {
	soma := map[time.Time]float64{}
	pesoNaData := map[time.Time]float64{}
	for t, rs := range retornos {
		w := pesos[t]
		if w <= 0 {
			continue
		}
		for _, p := range rs {
			soma[p.Data] += w * p.Valor
			pesoNaData[p.Data] += w
		}
	}
	var r []Ponto
	for d, s := range soma {
		r = append(r, Ponto{d, s / pesoNaData[d]})
	}
	slices.SortFunc(r, func(a, b Ponto) int { return a.Data.Compare(b.Data) })
	return r
}
