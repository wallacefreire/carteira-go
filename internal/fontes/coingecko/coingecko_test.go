package coingecko

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoricoBitcoin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("days") != "30" {
			t.Errorf("dias %q", r.URL.RawQuery)
		}
		// Formato real (22/11/2026): o último ponto é o preço de agora, no mesmo dia do anterior.
		w.Write([]byte(`{"prices":[[1785024000000,327469.91],[1785110400000,332208.00],[1785150000000,333000.50]]}`))
	}))
	defer srv.Close()

	c := Novo()
	c.URLHistorico = srv.URL + "/?days="
	ps, err := c.HistoricoBitcoin(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Dia.Format("2006-01-02") != "2026-07-26" || ps[1].Valor != 333000.50 {
		t.Errorf("histórico %+v", ps)
	}
}
