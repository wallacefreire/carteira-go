package b3

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Linhas reais do COTAHIST_A2026 (BBAS3, TAEE11) e uma de mercado fracionário (ignorada).
const amostra = "00COTAHIST.2026BOVESPA 20261027\r\n" +
	"012026010202BBAS3       010BRASIL      ON      NM   R$  000000000220500000000022200000000002163000000000217800000000021680000000002168000000000216917599000000000016077000000000035017300000000000000000009999123100000010000000000000BRBBASACNOR3334\r\n" +
	"012026010202TAEE11      010TAESA       UNT     N2   R$  000000000422000000000042500000000004197000000000421700000000041970000000004197000000000420305310000000000001404100000000005922168900000000000000009999123100000010000000000000BRTAEECDAM10153\r\n" +
	"012026010296BBAS3F      020BRASIL      ON      NM   R$  000000000220500000000022200000000002163000000000217800000000021680000000002168000000000216917599000000000016077000000000035017300000000000000000009999123100000010000000000000BRBBASACNOR3334\r\n"

func TestLerCotahist(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "COTAHIST_A2026.ZIP")
	f, err := os.Create(caminho)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("COTAHIST_A2026.TXT")
	w.Write([]byte(amostra))
	z.Close()
	f.Close()

	ps, err := LerCotahist(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("quer 2 preços (sem o fracionário), veio %+v", ps)
	}
	if p := ps[0]; p.Ticker != "BBAS3" || p.Fechamento != 21.68 || p.ISIN != "BRBBASACNOR3" || p.Data.Format("2006-01-02") != "2026-01-02" {
		t.Errorf("BBAS3: %+v", p)
	}
	if p := ps[1]; p.Ticker != "TAEE11" || p.Fechamento != 41.97 {
		t.Errorf("TAEE11: %+v", p)
	}
}

func TestEventos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		partes := strings.Split(r.URL.Path, "/")
		metodo := partes[len(partes)-2]
		js, _ := base64.StdEncoding.DecodeString(partes[len(partes)-1])
		var pedido map[string]any
		json.Unmarshal(js, &pedido)
		switch metodo {
		case "GetListedSupplementCompany":
			w.Write([]byte(`[{"code":"BBAS","tradingName":"BRASIL      ","stockDividends":[
				{"isinCode":"BRBBASACNOR3","factor":"100,00000000000","label":"DESDOBRAMENTO","lastDatePrior":"15/04/2024"},
				{"isinCode":"BRBBASACNOR3","factor":"0,00100000000","label":"GRUPAMENTO","lastDatePrior":"23/01/2004"}]}]`))
		case "GetListedCashDividends":
			if pedido["tradingName"] != "BRASIL      " {
				t.Errorf("nome de pregão sem os espaços: %q", pedido["tradingName"])
			}
			if pedido["pageNumber"].(float64) == 1 {
				w.Write([]byte(`{"page":{"totalPages":2},"results":[{"typeStock":"ON","valueCash":"0,10245643978","corporateAction":"JRS CAP PROPRIO","lastDatePriorEx":"01/09/2026"}]}`))
			} else {
				w.Write([]byte(`{"page":{"totalPages":2},"results":[{"typeStock":"ON","valueCash":"0,5","corporateAction":"DIVIDENDO","lastDatePriorEx":"01/03/2021"}]}`))
			}
		}
	}))
	defer srv.Close()

	c := &Cliente{HTTP: srv.Client(), Base: srv.URL + "/"}
	evs, err := c.Eventos(context.Background(), "BBAS")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 {
		t.Fatalf("quer 2 eventos em ações + 2 proventos (2 páginas), veio %+v", evs)
	}
	if f, ok := evs[0].FatorAcoes(); !ok || f != 2 {
		t.Errorf("desdobramento de 100%% = cada ação vira 2, veio %v", f)
	}
	if f, ok := evs[1].FatorAcoes(); !ok || f != 0.001 {
		t.Errorf("grupamento: fator %v", f)
	}
	if e := evs[2]; !e.Dinheiro || e.Especie != "ON" || e.Valor != 0.10245643978 {
		t.Errorf("provento: %+v", e)
	}
}

func TestEspecieDoTicker(t *testing.T) {
	for tk, quer := range map[string]string{"BBAS3": "ON", "ISAE4": "PN", "TAEE11": "UNT", "USIM5": "PNA"} {
		if got := EspecieDoTicker(tk); got != quer {
			t.Errorf("%s: %q, quer %q", tk, got, quer)
		}
	}
}
