package baixar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModificado(t *testing.T) {
	quando := time.Date(2026, 11, 8, 10, 44, 33, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/falta.zip" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodHead {
			t.Errorf("devia usar HEAD, usou %s", r.Method)
		}
		w.Header().Set("Last-Modified", quando.Format(http.TimeFormat))
	}))
	defer srv.Close()

	got, err := Modificado(context.Background(), srv.Client(), srv.URL+"/itr.zip")
	if err != nil || !got.Equal(quando) {
		t.Errorf("Modificado = %v, %v; quer %v", got, err, quando)
	}
	if _, err := Modificado(context.Background(), srv.Client(), srv.URL+"/falta.zip"); !errors.Is(err, ErrNaoExiste) {
		t.Errorf("arquivo que falta: erro %v", err)
	}
}
