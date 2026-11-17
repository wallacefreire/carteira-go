package main

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"carteira/internal/armazenamento"
	"carteira/internal/carteira"
	"carteira/internal/fontes/bcb"
)

// Duas compras de BTC com dólares diferentes: a parte do ativo e a do câmbio
// têm de somar o resultado em reais.
func TestSepararDolar(t *testing.T) {
	banco, err := armazenamento.Abrir(filepath.Join(t.TempDir(), "teste.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer banco.Fechar()

	dia := func(d, m int) time.Time { return time.Date(2026, time.Month(m), d, 0, 0, 0, 0, time.Local) }
	a := &app{banco: banco, agora: func() time.Time { return dia(16, 11).Add(15 * time.Hour) }}
	// PTAX já "lidas" nesta execução: o teste não vai ao Banco Central.
	a.guardarDolar("ptax_2026-08-25", bcb.Cotacao{Dia: dia(25, 8), Venda: 5})
	a.guardarDolar("ptax_2026-10-21", bcb.Cotacao{Dia: dia(21, 10), Venda: 4})
	a.guardarDolar("ptax_2026-11-16", bcb.Cotacao{Dia: dia(16, 11), Venda: 6})

	ops := []carteira.Operacao{
		{Data: dia(25, 8), Ticker: "BTC", Tipo: carteira.Compra, Quantidade: 0.001, Preco: 500000},  // R$ 500 = US$ 100
		{Data: dia(21, 10), Ticker: "BTC", Tipo: carteira.Compra, Quantidade: 0.001, Preco: 400000}, // R$ 400 = US$ 100
		{Data: dia(21, 10), Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 10, Preco: 20},
	}
	pos, _ := carteira.CalcularPosicoes(ops)
	linhas, _ := carteira.Pesos(pos, map[string]float64{"BTC": 660000, "BBAS3": 25}) // BTC vale R$ 1.320 = US$ 220

	got, err := a.separarDolar(context.Background(), ops, linhas)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Ticker != "BTC" {
		t.Fatalf("devia ter só o BTC: %+v", got)
	}
	l := got[0]
	// Custo US$ 200, dólar médio 900 ÷ 200 = 4,5. Ativo: (220 − 200) × 4,5 = 90.
	// Câmbio: 220 × (6 − 4,5) = 330. Soma 420 = 1.320 − 900.
	perto := func(x, y float64) bool { return math.Abs(x-y) < 1e-6 }
	if !perto(l.ParteAtivoNum, 90) || !perto(l.ParteCambioNum, 330) || !perto(l.ResultadoNum, 420) {
		t.Errorf("ativo %v, câmbio %v, resultado %v", l.ParteAtivoNum, l.ParteCambioNum, l.ResultadoNum)
	}
	if l.CustoUSD != "200,00" || l.ValorUSD != "220,00" || l.DolarCompra != "4,5000" || l.ResultadoUSDPct != "10,0%" || l.CambioPct != "33,3%" {
		t.Errorf("textos %+v", l)
	}
}
