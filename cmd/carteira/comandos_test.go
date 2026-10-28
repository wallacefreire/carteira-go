package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"carteira/internal/armazenamento"
	"carteira/internal/carteira"
	"carteira/internal/fontes/brapi"
)

// Importa um extrato, lança uma compra manual e confere a tabela de posição.
func TestImportarEPosicao(t *testing.T) {
	dir := t.TempDir()
	banco, err := armazenamento.Abrir(filepath.Join(dir, "teste.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer banco.Fechar()

	consultas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consultas++
		precos := map[string]string{"/quote/BBAS3": "30", "/quote/SOJA3": "10"}
		p, ok := precos[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":true,"message":"Ticker não encontrado"}`))
			return
		}
		w.Write([]byte(`{"results":[{"regularMarketPrice":` + p + `,"regularMarketTime":"2026-10-28T20:00:00.000Z"}]}`))
	}))
	defer srv.Close()
	cli := brapi.Novo("token")
	cli.URL = srv.URL

	var saida bytes.Buffer
	agora := time.Date(2026, 10, 28, 18, 0, 0, 0, time.Local)
	a := &app{banco: banco, brapi: cli, saida: &saida, agora: func() time.Time { return agora }}

	extrato := filepath.Join(dir, "movimentacao.xlsx")
	criarExtrato(t, extrato, [][]any{
		{"Entrada/Saída", "Data", "Movimentação", "Produto", "Instituição", "Quantidade", "Preço unitário", "Valor da Operação"},
		{"Credito", "10/05/2024", "Transferência - Liquidação", "BBAS3 - BANCO DO BRASIL S/A", "RICO", 100, 20.0, 2000.0},
		{"Credito", "11/05/2024", "Transferência - Liquidação", "SOJA3 - BOA SAFRA SEMENTES S/A", "RICO", 100, 10.0, 1000.0},
		{"Credito", "20/06/2024", "Dividendo", "BBAS3 - BANCO DO BRASIL S/A", "RICO", 100, 0.5, 50.0},
	})
	for range 2 { // a segunda importação não pode duplicar
		if err := a.importar([]string{extrato}); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(saida.String(), "Operações: 0 novas, 2 já estavam no banco") {
		t.Errorf("reimportação devia pular tudo:\n%s", saida.String())
	}

	if err := a.lancar(carteira.Compra, []string{"grnd3", "10", "5,00", "01/03/2018"}); err != nil {
		t.Fatal(err)
	}

	saida.Reset()
	if err := a.posicao(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := saida.String()
	// BBAS3: 100 × 30 = 3.000 de 4.000 → 75% (acima do teto); SOJA3: 1.000 → 25%; GRND3 sem cotação.
	for _, quer := range []string{"3.000,00", "75,0%", "acima do teto de 25,0%", "1.000,00", "4.000,00", "Proventos recebidos: R$ 50,00", "GRND3: sem cotação"} {
		if !strings.Contains(out, quer) {
			t.Errorf("saída sem %q:\n%s", quer, out)
		}
	}

	// Segunda consulta dentro de 15 min usa o cache (só GRND3, que falhou, vai ao brapi de novo).
	antes := consultas
	saida.Reset()
	if err := a.posicao(context.Background()); err != nil {
		t.Fatal(err)
	}
	if consultas-antes != 1 {
		t.Errorf("quer 1 consulta nova (GRND3), houve %d", consultas-antes)
	}
}

func criarExtrato(t *testing.T, caminho string, linhas [][]any) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	for i, l := range linhas {
		celula, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("Sheet1", celula, &l); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.SaveAs(caminho); err != nil {
		t.Fatal(err)
	}
}
