package indicadores

import (
	"math"
	"testing"
	"time"

	"carteira/internal/fontes/cvm"
	"carteira/internal/risco"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func perto(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// fluxo cria uma conta de DRE/DVA (período), saldo cria uma de balanço (data).
func fluxo(chave string, cons bool, refer, ini, fim string, v float64) cvm.Conta {
	return cvm.Conta{Chave: chave, Consolidado: cons, Demonstracao: "DRE", DtRefer: d(refer), DtIni: d(ini), DtFim: d(fim), Valor: v}
}

func saldo(chave string, cons bool, refer, fim string, v float64) cvm.Conta {
	return cvm.Conta{Chave: chave, Consolidado: cons, Demonstracao: "BPP", DtRefer: d(refer), DtFim: d(fim), Valor: v}
}

// Empresa comum: 2 anos de DFP + ITR do 1º semestre deste ano e do anterior.
func empresaExemplo() []cvm.Conta {
	var c []cvm.Conta
	for _, ano := range []struct {
		y                   string
		lucro, ebit, da     float64
		pl, emp, caixa      float64
		ac, pc, minoritario float64
	}{
		{"2024", 100, 200, 50, 1000, 400, 100, 600, 300, 0},
		{"2025", 120, 240, 60, 1100, 500, 100, 700, 350, 100},
	} {
		ini, fim := ano.y+"-01-01", ano.y+"-12-31"
		c = append(c,
			fluxo("lucro", true, fim, ini, fim, ano.lucro+10), // consolidado inclui minoritários
			fluxo("lucro_controladora", true, fim, ini, fim, ano.lucro),
			fluxo("ebit", true, fim, ini, fim, ano.ebit),
			fluxo("da", true, fim, ini, fim, -ano.da),
			saldo("pl", true, fim, fim, ano.pl),
			saldo("pl_nao_controladores", true, fim, fim, ano.minoritario),
			saldo("emprestimos_cp", true, fim, fim, ano.emp/2),
			saldo("emprestimos_lp", true, fim, fim, ano.emp/2),
			saldo("caixa", true, fim, fim, ano.caixa),
			saldo("ativo_circulante", true, fim, fim, ano.ac),
			saldo("passivo_circulante", true, fim, fim, ano.pc),
		)
	}
	// Coluna do ano anterior zerada num documento mais novo: deve ser ignorada.
	c = append(c, saldo("pl", true, "2025-12-31", "2024-12-31", 0))
	// Saldo de 31/12/2023 (início de 2024), só no individual.
	c = append(c, saldo("pl", false, "2024-12-31", "2023-12-31", 900))
	// 1º semestre: 2025 = 50, 2026 = 80 → 12 meses = 120 + 80 − 50 = 150.
	c = append(c,
		fluxo("lucro_controladora", true, "2025-06-30", "2025-01-01", "2025-06-30", 50),
		fluxo("lucro_controladora", true, "2026-06-30", "2026-01-01", "2026-06-30", 80),
		fluxo("lucro", true, "2025-06-30", "2025-01-01", "2025-06-30", 55),
		fluxo("lucro", true, "2026-06-30", "2026-01-01", "2026-06-30", 85),
		saldo("pl", true, "2025-06-30", "2025-06-30", 1050),
		saldo("pl", true, "2026-06-30", "2026-06-30", 1150),
	)
	return c
}

func TestCalcular(t *testing.T) {
	tk := cvm.Ticker{Codigo: "TEST3", AcoesPorUnidade: 1}
	comps := []cvm.Composicao{{DtRefer: d("2026-06-30"), Ordinarias: 100, TesourariaOrdinaria: 10}}
	r := Calcular(tk, empresaExemplo(), comps, Cotacao{Preco: 20, Fonte: "teste"}, Historico{})

	if r.Financeira {
		t.Fatal("empresa com EBIT não é financeira")
	}
	if len(r.Colunas) != 4 || r.Colunas[0].Rotulo != "2024" || r.Colunas[2].Rotulo != "12m" {
		t.Fatalf("colunas: %+v", r.Colunas)
	}
	a2025, m12, med := r.Colunas[1].Valores, r.Colunas[2].Valores, r.Colunas[3].Valores

	casos := []struct {
		nome      string
		got, quer float64
	}{
		{"lucro 2025", a2025["lucro"].Numero, 120},
		// PL controladora 2025 = 1100 − 100; 2024 = 1000 − 0 (o zero do documento novo é ignorado)
		{"ROE 2025", a2025["roe"].Numero, 120 / ((1000.0 + 1000.0) / 2)},
		{"ROIC 2025", a2025["roic"].Numero, 240 * 0.66 / (1100 + 400)},
		{"DL/EBITDA 2025", a2025["dl_ebitda"].Numero, 400.0 / 300},
		{"DL/PL 2025", a2025["dl_pl"].Numero, 400.0 / 1100},
		{"liquidez 2025", a2025["liquidez"].Numero, 2},
		{"lucro 12m", m12["lucro"].Numero, 150},
		// 90 ações × R$ 20 = 1.800 ÷ 150
		{"P/L 12m", m12["pl"].Numero, 12},
		{"média lucro", med["lucro"].Numero, 110},
	}
	for _, c := range casos {
		if !perto(c.got, c.quer) {
			t.Errorf("%s = %v, quer %v", c.nome, c.got, c.quer)
		}
	}
	// ROE 2024 usa o PL individual de 31/12/2023 (não havia consolidado).
	if v := r.Colunas[0].Valores["roe"]; !perto(v.Numero, 100/((1000.0+900)/2)) {
		t.Errorf("ROE 2024 = %v", v.Numero)
	}
	if r.Colunas[0].Valores["pl"].OK {
		t.Error("P/L anual não devia ter valor sem preço histórico")
	}
}

func TestBancoSemEBIT(t *testing.T) {
	var contas []cvm.Conta
	for _, c := range empresaExemplo() {
		if c.Chave != "ebit" {
			contas = append(contas, c)
		}
	}
	r := Calcular(cvm.Ticker{Codigo: "BANC3", AcoesPorUnidade: 1}, contas, nil, Cotacao{}, Historico{})
	if !r.Financeira {
		t.Fatal("sem EBIT devia ser tratada como financeira")
	}
	if v := r.Colunas[1].Valores["roic"]; v.OK || v.Motivo != motivoFinanceira {
		t.Errorf("ROIC de banco: %+v", v)
	}
	if !r.Colunas[1].Valores["roe"].OK {
		t.Error("ROE de banco devia ser calculado")
	}
}

func TestUnitEAcoesEmMilhares(t *testing.T) {
	contas := empresaExemplo()
	// LPA 2025 = 1,3333 → lucro 120 ÷ LPA = 90 ações; composição informa 0,09 (em milhares).
	contas = append(contas, cvm.Conta{Chave: "lpa", Consolidado: true, DtRefer: d("2025-12-31"), DtIni: d("2025-01-01"), DtFim: d("2025-12-31"), Valor: 120.0 / 90000})
	comps := []cvm.Composicao{{DtRefer: d("2026-06-30"), Ordinarias: 90}}
	// Unit com 3 ações a R$ 60 → R$ 20 por ação; 90.000 ações → 1.800.000 ÷ 150
	r := Calcular(cvm.Ticker{Codigo: "TEST11", AcoesPorUnidade: 3}, contas, comps, Cotacao{Preco: 60}, Historico{})
	if v := r.Colunas[2].Valores["pl"]; !perto(v.Numero, 12000) {
		t.Errorf("P/L = %v, quer 12000 (ações ×1000)", v.Numero)
	}
}

func TestRotuloPeriodo(t *testing.T) {
	casos := map[[2]string]string{
		{"2025-01-01", "2025-12-31"}: "2025",
		{"2026-01-01", "2026-03-31"}: "1T26",
		{"2026-01-01", "2026-06-30"}: "1S26",
		{"2026-01-01", "2026-09-30"}: "9M26",
	}
	for p, quer := range casos {
		if got := rotuloPeriodo(d(p[0]), d(p[1])); got != quer {
			t.Errorf("rotuloPeriodo(%v) = %q, quer %q", p, got, quer)
		}
	}
}

// historicoExemplo: em 2025 a ação sobe e cai exatamente o dobro do BOVA11
// (beta 2); fechamentos de fim de ano R$ 10 (2024) e R$ 12 (2025).
func historicoExemplo() Historico {
	h := Historico{Fonte: "teste"}
	acao, indice := 10.0, 100.0
	h.Precos = append(h.Precos, risco.Ponto{Data: d("2024-12-30"), Valor: 10})
	h.Ajustado = append(h.Ajustado, risco.Ponto{Data: d("2024-12-30"), Valor: acao})
	h.Indice = append(h.Indice, risco.Ponto{Data: d("2024-12-30"), Valor: indice})
	dia := d("2025-01-02")
	for i := 0; dia.Year() == 2025; i++ {
		if dia.Weekday() != time.Saturday && dia.Weekday() != time.Sunday {
			r := 0.01 * math.Sin(float64(i))
			acao *= 1 + 2*r
			indice *= 1 + r
			h.Ajustado = append(h.Ajustado, risco.Ponto{Data: dia, Valor: acao})
			h.Indice = append(h.Indice, risco.Ponto{Data: dia, Valor: indice})
			h.Ate = dia
		}
		dia = dia.AddDate(0, 0, 1)
	}
	h.Precos = append(h.Precos, risco.Ponto{Data: d("2025-12-30"), Valor: 12})
	return h
}

func TestPLAnualEBeta(t *testing.T) {
	comps := []cvm.Composicao{
		{DtRefer: d("2024-12-31"), Ordinarias: 90},
		{DtRefer: d("2025-12-31"), Ordinarias: 100},
	}
	r := Calcular(cvm.Ticker{Codigo: "TEST3", AcoesPorUnidade: 1}, empresaExemplo(), comps, Cotacao{Preco: 20}, historicoExemplo())
	a2024, a2025 := r.Colunas[0].Valores, r.Colunas[1].Valores
	// 2024: 90 ações × R$ 10 ÷ lucro 100; 2025: 100 × R$ 12 ÷ 120.
	if !perto(a2024["pl"].Numero, 9) || !perto(a2025["pl"].Numero, 10) {
		t.Errorf("P/L anual: 2024 %+v · 2025 %+v", a2024["pl"], a2025["pl"])
	}
	if b := a2025["beta"]; !b.OK || !perto(b.Numero, 2) {
		t.Errorf("beta 2025: %+v", b)
	}
	if v := a2025["volatilidade"]; !v.OK || v.Numero <= 0 {
		t.Errorf("volatilidade 2025: %+v", v)
	}
	if a2024["beta"].OK {
		t.Error("2024 tem um pregão só: beta não devia sair")
	}
	if c := r.Colunas[2].Valores["correlacao"]; c.OK {
		t.Errorf("sem carteira não há correlação: %+v", c)
	}
}

func TestPrejuizoTrimestral(t *testing.T) {
	// 1S25 = 50 e 2025 = 120 no exemplo; com 1T25 = 60, o 2º trimestre de 2025 teve prejuízo de 10.
	contas := append(empresaExemplo(), fluxo("lucro_controladora", true, "2025-03-31", "2025-01-01", "2025-03-31", 60))
	r := Calcular(cvm.Ticker{Codigo: "TEST3", AcoesPorUnidade: 1}, contas, nil, Cotacao{}, Historico{})
	if len(r.PrejuizoTrimestral) != 1 || r.PrejuizoTrimestral[0] != "2T25" {
		t.Errorf("trimestres com prejuízo: %v", r.PrejuizoTrimestral)
	}
}
