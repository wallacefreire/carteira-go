package bcb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Resposta real da API (16/11/2026), encurtada.
const resposta = `{"@odata.context":"x","value":[
{"cotacaoCompra":5.65120,"cotacaoVenda":5.65180,"dataHoraCotacao":"2026-11-13 13:03:27.375047"},
{"cotacaoCompra":5.73180,"cotacaoVenda":5.73240,"dataHoraCotacao":"2026-11-16 13:06:32.509132"}]}`

func TestDolar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/CotacaoDolarPeriodo(dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)" {
			t.Errorf("caminho %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("@dataInicial") != "'11-06-2026'" || q.Get("@dataFinalCotacao") != "'11-16-2026'" {
			t.Errorf("datas %q", r.URL.RawQuery)
		}
		w.Write([]byte(resposta))
	}))
	defer srv.Close()

	c := Novo()
	c.URL = srv.URL
	got, err := c.Dolar(context.Background(), time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got.Venda != 5.7324 || got.Compra != 5.7318 || got.Dia.Format("02/01/2006") != "16/11/2026" {
		t.Errorf("cotação %+v", got)
	}
}

func TestDolarSemCotacao(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()

	c := Novo()
	c.URL = srv.URL
	if _, err := c.Dolar(context.Background(), time.Now()); err == nil {
		t.Fatal("devia dar erro sem cotação")
	}
}

func TestSerie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bcdata.sgs.12/dados" {
			t.Errorf("caminho %q", r.URL.Path)
		}
		if q := r.URL.Query(); q.Get("dataInicial") != "01/11/2026" || q.Get("dataFinal") != "05/11/2026" {
			t.Errorf("datas %q", r.URL.RawQuery)
		}
		// Resposta real (22/11/2026), encurtada.
		w.Write([]byte(`[{"data":"03/11/2026","valor":"0.050788"},{"data":"04/11/2026","valor":"0.050788"}]`))
	}))
	defer srv.Close()

	c := Novo()
	c.URLSGS = srv.URL + "/bcdata.sgs.%d/dados"
	vs, err := c.Serie(context.Background(), SerieCDI, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Valor != 0.050788 || vs[1].Data.Format("02/01/2006") != "04/11/2026" {
		t.Errorf("série %+v", vs)
	}
}
