package importacao

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"carteira/internal/carteira"
)

var cabecalho = []string{"Entrada/Saída", "Data", "Movimentação", "Produto", "Instituição", "Quantidade", "Preço unitário", "Valor da Operação"}

func extratoExemplo() [][]string {
	return [][]string{
		cabecalho,
		{"Credito", "10/05/2024", "Transferência - Liquidação", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "100", "27.5", "2750"},
		{"Debito", "12/06/2024", "Transferência - Liquidação", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "30", "30", "900"},
		{"Credito", "15/04/2024", "Desdobro", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "100", "-", "-"},
		{"Credito", "20/06/2024", "Dividendo", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "170", "0,5", "85"},
		{"Credito", "21/06/2024", "Juros Sobre Capital Próprio", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "170", "0,1", "14,45"},
		{"Debito", "01/07/2024", "Transferência", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "170", "-", "-"},
		{"Credito", "01/07/2024", "Compra", "Tesouro IPCA+ 2035", "RICO INVESTIMENTOS", "0,5", "2000", "1000"},
		{"Credito", "02/07/2024", "Grupamento", "MGLU3 - MAGAZINE LUIZA S/A", "RICO INVESTIMENTOS", "10", "-", "-"},
		{"Credito", "03/07/2024", "Bonificação em Ativos", "ITSA4 - ITAUSA S/A", "RICO INVESTIMENTOS", "5", "1,23", "-"},
		{},
		// Igual à primeira linha: duas compras idênticas no mesmo dia são duas operações.
		{"Credito", "10/05/2024", "Transferência - Liquidação", "BBAS3 - BANCO DO BRASIL S/A", "RICO INVESTIMENTOS", "100", "27.5", "2750"},
	}
}

func TestLerLinhas(t *testing.T) {
	r, err := lerLinhas(extratoExemplo())
	if err != nil {
		t.Fatal(err)
	}
	if r.Linhas != 10 {
		t.Errorf("linhas lidas %d, quer 10", r.Linhas)
	}

	quer := []struct {
		tipo   carteira.Tipo
		ticker string
		qtd    float64
		preco  float64
	}{
		{carteira.Compra, "BBAS3", 100, 27.5},
		{carteira.Venda, "BBAS3", 30, 30},
		{carteira.Ajuste, "BBAS3", 100, 0},
		{carteira.Compra, "Tesouro IPCA+ 2035", 0.5, 2000},
		{carteira.Bonificacao, "ITSA4", 5, 1.23},
		{carteira.Compra, "BBAS3", 100, 27.5},
	}
	if len(r.Operacoes) != len(quer) {
		t.Fatalf("%d operações, quer %d: %+v", len(r.Operacoes), len(quer), r.Operacoes)
	}
	for i, q := range quer {
		op := r.Operacoes[i]
		if op.Tipo != q.tipo || op.Ticker != q.ticker || op.Quantidade != q.qtd || op.Preco != q.preco {
			t.Errorf("operação %d = %+v; quer %+v", i, op, q)
		}
	}
	if r.Operacoes[0].Chave == r.Operacoes[5].Chave {
		t.Error("linhas idênticas no mesmo arquivo receberam a mesma chave")
	}

	if len(r.Proventos) != 2 || r.Proventos[0].Valor != 85 || r.Proventos[1].Valor != 14.45 {
		t.Errorf("proventos: %+v", r.Proventos)
	}
	if r.Ignorados["Transferência"] != 1 || r.Ignorados["Grupamento"] != 1 {
		t.Errorf("ignorados: %v", r.Ignorados)
	}
	if len(r.Avisos) != 1 {
		t.Errorf("quer 1 aviso (grupamento), veio %v", r.Avisos)
	}
}

func TestChaveIgualEntreImportacoes(t *testing.T) {
	a, _ := lerLinhas(extratoExemplo())
	b, _ := lerLinhas(extratoExemplo())
	for i := range a.Operacoes {
		if a.Operacoes[i].Chave != b.Operacoes[i].Chave {
			t.Fatalf("chave da operação %d mudou entre leituras", i)
		}
	}
}

func TestExtratoDeNegociacaoRecusado(t *testing.T) {
	linhas := [][]string{{"Data do Negócio", "Tipo de Movimentação", "Mercado", "Prazo/Vencimento", "Instituição", "Código de Negociação", "Quantidade", "Preço", "Valor"}}
	if _, err := lerLinhas(linhas); err == nil {
		t.Fatal("devia recusar o extrato de Negociação")
	}
}

// Testa o caminho do arquivo .xlsx de verdade, com números e datas guardados
// como números no Excel (e não como texto).
func TestLerMovimentacaoXLSX(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	cab := make([]any, len(cabecalho))
	for i, c := range cabecalho {
		cab[i] = c
	}
	linha := []any{"Credito", time.Date(2024, 5, 10, 0, 0, 0, 0, time.UTC), "Transferência - Liquidação",
		"TAEE11 - TRANSMISSORA ALIANÇA DE ENERGIA ELÉTRICA S/A", "RICO", 50, 34.56, 1728.0}
	if err := f.SetSheetRow("Sheet1", "A1", &cab); err != nil {
		t.Fatal(err)
	}
	if err := f.SetSheetRow("Sheet1", "A2", &linha); err != nil {
		t.Fatal(err)
	}
	caminho := filepath.Join(t.TempDir(), "movimentacao.xlsx")
	if err := f.SaveAs(caminho); err != nil {
		t.Fatal(err)
	}

	r, err := LerMovimentacao(caminho)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Operacoes) != 1 {
		t.Fatalf("operações: %+v, avisos: %v", r.Operacoes, r.Avisos)
	}
	op := r.Operacoes[0]
	if op.Ticker != "TAEE11" || op.Quantidade != 50 || op.Preco != 34.56 || op.Data.Format("02/01/2006") != "10/05/2024" {
		t.Errorf("operação lida errado: %+v", op)
	}
}

func TestAtualizacaoELiquidacaoSemPreco(t *testing.T) {
	linhas := [][]string{
		cabecalho,
		{"Credito", "02/09/2026", "Atualização", "BBAS3 - BANCO DO BRASIL S/A", "XP", "8", "-", "-"},
		{"Debito", "27/10/2026", "Transferência - Liquidação", "BBAS3 - BANCO DO BRASIL S/A", "XP", "26", "-", "-"},
		{"Credito", "19/11/2024", "Atualização", "ISAE4 - CTEEP - CIA TRANSMISSÃO ENERGIA ELÉTRICA PAULISTA", "XP", "5", "-", "-"},
		{"Credito", "06/01/2023", "Transferência - Liquidação", "TRPL4 - CTEEP - CIA TRANSMISSÃO ENERGIA ELÉTRICA PAULISTA", "XP", "5", "22,2", "111"},
	}
	r, err := lerLinhas(linhas)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Operacoes) != 2 {
		t.Fatalf("quer 2 operações (troca e compra), veio %+v", r.Operacoes)
	}
	troca := r.Operacoes[0]
	if troca.Tipo != carteira.Troca || troca.Ticker != "ISAE4" || troca.TickerAnterior != "TRPL4" || troca.Quantidade != 5 {
		t.Errorf("troca lida errado: %+v", troca)
	}
	if r.Ignorados["Atualização"] != 1 || r.Ignorados["Transferência - Liquidação sem preço"] != 1 {
		t.Errorf("ignorados: %v", r.Ignorados)
	}
	if len(r.Avisos) != 2 {
		t.Errorf("quer 2 avisos (BBAS3), veio %v", r.Avisos)
	}
}
