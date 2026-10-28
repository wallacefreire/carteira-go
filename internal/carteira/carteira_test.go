package carteira

import (
	"math"
	"testing"
	"time"
)

func dia(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func perto(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func conferir(t *testing.T, p *Posicao, qtd, custo, realizado float64) {
	t.Helper()
	if p == nil {
		t.Fatal("posição não encontrada")
	}
	if !perto(p.Quantidade, qtd) || !perto(p.Custo, custo) || !perto(p.Realizado, realizado) {
		t.Errorf("%s: qtd %v custo %v realizado %v; quer %v %v %v",
			p.Ticker, p.Quantidade, p.Custo, p.Realizado, qtd, custo, realizado)
	}
}

func TestPrecoMedioCompraEVenda(t *testing.T) {
	ops := []Operacao{
		{Data: dia("2024-01-10"), Ticker: "BBAS3", Tipo: Compra, Quantidade: 100, Preco: 20},
		{Data: dia("2024-02-10"), Ticker: "BBAS3", Tipo: Compra, Quantidade: 100, Preco: 30},
		// preço médio 25; vender 50 a 40 realiza 50 × 15 = 750
		{Data: dia("2024-03-10"), Ticker: "BBAS3", Tipo: Venda, Quantidade: 50, Preco: 40},
	}
	pos, avisos := CalcularPosicoes(ops)
	conferir(t, pos["BBAS3"], 150, 3750, 750)
	if pm := pos["BBAS3"].PrecoMedio(); !perto(pm, 25) {
		t.Errorf("preço médio %v, quer 25 (venda não muda o preço médio)", pm)
	}
	if len(avisos) != 0 {
		t.Errorf("avisos inesperados: %v", avisos)
	}
}

func TestZerarERecomprarComecaPrecoMedioDoZero(t *testing.T) {
	ops := []Operacao{
		{Data: dia("2024-01-10"), Ticker: "SOJA3", Tipo: Compra, Quantidade: 100, Preco: 10},
		{Data: dia("2024-02-10"), Ticker: "SOJA3", Tipo: Venda, Quantidade: 100, Preco: 12},
		{Data: dia("2024-03-10"), Ticker: "SOJA3", Tipo: Compra, Quantidade: 50, Preco: 20},
	}
	pos, _ := CalcularPosicoes(ops)
	conferir(t, pos["SOJA3"], 50, 1000, 200)
}

func TestDesdobroEBonificacao(t *testing.T) {
	ops := []Operacao{
		{Data: dia("2024-01-10"), Ticker: "WEGE3", Tipo: Compra, Quantidade: 100, Preco: 30},
		// desdobro 1:2 → +100 ações, custo igual, preço médio cai para 15
		{Data: dia("2024-02-10"), Ticker: "WEGE3", Tipo: Ajuste, Quantidade: 100},
		// bonificação de 20 ações a R$ 5 atribuídos → custo +100
		{Data: dia("2024-03-10"), Ticker: "WEGE3", Tipo: Bonificacao, Quantidade: 20, Preco: 5},
	}
	pos, _ := CalcularPosicoes(ops)
	conferir(t, pos["WEGE3"], 220, 3100, 0)
}

func TestVendaMaiorQuePosicaoAvisa(t *testing.T) {
	ops := []Operacao{
		{Data: dia("2024-01-10"), Ticker: "TAEE11", Tipo: Compra, Quantidade: 10, Preco: 30},
		{Data: dia("2024-02-10"), Ticker: "TAEE11", Tipo: Venda, Quantidade: 50, Preco: 35},
	}
	pos, avisos := CalcularPosicoes(ops)
	conferir(t, pos["TAEE11"], 0, 0, 50)
	if len(avisos) != 1 {
		t.Errorf("quer 1 aviso, veio %v", avisos)
	}
}

func TestMesmoDiaEntradaAntesDaSaida(t *testing.T) {
	// A venda vem antes na lista (o extrato da B3 é do mais novo para o mais velho).
	ops := []Operacao{
		{Data: dia("2024-01-10"), Ticker: "BBAS3", Tipo: Venda, Quantidade: 50, Preco: 22},
		{Data: dia("2024-01-10"), Ticker: "BBAS3", Tipo: Compra, Quantidade: 100, Preco: 20},
	}
	pos, avisos := CalcularPosicoes(ops)
	conferir(t, pos["BBAS3"], 50, 1000, 100)
	if len(avisos) != 0 {
		t.Errorf("avisos inesperados: %v", avisos)
	}
}

func TestPesos(t *testing.T) {
	pos := map[string]*Posicao{
		"BBAS3": {Ticker: "BBAS3", Quantidade: 100, Custo: 2000},
		"SOJA3": {Ticker: "SOJA3", Quantidade: 100, Custo: 1000},
		"GRND3": {Ticker: "GRND3", Quantidade: 10, Custo: 50}, // sem cotação
		"VELHA": {Ticker: "VELHA", Quantidade: 0},             // vendida
	}
	linhas, total := Pesos(pos, map[string]float64{"BBAS3": 30, "SOJA3": 10})
	if !perto(total, 4000) {
		t.Fatalf("total %v, quer 4000", total)
	}
	if len(linhas) != 3 || linhas[0].Ticker != "BBAS3" || linhas[1].Ticker != "SOJA3" || linhas[2].Ticker != "GRND3" {
		t.Fatalf("ordem errada: %+v", linhas)
	}
	if !perto(linhas[0].Peso, 0.75) || !perto(linhas[1].Peso, 0.25) || linhas[2].Peso != 0 {
		t.Errorf("pesos %v %v %v", linhas[0].Peso, linhas[1].Peso, linhas[2].Peso)
	}
}

func TestNormalizarTicker(t *testing.T) {
	casos := []struct {
		in   string
		quer string
		ok   bool
	}{
		{"BBAS3 - BANCO DO BRASIL S/A", "BBAS3", true},
		{"bbas3f", "BBAS3", true},
		{"TAEE11", "TAEE11", true},
		{"B3SA3", "B3SA3", true},
		{"Tesouro IPCA+ 2035", "TESOURO IPCA+ 2035", false},
		{"CDB - BANCO XYZ", "CDB", false},
	}
	for _, c := range casos {
		got, ok := NormalizarTicker(c.in)
		if got != c.quer || ok != c.ok {
			t.Errorf("NormalizarTicker(%q) = %q, %v; quer %q, %v", c.in, got, ok, c.quer, c.ok)
		}
	}
}

func TestTrocaDeCodigoLevaOCusto(t *testing.T) {
	ops := []Operacao{
		{Data: dia("2023-01-10"), Ticker: "TRPL4", Tipo: Compra, Quantidade: 5, Preco: 22},
		// A troca vem antes da venda do código novo no mesmo dia.
		{Data: dia("2024-11-19"), Ticker: "ISAE4", Tipo: Venda, Quantidade: 1, Preco: 25},
		{Data: dia("2024-11-19"), Ticker: "ISAE4", Tipo: Troca, Quantidade: 5, TickerAnterior: "TRPL4"},
	}
	pos, avisos := CalcularPosicoes(ops)
	conferir(t, pos["TRPL4"], 0, 0, 0)
	conferir(t, pos["ISAE4"], 4, 88, 3) // preço médio 22 herdado; vender 1 a 25 realiza 3
	if len(avisos) != 0 {
		t.Errorf("avisos inesperados: %v", avisos)
	}
}

func TestNormalizarAtivo(t *testing.T) {
	casos := map[string]string{
		"bbas3":               "BBAS3",
		"btc":                 "BTC",
		"tesouro  Selic 2031": "Tesouro Selic 2031",
		"Tesouro IPCA+ 2050":  "Tesouro IPCA+ 2050",
	}
	for entrada, quer := range casos {
		if got, ok := NormalizarAtivo(entrada); !ok || got != quer {
			t.Errorf("NormalizarAtivo(%q) = %q, %v; quer %q", entrada, got, ok, quer)
		}
	}
	for _, ruim := range []string{"Tesouro Selic", "CDB 2030", "XYZ"} {
		if _, ok := NormalizarAtivo(ruim); ok {
			t.Errorf("%q não devia ser aceito", ruim)
		}
	}
	if !EhTesouro("Tesouro Selic 2031") || EhB3("Tesouro Selic 2031") || !EhB3("TAEE11") {
		t.Error("EhTesouro/EhB3")
	}
}
