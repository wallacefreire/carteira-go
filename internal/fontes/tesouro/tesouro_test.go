package tesouro

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLer(t *testing.T) {
	csv := "Tipo Titulo;Data Vencimento;Data Base;Taxa Compra Manha;Taxa Venda Manha;PU Compra Manha;PU Venda Manha;PU Base Manha\n" +
		"Tesouro Selic;01/03/2031;26/10/2026;0,10;0,11;20084,44;20065,45;20065,45\n" +
		"Tesouro Selic;01/03/2031;27/10/2026;0,10;0,11;20094,02;20075,03;20075,03\n" +
		"Tesouro IPCA+;15/05/2035;27/10/2026;7,50;7,62;2100,00;2080,50;2080,50\n"
	caminho := filepath.Join(t.TempDir(), "precos.csv")
	if err := os.WriteFile(caminho, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := Ler(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if p := ps["Tesouro Selic 2031"]; p.PUVenda != 20075.03 || p.DataBase.Format("2006-01-02") != "2026-10-27" {
		t.Errorf("Selic 2031: %+v", p)
	}
	if p := ps["Tesouro IPCA+ 2035"]; p.PUVenda != 2080.5 {
		t.Errorf("IPCA+ 2035: %+v", p)
	}
}
