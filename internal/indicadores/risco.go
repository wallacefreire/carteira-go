package indicadores

import (
	"fmt"
	"math"
	"strings"
	"time"

	"carteira/internal/formato"
	"carteira/internal/risco"
)

// Historico são os preços usados no P/L de cada ano e nos indicadores de risco.
type Historico struct {
	Precos   []risco.Ponto  // fechamento sem ajuste (P/L de cada ano)
	Ajustado []risco.Ponto  // ajustado por proventos e eventos em ações
	Ajustes  []risco.Ajuste // eventos aplicados no ajuste
	Indice   []risco.Ponto  // BOVA11 ajustado, no lugar do Ibovespa
	Fonte    string         // ex.: "COTAHIST (B3)"
	Ate      time.Time      // último pregão disponível

	// Carteira: retornos semanais da carteira de hoje sem este ativo, e a
	// descrição dos pesos usados (para a memória de cálculo).
	Carteira      []risco.Ponto
	PesosCarteira string
}

const (
	minPregoes = 100 // por ano
	minSemanas = 52  // nos 2 anos
	pregoesAno = 252
)

func ultimoAte(s []risco.Ponto, d time.Time) (risco.Ponto, bool) {
	var achou risco.Ponto
	ok := false
	for _, p := range s {
		if p.Data.After(d) {
			break
		}
		achou, ok = p, true
	}
	return achou, ok
}

// riscoAno: beta e volatilidade com os retornos diários do ano.
func riscoAno(col Coluna, h Historico, ano int) {
	col.Valores["correlacao"] = Valor{Motivo: "calculada só para os últimos 2 anos (coluna 12m)"}
	if len(h.Ajustado) == 0 {
		col.Valores["beta"] = Valor{Motivo: motivoHistorico}
		col.Valores["volatilidade"] = Valor{Motivo: motivoHistorico}
		return
	}
	de := time.Date(ano, 1, 1, 0, 0, 0, 0, time.UTC)
	ate := time.Date(ano, 12, 31, 0, 0, 0, 0, time.UTC)
	ret := risco.Retornos(risco.Entre(h.Ajustado, de, ate))
	periodo := fmt.Sprintf("retornos diários de %s a %s", formato.Data(de), formato.Data(ate))
	col.Valores["volatilidade"] = volatilidade(ret, periodo, h, de, ate)
	col.Valores["beta"] = beta(ret, risco.Retornos(risco.Entre(h.Indice, de, ate)), minPregoes, "Pregões usados", periodo, pregoesAno, h, de, ate)
}

// risco12m: beta e correlação semanais de 2 anos; volatilidade dos últimos 252 pregões.
func risco12m(col Coluna, h Historico) {
	if len(h.Ajustado) == 0 {
		for _, k := range []string{"beta", "volatilidade", "correlacao"} {
			col.Valores[k] = Valor{Motivo: motivoHistorico}
		}
		return
	}
	ate := h.Ate
	de2 := ate.AddDate(-2, 0, 0)
	semanal := risco.Retornos(risco.Semanal(risco.Entre(h.Ajustado, de2, ate)))
	periodo2 := fmt.Sprintf("retornos semanais de %s a %s", formato.Data(de2), formato.Data(ate))
	col.Valores["beta"] = beta(semanal, risco.Retornos(risco.Semanal(risco.Entre(h.Indice, de2, ate))),
		minSemanas, "Semanas usadas", periodo2, 52, h, de2, ate)

	diario := risco.Entre(h.Ajustado, ate.AddDate(-1, 0, -7), ate)
	if len(diario) > pregoesAno+1 {
		diario = diario[len(diario)-pregoesAno-1:]
	}
	ret := risco.Retornos(diario)
	periodo := "últimos 252 pregões"
	if len(ret) > 0 {
		periodo = fmt.Sprintf("retornos diários de %s a %s", formato.Data(diario[0].Data), formato.Data(ate))
	}
	col.Valores["volatilidade"] = volatilidade(ret, periodo, h, diario[0].Data, ate)

	if len(h.Carteira) == 0 {
		col.Valores["correlacao"] = Valor{Motivo: "carteira sem outros ativos com histórico de preços"}
		return
	}
	x, y := risco.Alinhar(semanal, h.Carteira)
	if len(x) < minSemanas {
		col.Valores["correlacao"] = Valor{Motivo: fmt.Sprintf("poucas semanas em comum com a carteira (%d)", len(x))}
		return
	}
	col.Valores["correlacao"] = Valor{
		Numero: risco.Correlacao(x, y), OK: true,
		Partes: []Parte{
			{Nome: "Semanas usadas", Valor: float64(len(x))},
			{Nome: "Volatilidade semanal anualizada do ativo", Valor: risco.Desvio(x) * math.Sqrt(52), Unidade: "%"},
			{Nome: "Volatilidade semanal anualizada da carteira", Valor: risco.Desvio(y) * math.Sqrt(52), Unidade: "%"},
		},
		Nota: periodo2 + " · carteira (pesos de hoje, sem este ativo): " + h.PesosCarteira + notaAjustes(h, de2, ate),
	}
}

func volatilidade(ret []risco.Ponto, periodo string, h Historico, de, ate time.Time) Valor {
	if len(ret) < minPregoes {
		return Valor{Motivo: fmt.Sprintf("poucos pregões (%d)", len(ret))}
	}
	d := risco.Desvio(risco.Valores(ret))
	return Valor{
		Numero: d * math.Sqrt(pregoesAno), OK: true,
		Partes: []Parte{
			{Nome: "Pregões usados", Valor: float64(len(ret))},
			{Nome: "Desvio-padrão dos retornos diários", Valor: d, Unidade: "%"},
		},
		Nota: periodo + " · anualizado: desvio diário × √252" + notaAjustes(h, de, ate),
	}
}

// beta com a memória em termos fáceis de conferir:
// beta = correlação × volatilidade da ação ÷ volatilidade do BOVA11.
func beta(ativo, indice []risco.Ponto, minimo int, nomeN, periodo string, porAno float64, h Historico, de, ate time.Time) Valor {
	x, y := risco.Alinhar(ativo, indice)
	if len(x) < minimo {
		return Valor{Motivo: fmt.Sprintf("poucos retornos em comum com o BOVA11 (%d)", len(x))}
	}
	return Valor{
		Numero: risco.Beta(x, y), OK: true,
		Partes: []Parte{
			{Nome: nomeN, Valor: float64(len(x))},
			{Nome: "Correlação com o BOVA11", Valor: risco.Correlacao(x, y), Unidade: "x"},
			{Nome: "Volatilidade anualizada da ação", Valor: risco.Desvio(x) * math.Sqrt(porAno), Unidade: "%"},
			{Nome: "Volatilidade anualizada do BOVA11", Valor: risco.Desvio(y) * math.Sqrt(porAno), Unidade: "%"},
		},
		Nota: periodo + " · beta = correlação × volatilidade da ação ÷ volatilidade do BOVA11 (o ETF que replica o Ibovespa)" + notaAjustes(h, de, ate),
	}
}

// notaAjustes resume os eventos que entraram no ajuste dos preços do período.
func notaAjustes(h Historico, de, ate time.Time) string {
	proventos := 0
	var outros []string
	for _, a := range h.Ajustes {
		if a.Data.Before(de) || a.Data.After(ate) {
			continue
		}
		if a.Provento {
			proventos++
			continue
		}
		outros = append(outros, a.Descricao)
	}
	if proventos == 0 && len(outros) == 0 {
		return " · preços do " + h.Fonte + ", sem proventos nem eventos no período"
	}
	s := fmt.Sprintf(" · preços do %s ajustados por %d proventos", h.Fonte, proventos)
	if len(outros) > 0 {
		s += " e por " + strings.Join(outros, ", ")
	}
	return s
}
