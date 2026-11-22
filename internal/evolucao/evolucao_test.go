package evolucao

import (
	"math"
	"testing"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/risco"
)

func data(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func perto(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCalcularAporteNaoEhGanho(t *testing.T) {
	dias := []time.Time{data("2026-01-05"), data("2026-01-06"), data("2026-01-07"), data("2026-01-08")}
	e := Entrada{
		Dias: dias,
		Operacoes: []carteira.Operacao{
			{Data: data("2026-01-05"), Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 10, Preco: 10},
			// Aporte no meio: dobra o patrimônio, mas não é rendimento.
			{Data: data("2026-01-07"), Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 10, Preco: 11},
			// Compra num sábado entra no pregão seguinte (não há neste teste: fica de fora).
			{Data: data("2026-01-10"), Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 1, Preco: 11},
		},
		Proventos: []carteira.Provento{{Data: data("2026-01-08"), Ticker: "BBAS3", Valor: 4}},
		Precos: map[string][]risco.Ponto{"BBAS3": {
			{Data: data("2026-01-05"), Valor: 10}, {Data: data("2026-01-06"), Valor: 11},
			{Data: data("2026-01-07"), Valor: 11}, {Data: data("2026-01-08"), Valor: 11},
		}},
	}
	c := Calcular(e)
	if len(c.Dias) != 4 {
		t.Fatalf("dias: %d", len(c.Dias))
	}
	// 05: 1 · 06: +10% · 07: aporte ao preço do dia, sem ganho · 08: provento de 4 sobre 220 = +1,82%
	esperado := []float64{1, 1.1, 1.1, 1.1 * (220 + 4) / 220}
	for i, v := range esperado {
		if !perto(c.Cota[i], v) {
			t.Errorf("cota dia %d = %v, esperado %v", i, c.Cota[i], v)
		}
	}
	if c.Patrimonio[3] != 220 || c.Investido[3] != 210 {
		t.Errorf("patrimônio %v, investido %v", c.Patrimonio[3], c.Investido[3])
	}
	if len(c.SemPreco) != 0 {
		t.Errorf("sem preço: %v", c.SemPreco)
	}
}

func TestCalcularVendaESemPreco(t *testing.T) {
	dias := []time.Time{data("2026-01-05"), data("2026-01-06"), data("2026-01-07")}
	e := Entrada{
		Dias: dias,
		Operacoes: []carteira.Operacao{
			{Data: data("2026-01-04"), Ticker: "BABA34", Tipo: carteira.Compra, Quantidade: 10, Preco: 20}, // domingo
			{Data: data("2026-01-07"), Ticker: "BABA34", Tipo: carteira.Venda, Quantidade: 10, Preco: 22},
		},
	}
	c := Calcular(e)
	// Sem preço de mercado: vale o da compra até a venda, que realiza +10%.
	if !perto(c.Cota[1], 1) || !perto(c.Cota[2], 1.1) || c.Patrimonio[2] != 0 || c.Investido[2] != -20 {
		t.Errorf("cota %v, patrimônio %v, investido %v", c.Cota, c.Patrimonio, c.Investido)
	}
	if len(c.SemPreco) != 1 || c.SemPreco[0] != "BABA34" {
		t.Errorf("sem preço: %v", c.SemPreco)
	}
}

func TestIndices(t *testing.T) {
	dias := []time.Time{data("2026-01-30"), data("2026-02-02"), data("2026-02-03")}
	p := IndicePrecos(dias, []risco.Ponto{{Data: data("2026-01-30"), Valor: 100}, {Data: data("2026-02-03"), Valor: 105}})
	if p[0] != 1 || p[1] != 1 || !perto(p[2], 1.05) {
		t.Errorf("preços %v", p)
	}
	cdi := IndiceCDI(dias, []risco.Ponto{{Data: data("2026-01-30"), Valor: 0.05}, {Data: data("2026-02-02"), Valor: 0.05}})
	if cdi[0] != 1 || !perto(cdi[1], 1.0005) || !perto(cdi[2], 1.0005*1.0005) {
		t.Errorf("CDI %v", cdi)
	}
	ipca := IndiceIPCA(dias, []risco.Ponto{{Data: data("2026-01-01"), Valor: 1}})
	// Fevereiro sem IPCA publicado.
	if ipca[0] != 1 || !math.IsNaN(ipca[1]) {
		t.Errorf("IPCA %v", ipca)
	}
	ipca = IndiceIPCA([]time.Time{data("2026-01-15"), data("2026-01-31")}, []risco.Ponto{{Data: data("2026-01-01"), Valor: 1}})
	if !perto(ipca[1], 1.01/math.Pow(1.01, 15.0/31)) {
		t.Errorf("IPCA no mês %v", ipca)
	}
}

func TestPeriodosERetorno(t *testing.T) {
	dias := []time.Time{data("2025-06-02"), data("2025-12-30"), data("2026-01-02"), data("2026-06-01"), data("2026-11-20")}
	ps := Periodos(dias)
	rotulos := ""
	for _, p := range ps {
		rotulos += p.Rotulo + ";"
	}
	if rotulos != "2025;2026;12 meses;Desde o início;" {
		t.Fatalf("períodos %q", rotulos)
	}
	// 2026 vai do último pregão de 2025 ao último de 2026.
	if ps[1].Ini != 1 || ps[1].Fim != 4 || !ps[0].Parcial || !ps[1].Parcial {
		t.Errorf("2026: %+v / 2025: %+v", ps[1], ps[0])
	}
	// 12 meses: a base é o último pregão até 20/11/2025, que é 02/06/2025.
	if ps[2].Ini != 0 || ps[2].Fim != 4 {
		t.Errorf("12 meses: %+v", ps[2])
	}
	idx := []float64{1, 1.1, 1.2, 1.32, math.NaN()}
	r, ate, ok := Retorno(idx, ps[1])
	if !ok || ate != 3 || !perto(r, 0.2) {
		t.Errorf("retorno %v até %d (%v)", r, ate, ok)
	}
}
