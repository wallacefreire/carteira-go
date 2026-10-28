package risco

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

func perto(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAjustarDesdobramentoEProvento(t *testing.T) {
	precos := []Ponto{
		{dia("2024-04-12"), 56}, {dia("2024-04-15"), 56}, // data-com do desdobramento
		{dia("2024-04-16"), 28.2},                        // cada ação virou 2
		{dia("2024-04-17"), 30}, {dia("2024-04-18"), 29}, // provento de R$ 1 com data-com 17/04
	}
	evs := []Evento{
		{DataCom: dia("2024-04-15"), FatorAcoes: 2, Descricao: "desdobramento"},
		{DataCom: dia("2024-04-17"), Provento: 1, Descricao: "dividendo"},
		{DataCom: dia("2024-04-18"), Provento: 1, Descricao: "sem pregão depois: ignorado"},
	}
	aj, feitos := Ajustar(precos, evs)
	if len(feitos) != 2 {
		t.Fatalf("ajustes: %+v", feitos)
	}
	// Retorno de 15 para 16/04: 28,2 ÷ 28 − 1 (sem o ajuste seria −50%).
	r := Retornos(aj)
	if !perto(r[1].Valor, 28.2/28-1) {
		t.Errorf("retorno no desdobramento = %v", r[1].Valor)
	}
	// Retorno de 17 para 18/04 inclui o provento: (29 + 1) ÷ 30 − 1 = 0.
	if !perto(r[3].Valor, 0) {
		t.Errorf("retorno com provento = %v", r[3].Valor)
	}
	if precos[0].Valor != 56 {
		t.Error("Ajustar não pode mudar a série original")
	}
}

func TestEstatisticas(t *testing.T) {
	m := []float64{0.01, -0.02, 0.03, 0.00, -0.01}
	a := make([]float64, len(m))
	for i, v := range m {
		a[i] = 2*v + 0.001 // ação que oscila o dobro do mercado
	}
	if b := Beta(a, m); !perto(b, 2) {
		t.Errorf("beta = %v, quer 2", b)
	}
	if c := Correlacao(a, m); !perto(c, 1) {
		t.Errorf("correlação = %v, quer 1", c)
	}
	// Desvio amostral de 1, 2, 3, 4 = raiz(5/3)
	if d := Desvio([]float64{1, 2, 3, 4}); !perto(d, math.Sqrt(5.0/3)) {
		t.Errorf("desvio = %v", d)
	}
}

func TestSemanalECarteira(t *testing.T) {
	s := []Ponto{{dia("2026-10-19"), 10}, {dia("2026-10-23"), 11}, {dia("2026-10-26"), 12}, {dia("2026-10-28"), 13}}
	w := Semanal(s)
	if len(w) != 2 || w[0].Valor != 11 || w[1].Valor != 13 || !w[1].Data.Equal(dia("2026-10-26")) {
		t.Fatalf("semanal: %+v", w)
	}
	d1, d2 := dia("2026-10-19"), dia("2026-10-26")
	ret := map[string][]Ponto{
		"A": {{d1, 0.10}, {d2, 0.10}},
		"B": {{d2, -0.10}}, // só negocia na segunda semana
	}
	c := RetornoCarteira(ret, map[string]float64{"A": 0.75, "B": 0.25})
	if len(c) != 2 || !perto(c[0].Valor, 0.10) || !perto(c[1].Valor, 0.75*0.10-0.25*0.10) {
		t.Errorf("carteira: %+v", c)
	}
}
