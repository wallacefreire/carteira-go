package armazenamento

import (
	"path/filepath"
	"testing"
	"time"

	"carteira/internal/carteira"
)

func abrirTeste(t *testing.T) *Banco {
	t.Helper()
	b, err := Abrir(filepath.Join(t.TempDir(), "teste.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Fechar() })
	return b
}

func TestImportarDuasVezesNaoDuplica(t *testing.T) {
	b := abrirTeste(t)
	d := time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC)
	ops := []carteira.Operacao{
		{Data: d, Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 100, Preco: 27.5, Origem: "b3-movimentacao", Chave: "a"},
		{Data: d, Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 100, Preco: 27.5, Origem: "b3-movimentacao", Chave: "b"},
	}
	provs := []carteira.Provento{{Data: d, Ticker: "BBAS3", Tipo: "Dividendo", Valor: 85, Origem: "b3-movimentacao", Chave: "c"}}

	n, p, err := b.Importar(ops, provs)
	if err != nil || n != 2 || p != 1 {
		t.Fatalf("1ª importação: %d ops, %d proventos, %v", n, p, err)
	}
	n, p, err = b.Importar(ops, provs)
	if err != nil || n != 0 || p != 0 {
		t.Fatalf("2ª importação devia pular tudo: %d ops, %d proventos, %v", n, p, err)
	}
	if total, _ := b.TotalProventos(); total != 85 {
		t.Errorf("total de proventos %v, quer 85", total)
	}
}

func TestManualSemChavePodeRepetir(t *testing.T) {
	b := abrirTeste(t)
	op := carteira.Operacao{Data: time.Date(2018, 3, 1, 0, 0, 0, 0, time.UTC), Ticker: "BBAS3", Tipo: carteira.Compra, Quantidade: 10, Preco: 15, Origem: "manual"}
	for range 2 {
		if _, err := b.InserirOperacao(op); err != nil {
			t.Fatal(err)
		}
	}
	ops, err := b.Operacoes("BBAS3")
	if err != nil || len(ops) != 2 {
		t.Fatalf("quer 2 operações manuais iguais, veio %d (%v)", len(ops), err)
	}
	if !ops[0].Data.Equal(op.Data) || ops[0].Tipo != carteira.Compra || ops[0].Chave != "" {
		t.Errorf("operação lida errado: %+v", ops[0])
	}

	ok, err := b.RemoverOperacao(ops[0].ID)
	if err != nil || !ok {
		t.Fatalf("remover: %v %v", ok, err)
	}
	if ok, _ := b.RemoverOperacao(ops[0].ID); ok {
		t.Error("remover duas vezes devia devolver false")
	}
}

func TestCotacaoEConfig(t *testing.T) {
	b := abrirTeste(t)
	if _, ok, err := b.Cotacao("BBAS3"); ok || err != nil {
		t.Fatalf("cotação inexistente: %v %v", ok, err)
	}
	c := Cotacao{Ticker: "BBAS3", Preco: 27.5, Horario: time.Date(2026, 10, 28, 17, 0, 0, 0, time.UTC), BuscadoEm: time.Now(), Fonte: "brapi"}
	for _, preco := range []float64{27.5, 28} { // a segunda gravação substitui a primeira
		c.Preco = preco
		if err := b.SalvarCotacao(c); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := b.Cotacao("BBAS3")
	if err != nil || !ok || got.Preco != 28 || !got.Horario.Equal(c.Horario) {
		t.Errorf("cotação lida: %+v %v %v", got, ok, err)
	}

	if v, _ := b.ConfigNumero("teto", 25); v != 25 {
		t.Errorf("padrão do teto %v, quer 25", v)
	}
	if err := b.SalvarConfigNumero("teto", 30); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.ConfigNumero("teto", 25); v != 30 {
		t.Errorf("teto salvo %v, quer 30", v)
	}
}
