// Package indicadores calcula lucro, P/L, EV/EBITDA, ROE, ROIC, endividamento e liquidez
// a partir dos balanços da CVM, guardando a memória de cálculo de cada número
// (fórmula, valores usados e de onde vieram) para o botão ⓘ.
package indicadores

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"

	"carteira/internal/fontes/cvm"
	"carteira/internal/formato"
)

// Aliquota de IR + CSLL usada no ROIC.
const Aliquota = 0.34

// Def descreve um indicador.
type Def struct {
	Chave   string
	Nome    string
	Unidade string // "R$", "%" ou "x"
	Formula string
	ComoLer string
}

// Lista é a ordem em que os indicadores aparecem.
var Lista = []Def{
	{"lucro", "Lucro líquido", "R$", "lucro atribuído aos acionistas da empresa (sem a parte de sócios de controladas)",
		"crescendo é bom; prejuízo no ano reprova no filtro"},
	{"pl", "P/L", "x", "(ações × cotação) ÷ lucro líquido; no ano, cotação de fechamento do último pregão do ano; em 12m, cotação atual",
		"abaixo de 10: barato · 10 a 20: médio · acima de 20: caro (compare com o setor) · negativo: prejuízo"},
	{"ev_ebitda", "EV/EBITDA", "x", "(valor de mercado + dívida líquida + sócios de controladas) ÷ (EBIT + depreciação e amortização); valor de mercado como no P/L",
		"abaixo de 6: barato · 6 a 10: médio · acima de 10: caro (compare com o setor) · não serve para bancos"},
	{"pvp", "P/VP", "x", "valor de mercado ÷ patrimônio líquido dos acionistas da empresa (fim do período); valor de mercado como no P/L",
		"abaixo de 1: paga menos que o patrimônio · 1 a 3: comum · acima de 3: caro (ou empresa de ROE muito alto)"},
	{"roe", "ROE", "%", "lucro líquido ÷ patrimônio líquido médio (início e fim do período)",
		"acima de 15%: bom · 10% a 15%: ok · abaixo de 10%: fraco"},
	{"roic", "ROIC", "%", "EBIT × (1 − 34%) ÷ (patrimônio líquido + dívida líquida)",
		"acima de 12%: bom · 8% a 12%: ok · abaixo de 8%: fraco"},
	{"dl_ebitda", "Dív. líq./EBITDA", "x", "(empréstimos − caixa − aplicações) ÷ (EBIT + depreciação e amortização)",
		"negativo: mais caixa que dívida · até 1: baixa · 1 a 3: moderada · acima de 3: alta"},
	{"dl_pl", "Dív. líq./PL", "x", "(empréstimos − caixa − aplicações) ÷ patrimônio líquido",
		"abaixo de 0,5: baixa · 0,5 a 1: moderada · acima de 1: alta"},
	{"liquidez", "Liquidez corrente", "x", "ativo circulante ÷ passivo circulante",
		"acima de 1,5: confortável · 1 a 1,5: ok · abaixo de 1: aperto"},
	{"beta", "Beta", "x", "covariância(retorno da ação, retorno do BOVA11) ÷ variância(retorno do BOVA11); no ano, retornos diários; em 12m, retornos semanais dos últimos 2 anos",
		"abaixo de 0,8: defensiva · 0,8 a 1,2: oscila como a bolsa · acima de 1,2: oscila mais que a bolsa"},
	{"volatilidade", "Volatilidade", "%", "desvio-padrão dos retornos diários × √252; em 12m, os últimos 252 pregões",
		"abaixo de 25%: baixa · 25% a 40%: média · acima de 40%: alta"},
	{"correlacao", "Correl. c/ carteira", "x", "correlação dos retornos semanais (2 anos) com a carteira de hoje, sem o próprio ativo",
		"acima de 0,6: repete o risco · 0,2 a 0,6: diversifica um pouco · abaixo de 0,2: diversifica bem · negativa: protege"},
}

// Buscar devolve a definição de um indicador pela chave.
func Buscar(chave string) (Def, bool) {
	for _, d := range Lista {
		if d.Chave == chave {
			return d, true
		}
	}
	return Def{}, false
}

// Parte é um número usado no cálculo, com a origem.
type Parte struct {
	Nome    string
	Valor   float64
	Moeda   bool   // true = valor em R$
	Unidade string // "%" ou "x" quando a parte é outro indicador (na média)
	Origem  string // ex.: "DFP 2025 · DRE individual · 3.11 Lucro/Prejuízo do Período"
}

// Valor é o resultado de um indicador num período, com a memória de cálculo.
type Valor struct {
	Numero float64
	OK     bool
	Motivo string // por que não há valor
	Partes []Parte
	Nota   string // observação sobre o cálculo
	// Periodo, quando preenchido, substitui o período da coluna na memória de
	// cálculo (ex.: o beta de 12m usa 2 anos de preços, não a janela do balanço).
	Periodo string
}

// Coluna é um período: um ano, os últimos 12 meses ou a média de 5 anos.
type Coluna struct {
	Rotulo  string
	Periodo string // ex.: "01/01/2025 a 31/12/2025"
	Valores map[string]Valor
}

// Resultado reúne os indicadores de uma ação.
type Resultado struct {
	Ticker, Nome, CNPJ string
	Financeira         bool      // banco ou seguradora: sem EBIT nem passivo circulante
	UltimoBalanco      time.Time // fim do período mais recente com dados
	ValorMercado       float64   // com a cotação usada no P/L de 12m; zero se não deu para calcular
	Colunas            []Coluna
	Avisos             []string
	PrejuizoTrimestral []string // trimestres com prejuízo nos 2 últimos anos e neste ano (ex.: "2T25")
}

// Cotacao é o preço usado no P/L. Sem Horario, a Fonte já diz a data.
type Cotacao struct {
	Preco   float64
	Horario time.Time
	Fonte   string
}

func (r *Resultado) avisar(s string) {
	if !slices.Contains(r.Avisos, s) {
		r.Avisos = append(r.Avisos, s)
	}
}

const (
	motivoFinanceira = "não se aplica a bancos e seguradoras"
	motivoHistorico  = "sem histórico de preços"
	motivoSemDado    = "dado não encontrado na CVM"
)

// Calcular monta os indicadores dos 5 últimos anos, dos últimos 12 meses e a média.
// h traz o histórico de preços (P/L de cada ano, beta, volatilidade e correlação).
func Calcular(t cvm.Ticker, contas []cvm.Conta, comps []cvm.Composicao, cot Cotacao, h Historico) Resultado {
	c := calc{contas: contas}
	r := Resultado{Ticker: t.Codigo, Nome: t.Nome, CNPJ: t.CNPJ, Financeira: !c.temChave("ebit")}

	anos := c.periodosAnuais()
	if len(anos) > 5 {
		anos = anos[len(anos)-5:]
	}
	if len(anos) == 0 {
		r.Avisos = append(r.Avisos, "sem balanço anual (DFP) na CVM para esta empresa")
		return r
	}

	var colunasAnuais []Coluna
	for _, a := range anos {
		j := janela{termos: []termo{{a.ini, a.fim, 1}}}
		col := c.coluna(fmt.Sprint(a.fim.Year()), j, a.fim, a.fim.AddDate(-1, 0, 0), r.Financeira)
		m := c.mercadoAno(t, comps, h, a, &r)
		col.Valores["pl"] = pl(m, col.Valores["lucro"])
		col.Valores["ev_ebitda"] = c.evEbitda(m, j, a.fim, r.Financeira)
		col.Valores["pvp"] = c.pvp(m, a.fim)
		riscoAno(col, h, a.fim.Year())
		colunasAnuais = append(colunasAnuais, col)
	}
	r.Colunas = append(r.Colunas, colunasAnuais...)

	j12, fim12 := c.janela12m(anos[len(anos)-1])
	r.UltimoBalanco = fim12
	col12 := c.coluna("12m", j12, fim12, fim12.AddDate(-1, 0, 0), r.Financeira)
	m12 := c.valorMercado(t, comps, cot, time.Time{}, anos[len(anos)-1], &r)
	col12.Valores["pl"] = pl(m12, col12.Valores["lucro"])
	col12.Valores["ev_ebitda"] = c.evEbitda(m12, j12, fim12, r.Financeira)
	col12.Valores["pvp"] = c.pvp(m12, fim12)
	if m12.motivo == "" {
		r.ValorMercado = m12.valor
	}
	risco12m(col12, h)
	r.Colunas = append(r.Colunas, col12)
	r.Colunas = append(r.Colunas, media(colunasAnuais))
	r.PrejuizoTrimestral = c.trimestresNegativos(anos)

	// Lucro consolidado muito diferente do da controladora: parte do lucro é de
	// sócios de controladas (ex.: fundo consolidado). Avisa, porque sites que
	// usam o consolidado mostram outro P/L.
	if cons, _, ok := j12.soma("Lucro consolidado", c.lucroConsolidado); ok {
		if l := col12.Valores["lucro"]; l.OK && math.Abs(cons-l.Numero) > 0.2*math.Abs(cons) {
			r.avisar(fmt.Sprintf("lucro consolidado dos 12 meses (R$ %s mi) é bem diferente do lucro da controladora (R$ %s mi): a diferença é de sócios de empresas controladas. P/L e ROE usam o da controladora.",
				formato.Numero(cons/1e6, 1), formato.Numero(l.Numero/1e6, 1)))
		}
	}
	return r
}

// ---------- acesso às contas ----------

type calc struct{ contas []cvm.Conta }

func (c calc) temChave(chave string) bool {
	return slices.ContainsFunc(c.contas, func(x cvm.Conta) bool { return x.Chave == chave })
}

// achar devolve a conta mais recente (documento mais novo, maior versão) para
// a chave e o período. Para saldo, ini é zero.
func (c calc) achar(chave string, consolidado bool, ini, fim time.Time) (cvm.Conta, bool) {
	var achou cvm.Conta
	ok := false
	for _, x := range c.contas {
		if x.Chave != chave || x.Consolidado != consolidado || !x.DtIni.Equal(ini) || !x.DtFim.Equal(fim) {
			continue
		}
		// Prefere valor diferente de zero: a coluna do ano anterior em alguns
		// documentos vem zerada (ex.: mudança de norma contábil). Entre os
		// não zerados, o documento mais novo (pode ter reapresentação).
		melhor := !ok ||
			(achou.Valor == 0 && x.Valor != 0) ||
			((x.Valor != 0) == (achou.Valor != 0) &&
				(x.DtRefer.After(achou.DtRefer) || (x.DtRefer.Equal(achou.DtRefer) && x.Versao > achou.Versao)))
		if melhor {
			achou, ok = x, true
		}
	}
	return achou, ok
}

func origem(x cvm.Conta) string {
	doc := fmt.Sprintf("ITR %s", x.DtRefer.Format("01/2006"))
	if x.DtRefer.Month() == 12 && x.DtRefer.Day() == 31 {
		doc = fmt.Sprintf("DFP %d", x.DtRefer.Year())
	}
	tipo := "consolidada"
	if !x.Consolidado {
		tipo = "individual"
	}
	nomes := map[string]string{"DRE": "DRE", "BPA": "Balanço (ativo)", "BPP": "Balanço (passivo)", "DVA": "DVA"}
	return fmt.Sprintf("%s · %s %s · %s %s", doc, nomes[x.Demonstracao], tipo, x.Codigo, x.Descricao)
}

// primeiro tenta as fontes em ordem (ex.: individual, depois consolidada).
func (c calc) primeiro(nome string, ini, fim time.Time, fontes ...fonte) (float64, Parte, bool) {
	for _, f := range fontes {
		if x, ok := c.achar(f.chave, f.consolidado, ini, fim); ok {
			return x.Valor, Parte{Nome: nome, Valor: x.Valor, Moeda: true, Origem: origem(x)}, true
		}
	}
	return 0, Parte{}, false
}

type fonte struct {
	chave       string
	consolidado bool
}

// Lucro dos acionistas da empresa: linha "atribuído a sócios da controladora"
// da DRE consolidada. Sem ela, usa a DRE individual (empresa sem controladas).
// A individual não vem primeiro porque em bancos segue outra norma contábil
// e não bate com o consolidado.
func (c calc) lucro(ini, fim time.Time) (float64, Parte, bool) {
	return c.primeiro("Lucro", ini, fim, fonte{"lucro_controladora", true}, fonte{"lucro", false}, fonte{"lucro", true})
}

// plControladora é o PL dos acionistas da empresa: PL consolidado menos a
// parte de sócios de controladas. Sem consolidado, usa o PL individual.
func (c calc) plControladora(d time.Time) (float64, []Parte, bool) {
	total, pTotal, ok := c.saldo("PL consolidado", d, fonte{"pl", true})
	if !ok || total == 0 {
		v, p, ok := c.saldo("PL da controladora", d, fonte{"pl", false})
		return v, []Parte{p}, ok
	}
	minor, pMinor, okMinor := c.saldo("PL de sócios de controladas", d, fonte{"pl_nao_controladores", true})
	if !okMinor || minor == 0 {
		return total, []Parte{pTotal}, true
	}
	pMinor.Nome = "(−) " + pMinor.Nome
	return total - minor, []Parte{pTotal, pMinor}, true
}

func (c calc) lucroConsolidado(ini, fim time.Time) (float64, Parte, bool) {
	return c.primeiro("Lucro consolidado", ini, fim, fonte{"lucro", true}, fonte{"lucro", false})
}

func (c calc) ebit(ini, fim time.Time) (float64, Parte, bool) {
	return c.primeiro("EBIT", ini, fim, fonte{"ebit", true}, fonte{"ebit", false})
}

func (c calc) da(ini, fim time.Time) (float64, Parte, bool) {
	v, p, ok := c.primeiro("Depreciação e amortização", ini, fim, fonte{"da", true}, fonte{"da", false})
	return math.Abs(v), p, ok // a DVA traz como valor negativo (retenção)
}

func (c calc) saldo(nome string, d time.Time, fontes ...fonte) (float64, Parte, bool) {
	// Empresa que ainda não consolidava (ex.: antes de ter controladas) aparece
	// com consolidado zerado na coluna do ano anterior; aí vale o individual.
	if !c.consolidadoEm(d) {
		fontes = slices.DeleteFunc(slices.Clone(fontes), func(f fonte) bool { return f.consolidado })
	}
	v, p, ok := c.primeiro(nome, time.Time{}, d, fontes...)
	p.Nome = nome + " em " + formato.Data(d)
	return v, p, ok
}

// ---------- períodos ----------

type periodo struct{ ini, fim time.Time }

// periodosAnuais são os exercícios das DFPs, do mais antigo ao mais novo.
func (c calc) periodosAnuais() []periodo {
	vistos := map[periodo]bool{}
	for _, x := range c.contas {
		if x.Chave == "lucro" && x.DtRefer.Equal(x.DtFim) && x.DtFim.Sub(x.DtIni) > 300*24*time.Hour {
			vistos[periodo{x.DtIni, x.DtFim}] = true
		}
	}
	var l []periodo
	for p := range vistos {
		l = append(l, p)
	}
	slices.SortFunc(l, func(a, b periodo) int { return a.fim.Compare(b.fim) })
	return l
}

type termo struct {
	ini, fim time.Time
	sinal    float64
}

// janela é um período formado por somas e subtrações de períodos publicados.
type janela struct{ termos []termo }

func (j janela) soma(nome string, g func(ini, fim time.Time) (float64, Parte, bool)) (float64, []Parte, bool) {
	var total float64
	var partes []Parte
	for _, t := range j.termos {
		v, p, ok := g(t.ini, t.fim)
		if !ok {
			return 0, nil, false
		}
		total += t.sinal * v
		p.Nome = nome + " " + rotuloPeriodo(t.ini, t.fim)
		if t.sinal < 0 {
			p.Nome = "(−) " + p.Nome
		}
		partes = append(partes, p)
	}
	return total, partes, true
}

// rotuloPeriodo: "2025" para o ano, "1T26", "1S26" ou "9M26" para o acumulado do ano.
func rotuloPeriodo(ini, fim time.Time) string {
	meses := (fim.Year()-ini.Year())*12 + int(fim.Month()-ini.Month()) + 1
	switch {
	case meses >= 12:
		return fmt.Sprint(fim.Year())
	case meses == 3:
		return fmt.Sprintf("1T%02d", fim.Year()%100)
	case meses == 6:
		return fmt.Sprintf("1S%02d", fim.Year()%100)
	default:
		return fmt.Sprintf("%dM%02d", meses, fim.Year()%100)
	}
}

// janela12m: com ITR mais novo que a última DFP, 12 meses =
// último ano + acumulado deste ano − mesmo acumulado do ano passado.
// Senão, é o próprio último ano.
func (c calc) janela12m(ultimoAno periodo) (janela, time.Time) {
	inicioAno := ultimoAno.fim.AddDate(0, 0, 1)
	var fim time.Time
	for _, x := range c.contas {
		if x.Chave == "lucro" && x.DtIni.Equal(inicioAno) && x.DtFim.After(fim) {
			fim = x.DtFim
		}
	}
	fimAnterior := fim.AddDate(-1, 0, 0)
	_, _, temAnterior := c.lucro(ultimoAno.ini, fimAnterior)
	if fim.IsZero() || !temAnterior {
		return janela{termos: []termo{{ultimoAno.ini, ultimoAno.fim, 1}}}, ultimoAno.fim
	}
	return janela{termos: []termo{
		{ultimoAno.ini, ultimoAno.fim, 1},
		{inicioAno, fim, 1},
		{ultimoAno.ini, fimAnterior, -1},
	}}, fim
}

// ---------- cálculo de uma coluna ----------

func (c calc) coluna(rotulo string, j janela, fim, fimAnterior time.Time, financeira bool) Coluna {
	col := Coluna{Rotulo: rotulo, Valores: map[string]Valor{}}
	ini := j.termos[0].ini
	if len(j.termos) > 1 {
		ini = fimAnterior.AddDate(0, 0, 1)
	}
	col.Periodo = formato.Data(ini) + " a " + formato.Data(fim)

	lucro, partesLucro, okLucro := j.soma("Lucro", c.lucro)
	if okLucro {
		col.Valores["lucro"] = Valor{Numero: lucro, OK: true, Partes: partesLucro}
	} else {
		col.Valores["lucro"] = Valor{Motivo: motivoSemDado}
	}

	// ROE: lucro ÷ PL médio da controladora (PL individual = PL dos acionistas da empresa).
	plFim, pPlFim, ok1 := c.plControladora(fim)
	plIni, pPlIni, ok2 := c.plControladora(fimAnterior)
	switch {
	case !okLucro || !ok1:
		col.Valores["roe"] = Valor{Motivo: motivoSemDado}
	case ok2:
		col.Valores["roe"] = dividir(lucro, (plFim+plIni)/2, concat(partesLucro, pPlFim, pPlIni), "PL médio = (PL do fim + PL do início) ÷ 2")
	default:
		col.Valores["roe"] = dividir(lucro, plFim, concat(partesLucro, pPlFim), "sem o PL do início do período; usado só o do fim")
	}

	if financeira {
		for _, k := range []string{"roic", "dl_ebitda", "dl_pl", "liquidez"} {
			col.Valores[k] = Valor{Motivo: motivoFinanceira}
		}
		return col
	}

	// Dívida líquida no fim do período.
	dl, partesDL, okDL := c.dividaLiquida(fim)
	plTotal, pPlTotal, okPL := c.saldo("PL total (com minoritários)", fim, fonte{"pl", true}, fonte{"pl", false})
	ebit, partesEbit, okEbit := j.soma("EBIT", c.ebit)
	da, partesDA, okDA := j.soma("Depreciação e amortização", c.da)

	if okEbit && okPL && okDL {
		v := dividir(ebit*(1-Aliquota), plTotal+dl, concat(partesEbit, []Parte{pPlTotal}, partesDL), "")
		v.Nota = "EBIT × 0,66 (desconta 34% de IR e CSLL)"
		col.Valores["roic"] = v
	} else {
		col.Valores["roic"] = Valor{Motivo: motivoSemDado}
	}
	if okEbit && okDA && okDL {
		col.Valores["dl_ebitda"] = dividir(dl, ebit+da, concat(partesDL, partesEbit, partesDA), "")
	} else {
		col.Valores["dl_ebitda"] = Valor{Motivo: motivoSemDado}
	}
	if okDL && okPL {
		col.Valores["dl_pl"] = dividir(dl, plTotal, concat(partesDL, []Parte{pPlTotal}), "")
	} else {
		col.Valores["dl_pl"] = Valor{Motivo: motivoSemDado}
	}
	ac, pAC, okAC := c.saldo("Ativo circulante", fim, fonte{"ativo_circulante", true}, fonte{"ativo_circulante", false})
	pc, pPC, okPC := c.saldo("Passivo circulante", fim, fonte{"passivo_circulante", true}, fonte{"passivo_circulante", false})
	if okAC && okPC {
		col.Valores["liquidez"] = dividir(ac, pc, []Parte{pAC, pPC}, "")
	} else {
		col.Valores["liquidez"] = Valor{Motivo: motivoSemDado}
	}
	return col
}

// dividaLiquida = empréstimos de curto e longo prazo − caixa − aplicações financeiras.
// Conta que a empresa não tem (ex.: sem aplicações) entra como zero.
func (c calc) dividaLiquida(d time.Time) (float64, []Parte, bool) {
	itens := []struct {
		chave, nome string
		sinal       float64
	}{
		{"emprestimos_cp", "Empréstimos curto prazo", 1},
		{"emprestimos_lp", "Empréstimos longo prazo", 1},
		{"caixa", "Caixa", -1},
		{"aplicacoes", "Aplicações financeiras", -1},
	}
	var total float64
	var partes []Parte
	achouAlgum := false
	for _, it := range itens {
		v, p, ok := c.saldo(it.nome, d, fonte{it.chave, true}, fonte{it.chave, false})
		if !ok {
			continue
		}
		achouAlgum = true
		total += it.sinal * v
		if it.sinal < 0 {
			p.Nome = "(−) " + p.Nome
		}
		partes = append(partes, p)
	}
	return total, partes, achouAlgum
}

func dividir(a, b float64, partes []Parte, nota string) Valor {
	if b == 0 {
		return Valor{Motivo: "divisão por zero", Partes: partes}
	}
	return Valor{Numero: a / b, OK: true, Partes: partes, Nota: nota}
}

func concat(ls ...[]Parte) []Parte {
	var r []Parte
	for _, l := range ls {
		r = append(r, l...)
	}
	return r
}

// ---------- P/L e EV/EBITDA ----------

// mercado é o valor de mercado numa data, com a memória de cálculo.
// Motivo preenchido = não deu para calcular.
type mercado struct {
	valor  float64
	partes []Parte
	nota   string
	motivo string
}

// mercadoAno usa o fechamento do último pregão do ano (sem ajuste: é o valor
// de mercado real daquele dia) e o número de ações do balanço do mesmo ano.
func (c calc) mercadoAno(t cvm.Ticker, comps []cvm.Composicao, h Historico, a periodo, r *Resultado) mercado {
	if len(h.Precos) == 0 {
		return mercado{motivo: motivoHistorico}
	}
	p, ok := ultimoAte(h.Precos, a.fim)
	if !ok || a.fim.Sub(p.Data) > 15*24*time.Hour {
		return mercado{motivo: "sem pregão no fim do ano"}
	}
	cot := Cotacao{Preco: p.Valor, Fonte: fmt.Sprintf("%s, fechamento de %s", h.Fonte, formato.Data(p.Data))}
	return c.valorMercado(t, comps, cot, a.fim, a, r)
}

// valorMercado usa todas as ações (ON + PN, sem tesouraria) ao preço por
// ação do código consultado; numa unit, preço por ação = preço da unit ÷
// ações dentro dela. O número de ações é o da composição mais nova até a
// data ate (zero = sem limite); ano é o exercício usado para conferir se ele
// veio em milhares.
func (c calc) valorMercado(t cvm.Ticker, comps []cvm.Composicao, cot Cotacao, ate time.Time, ano periodo, r *Resultado) mercado {
	if cot.Preco <= 0 {
		return mercado{motivo: "sem cotação"}
	}
	if t.AcoesPorUnidade <= 0 {
		return mercado{motivo: "composição da unit desconhecida"}
	}
	if !ate.IsZero() {
		comps = slices.DeleteFunc(slices.Clone(comps), func(x cvm.Composicao) bool { return x.DtRefer.After(ate) })
	}
	if len(comps) == 0 {
		return mercado{motivo: "número de ações não encontrado na CVM"}
	}
	comp := slices.MaxFunc(comps, func(a, b cvm.Composicao) int {
		if x := a.DtRefer.Compare(b.DtRefer); x != 0 {
			return x
		}
		return cmp.Compare(a.Versao, b.Versao)
	})
	acoes := comp.Total()
	m := mercado{}
	if fator := c.escalaAcoes(acoes, ano); fator != 1 {
		acoes *= fator
		m.nota = "a CVM traz o número de ações em milhares para esta empresa; multiplicado por 1.000 (conferido com o lucro por ação)"
		r.avisar("número de ações veio em milhares na CVM; corrigido com o lucro por ação")
	}
	precoAcao := cot.Preco / t.AcoesPorUnidade
	m.valor = acoes * precoAcao

	m.partes = []Parte{{
		Nome: "Ações (ON + PN − tesouraria)", Valor: acoes,
		Origem: fmt.Sprintf("composição do capital, %s", formato.Data(comp.DtRefer)),
	}, {
		Nome: "Cotação " + t.Codigo, Valor: cot.Preco, Moeda: true, Origem: cot.Fonte,
	}}
	if !cot.Horario.IsZero() {
		m.partes[1].Origem += ", " + cot.Horario.Local().Format("02/01/2006 15:04")
	}
	if t.AcoesPorUnidade != 1 {
		m.partes = append(m.partes, Parte{Nome: "Ações por unit", Valor: t.AcoesPorUnidade, Origem: "FCA (CVM)"})
	}
	m.partes = append(m.partes, Parte{Nome: "Valor de mercado", Valor: m.valor, Moeda: true, Origem: "ações × preço por ação"})
	return m
}

// pl = valor de mercado ÷ lucro.
func pl(m mercado, lucro Valor) Valor {
	if !lucro.OK {
		return Valor{Motivo: motivoSemDado}
	}
	if m.motivo != "" {
		return Valor{Motivo: m.motivo}
	}
	return dividir(m.valor, lucro.Numero, concat(m.partes, lucro.Partes), m.nota)
}

// pvp = valor de mercado ÷ PL dos acionistas da empresa (sem sócios de controladas).
func (c calc) pvp(m mercado, fim time.Time) Valor {
	if m.motivo != "" {
		return Valor{Motivo: m.motivo}
	}
	pl, partes, ok := c.plControladora(fim)
	if !ok {
		return Valor{Motivo: motivoSemDado}
	}
	if pl <= 0 {
		return Valor{Motivo: "patrimônio negativo ou zero", Partes: partes}
	}
	return dividir(m.valor, pl, concat(m.partes, partes), m.nota)
}

// evEbitda = (valor de mercado + dívida líquida + PL de sócios de controladas)
// ÷ EBITDA do período. Os sócios de controladas entram porque o EBITDA
// consolidado inclui a parte deles (ex.: SOJA3 consolida o SNAG11).
func (c calc) evEbitda(m mercado, j janela, fim time.Time, financeira bool) Valor {
	if financeira {
		return Valor{Motivo: motivoFinanceira}
	}
	if m.motivo != "" {
		return Valor{Motivo: m.motivo}
	}
	dl, partesDL, okDL := c.dividaLiquida(fim)
	ebit, partesEbit, okEbit := j.soma("EBIT", c.ebit)
	da, partesDA, okDA := j.soma("Depreciação e amortização", c.da)
	if !okDL || !okEbit || !okDA {
		return Valor{Motivo: motivoSemDado}
	}
	if ebit+da <= 0 {
		return Valor{Motivo: "EBITDA negativo ou zero"}
	}
	partes := concat(m.partes, partesDL)
	ev := m.valor + dl
	if c.consolidadoEm(fim) {
		if minor, p, ok := c.saldo("PL de sócios de controladas", fim, fonte{"pl_nao_controladores", true}); ok && minor != 0 {
			ev += minor
			partes = append(partes, p)
		}
	}
	partes = append(partes, Parte{Nome: "EV (valor da firma)", Valor: ev, Moeda: true, Origem: "valor de mercado + dívida líquida + sócios de controladas"})
	partes = append(partes, Parte{Nome: "EBITDA", Valor: ebit + da, Moeda: true, Origem: "EBIT + depreciação e amortização"})
	return dividir(ev, ebit+da, concat(partes, partesEbit, partesDA), m.nota)
}

// escalaAcoes confere o número de ações com lucro ÷ lucro por ação do ano.
// Algumas empresas informam a composição do capital em milhares.
func (c calc) escalaAcoes(acoes float64, a periodo) float64 {
	if acoes <= 0 {
		return 1
	}
	// Tenta o LPA do próprio ano; sem ele, o do último ano.
	candidatos := []periodo{a}
	if anos := c.periodosAnuais(); len(anos) > 0 {
		candidatos = append(candidatos, anos[len(anos)-1])
	}
	for _, p := range candidatos {
		lucro, _, ok := c.lucro(p.ini, p.fim)
		if !ok || lucro == 0 {
			continue
		}
		for _, x := range c.contas {
			if x.Chave != "lpa" || x.Valor == 0 || !x.DtIni.Equal(p.ini) || !x.DtFim.Equal(p.fim) {
				continue
			}
			if r := lucro / x.Valor / acoes; r > 300 && r < 3000 {
				return 1000
			}
			return 1
		}
	}
	return 1
}

// trimestresNegativos: o ITR traz o acumulado do ano (3, 6 e 9 meses); o
// trimestre é a diferença entre dois acumulados seguidos, e o 4º trimestre
// é o ano (DFP) menos os 9 meses.
func (c calc) trimestresNegativos(anos []periodo) []string {
	var inicios []time.Time
	for _, a := range anos[max(0, len(anos)-2):] {
		inicios = append(inicios, a.ini)
	}
	inicios = append(inicios, anos[len(anos)-1].fim.AddDate(0, 0, 1)) // ano atual
	var l []string
	for _, ini := range inicios {
		anterior, temAnterior := 0.0, true // antes do 1º trimestre o acumulado é zero
		for q := 1; q <= 4; q++ {
			fim := ini.AddDate(0, 3*q, -1)
			v, _, ok := c.lucro(ini, fim)
			if !ok {
				temAnterior = false
				continue
			}
			if temAnterior && v-anterior < 0 {
				l = append(l, fmt.Sprintf("%dT%02d", q, fim.Year()%100))
			}
			anterior, temAnterior = v, true
		}
	}
	return l
}

// ---------- média ----------

func media(anuais []Coluna) Coluna {
	col := Coluna{Rotulo: "Média 5a", Valores: map[string]Valor{}}
	if len(anuais) > 0 {
		col.Periodo = anuais[0].Rotulo + " a " + anuais[len(anuais)-1].Rotulo
	}
	for _, d := range Lista {
		var soma float64
		var partes []Parte
		motivo := ""
		for _, a := range anuais {
			v := a.Valores[d.Chave]
			if !v.OK {
				if motivo == "" {
					motivo = v.Motivo
				}
				continue
			}
			soma += v.Numero
			partes = append(partes, Parte{Nome: d.Nome + " " + a.Rotulo, Valor: v.Numero, Moeda: d.Unidade == "R$", Unidade: d.Unidade})
		}
		if len(partes) == 0 {
			col.Valores[d.Chave] = Valor{Motivo: motivo}
			continue
		}
		v := Valor{Numero: soma / float64(len(partes)), OK: true, Partes: partes}
		if len(partes) < len(anuais) {
			v.Nota = fmt.Sprintf("média de %d anos (faltam dados nos outros)", len(partes))
		}
		col.Valores[d.Chave] = v
	}
	return col
}

// consolidadoEm diz se há balanço consolidado de verdade (PL diferente de zero) na data.
func (c calc) consolidadoEm(d time.Time) bool {
	x, ok := c.achar("pl", true, time.Time{}, d)
	return ok && x.Valor != 0
}
