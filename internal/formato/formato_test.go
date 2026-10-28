package formato

import "testing"

func TestNumero(t *testing.T) {
	casos := []struct {
		v     float64
		casas int
		quer  string
	}{
		{1234567.891, 2, "1.234.567,89"},
		{999, 2, "999,00"},
		{1000, 0, "1.000"},
		{-1234.5, 2, "-1.234,50"},
		{-0.001, 2, "0,00"},
	}
	for _, c := range casos {
		if got := Numero(c.v, c.casas); got != c.quer {
			t.Errorf("Numero(%v, %d) = %q, quer %q", c.v, c.casas, got, c.quer)
		}
	}
}

func TestQuantidade(t *testing.T) {
	casos := map[float64]string{100: "100", 1500: "1.500", 0.5: "0,5", 0.00123: "0,00123"}
	for v, quer := range casos {
		if got := Quantidade(v); got != quer {
			t.Errorf("Quantidade(%v) = %q, quer %q", v, got, quer)
		}
	}
}

func TestLerNumero(t *testing.T) {
	casos := map[string]float64{
		"1.234,56":    1234.56,
		"27,50":       27.5,
		"27.50":       27.5,
		"R$ 1.000,00": 1000,
		"100":         100,
	}
	for s, quer := range casos {
		got, err := LerNumero(s)
		if err != nil || got != quer {
			t.Errorf("LerNumero(%q) = %v, %v; quer %v", s, got, err, quer)
		}
	}
	for _, s := range []string{"", "-", "abc", "NaN"} {
		if _, err := LerNumero(s); err == nil {
			t.Errorf("LerNumero(%q) devia falhar", s)
		}
	}
}
