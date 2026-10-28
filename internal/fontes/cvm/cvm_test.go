package cvm

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestChave(t *testing.T) {
	casos := []struct {
		dem, cod, desc, quer string
	}{
		{"DRE", "3.05", "Resultado Antes do Resultado Financeiro e dos Tributos", "ebit"},
		{"DRE", "3.05", "Resultado antes dos Tributos sobre o Lucro", ""}, // banco: não é EBIT
		{"DRE", "3.11.01", "Atribuído a Sócios da Empresa Controladora", "lucro_controladora"},
		{"DRE", "3.99.01.01", "ON", "lpa"},
		{"BPP", "2.03", "Patrimônio Líquido Consolidado", "pl"},
		{"BPP", "2.07", "Patrimônio Líquido Consolidado", "pl"}, // banco
		{"BPP", "2.07.02", "Patrimônio Líquido Atribuído aos Não Controladores", "pl_nao_controladores"},
		{"BPP", "2.01", "Passivos Financeiros Avaliados ao Valor Justo através do Resultado", ""}, // banco
		{"DVA", "7.04.01", "Depreciação, Amortização e Exaustão", "da"},
		{"BPA", "1.01.02", "Aplicações Financeiras", "aplicacoes"},
	}
	for _, c := range casos {
		if got := Chave(c.dem, c.cod, c.desc); got != c.quer {
			t.Errorf("Chave(%s, %s, %q) = %q, quer %q", c.dem, c.cod, c.desc, got, c.quer)
		}
	}
}

// criarZip grava arquivos CSV em Latin-1, como a CVM publica.
func criarZip(t *testing.T, arquivos map[string]string) string {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "teste.zip")
	f, err := os.Create(caminho)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for nome, conteudo := range arquivos {
		w, err := z.Create(nome)
		if err != nil {
			t.Fatal(err)
		}
		latin, err := charmap.ISO8859_1.NewEncoder().String(conteudo)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(latin))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return caminho
}

func TestLerBalancos(t *testing.T) {
	cab := "CNPJ_CIA;DT_REFER;VERSAO;DENOM_CIA;CD_CVM;GRUPO_DFP;MOEDA;ESCALA_MOEDA;ORDEM_EXERC;DT_INI_EXERC;DT_FIM_EXERC;CD_CONTA;DS_CONTA;VL_CONTA;ST_CONTA_FIXA\n"
	cabBP := "CNPJ_CIA;DT_REFER;VERSAO;DENOM_CIA;CD_CVM;GRUPO_DFP;MOEDA;ESCALA_MOEDA;ORDEM_EXERC;DT_FIM_EXERC;CD_CONTA;DS_CONTA;VL_CONTA;ST_CONTA_FIXA\n"
	caminho := criarZip(t, map[string]string{
		"dfp_cia_aberta_DRE_con_2025.csv": cab +
			"10.807.374/0001-77;2025-12-31;1;BOA SAFRA;1;DF;REAL;MIL;ÚLTIMO;2025-01-01;2025-12-31;3.11;Lucro/Prejuízo Consolidado do Período;101130.0000000000;S\n" +
			"10.807.374/0001-77;2025-12-31;1;BOA SAFRA;1;DF;REAL;MIL;PENÚLTIMO;2024-01-01;2024-12-31;3.11;Lucro/Prejuízo Consolidado do Período;93460.0000000000;S\n" +
			"10.807.374/0001-77;2025-12-31;1;BOA SAFRA;1;DF;REAL;MIL;ÚLTIMO;2025-01-01;2025-12-31;3.99.01.01;ON;0.15;N\n" +
			"10.807.374/0001-77;2025-12-31;1;BOA SAFRA;1;DF;REAL;MIL;ÚLTIMO;2025-01-01;2025-12-31;3.02;Custo;-1;S\n",
		"dfp_cia_aberta_BPP_ind_2025.csv": cabBP +
			"10.807.374/0001-77;2025-12-31;1;BOA SAFRA;1;DF;REAL;MIL;PENÚLTIMO;2024-12-31;2.03;Patrimônio Líquido;1346795.0000000000;S\n",
		"dfp_cia_aberta_composicao_capital_2025.csv": "CNPJ_CIA;DT_REFER;VERSAO;DENOM_CIA;QT_ACAO_ORDIN_CAP_INTEGR;QT_ACAO_PREF_CAP_INTEGR;QT_ACAO_TOTAL_CAP_INTEGR;QT_ACAO_ORDIN_TESOURO;QT_ACAO_PREF_TESOURO;QT_ACAO_TOTAL_TESOURO\n" +
			"10.807.374/0001-77;2025-12-31;2;BOA SAFRA;141902688;0;141902688;840790;0;840790\n",
	})

	contas, comps, err := LerBalancos(caminho)
	if err != nil {
		t.Fatal(err)
	}
	// DRE: só ÚLTIMO, sem a conta 3.02 (não usada). Balanço: PENÚLTIMO entra.
	if len(contas) != 3 {
		t.Fatalf("quer 3 contas, veio %d: %+v", len(contas), contas)
	}
	porChave := map[string]Conta{}
	for _, c := range contas {
		porChave[c.Chave] = c
	}
	if c := porChave["lucro"]; c.Valor != 101130000 || !c.Consolidado || c.DtIni.Format("2006-01-02") != "2025-01-01" {
		t.Errorf("lucro: %+v", c)
	}
	if c := porChave["lpa"]; c.Valor != 0.15 {
		t.Errorf("LPA não pode ser multiplicado pela escala: %+v", c)
	}
	if c := porChave["pl"]; c.Consolidado || c.Valor != 1346795000 || !c.DtIni.IsZero() {
		t.Errorf("PL: %+v", c)
	}
	if len(comps) != 1 || comps[0].Total() != 141902688-840790 {
		t.Errorf("composição: %+v", comps)
	}
}

func TestLerTickers(t *testing.T) {
	cab := "CNPJ_Companhia;Data_Referencia;Versao;ID_Documento;Nome_Empresarial;Valor_Mobiliario;Sigla_Classe_Acao_Preferencial;Classe_Acao_Preferencial;Codigo_Negociacao;Composicao_BDR_Unit;Mercado;Sigla_Entidade_Administradora;Entidade_Administradora;Data_Inicio_Negociacao;Data_Fim_Negociacao;Segmento;Data_Inicio_Listagem;Data_Fim_Listagem\n"
	caminho := criarZip(t, map[string]string{
		"fca_cia_aberta_valor_mobiliario_2026.csv": cab +
			"07.859.971/0001-30;2026-01-01;1;1;TAESA;Units;;;TAEE11;1 ON / 2 PN;Bolsa;;;;;;;\n" +
			"76.484.013/0001-45;2026-01-01;2;1;SANEPAR;Units;;;SAPR11;1 ON e 4 PN;Bolsa;;;;;;;\n" +
			"10.807.374/0001-77;2026-01-01;1;1;BOA SAFRA;Ações Ordinárias;;;SOJA3;;Bolsa;;;;;;;\n" +
			"10.807.374/0001-77;2026-01-01;1;1;BOA SAFRA;Debêntures;;;SOJA11;;Balcão;;;;;;;\n" +
			"11.111.111/0001-11;2026-01-01;1;1;VELHA;Ações Ordinárias;;;VELH3;;Bolsa;;;;2020-01-01;;;\n",
	})
	ts, err := LerTickers(caminho)
	if err != nil {
		t.Fatal(err)
	}
	quer := map[string]float64{"TAEE11": 3, "SAPR11": 5, "SOJA3": 1}
	if len(ts) != len(quer) {
		t.Fatalf("tickers: %+v", ts)
	}
	for _, tk := range ts {
		if quer[tk.Codigo] != tk.AcoesPorUnidade {
			t.Errorf("%s: %v ações por unidade, quer %v", tk.Codigo, tk.AcoesPorUnidade, quer[tk.Codigo])
		}
	}
}
