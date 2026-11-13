package indicadores

import "testing"

func TestTrimestres(t *testing.T) {
	// Ano 2025 = 120; 9M25 = 90 → 4T25 = 30. Em 2026: 3M = 30, 6M = 80, 9M = 100.
	var contas = []cvmConta{
		{"lucro", "2025-12-31", "2025-01-01", "2025-12-31", 120},
		{"lucro", "2025-09-30", "2025-01-01", "2025-09-30", 90},
		{"lucro", "2026-03-31", "2026-01-01", "2026-03-31", 30},
		{"lucro", "2026-06-30", "2026-01-01", "2026-06-30", 80},
		{"lucro", "2026-09-30", "2026-01-01", "2026-09-30", 100},
		{"lucro_descontinuadas", "2026-09-30", "2026-01-01", "2026-09-30", 15},
		{"lucro_descontinuadas", "2026-06-30", "2026-01-01", "2026-06-30", 5},
	}
	c := calc{}
	for _, x := range contas {
		c.contas = append(c.contas, fluxo(x.chave, false, x.refer, x.ini, x.fim, x.v))
	}
	cols := c.trimestres(c.periodosAnuais(), false)
	quer := map[string][2]float64{ // lucro, lucro sem descontinuadas
		"4T25": {30, 30},
		"1T26": {30, 30},
		"2T26": {50, 45}, // 80−30; sem: (80−5) − 30
		"3T26": {20, 10}, // 100−80; sem: (100−15) − (80−5)
	}
	achou := 0
	for _, col := range cols {
		q, ok := quer[col.Rotulo]
		if !ok {
			continue
		}
		achou++
		if v := col.Valores["lucro"]; !v.OK || !perto(v.Numero, q[0]) {
			t.Errorf("%s lucro = %+v, quer %v", col.Rotulo, v, q[0])
		}
		if v := col.Valores["lucro_rec"]; !v.OK || !perto(v.Numero, q[1]) {
			t.Errorf("%s lucro sem descontinuadas = %+v, quer %v", col.Rotulo, v, q[1])
		}
	}
	if achou != len(quer) {
		t.Errorf("achou %d dos %d trimestres: %+v", achou, len(quer), cols)
	}
	if cols[len(cols)-1].Rotulo != "3T26" {
		t.Errorf("último trimestre = %s", cols[len(cols)-1].Rotulo)
	}
}

type cvmConta struct {
	chave, refer, ini, fim string
	v                      float64
}
