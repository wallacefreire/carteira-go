package brapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCotacao(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/quote/BBAS3" {
			t.Errorf("caminho %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer segredo" {
			t.Errorf("Authorization %q", got)
		}
		w.Write([]byte(`{"results":[{"symbol":"BBAS3","regularMarketPrice":27.43,"regularMarketTime":"2026-10-28T20:07:00.000Z"}],"requestedAt":"x","took":"1ms"}`))
	}))
	defer srv.Close()

	c := Novo("segredo")
	c.URL = srv.URL
	got, err := c.Cotacao(context.Background(), "BBAS3")
	if err != nil {
		t.Fatal(err)
	}
	quer := time.Date(2026, 10, 28, 20, 7, 0, 0, time.UTC)
	if got.Preco != 27.43 || !got.Horario.Equal(quer) {
		t.Errorf("cotação %+v", got)
	}
}

func TestCotacaoErroDaAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":true,"message":"Token inválido"}`))
	}))
	defer srv.Close()

	c := Novo("")
	c.URL = srv.URL
	_, err := c.Cotacao(context.Background(), "SOJA3")
	if err == nil || !strings.Contains(err.Error(), "Token inválido") {
		t.Fatalf("erro devia trazer a mensagem da API, veio %v", err)
	}
}

func TestHorarioEmSegundos(t *testing.T) {
	got, err := lerHorario([]byte(`1793218020`))
	if err != nil || got.Unix() != 1793218020 {
		t.Fatalf("%v %v", got, err)
	}
}
