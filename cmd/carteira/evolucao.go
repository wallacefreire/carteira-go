package main

// Evolução da carteira: rentabilidade pelo método de cotas, comparada com
// Ibovespa, CDI, IPCA, S&P 500 e IFIX, e o patrimônio contra o total investido.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/evolucao"
	"carteira/internal/fontes/bcb"
	"carteira/internal/fontes/coingecko"
	"carteira/internal/fontes/tesouro"
	"carteira/internal/formato"
	"carteira/internal/risco"
)

// Carteira vazia por pelo menos isso (uns 3 meses) e depois de novo com
// ativos: a volta vira o período "Desde a volta".
const pregoesParaRecomeco = 60

// As séries do Banco Central são baixadas de novo depois disso.
const validadeBCB = 12 * time.Hour

// comparacao é uma linha do gráfico além da carteira. Os índices de ações
// não estão no COTAHIST: entra o ETF da B3 que replica cada um, com os
// proventos reinvestidos (preço ajustado).
type comparacao struct {
	chave, nome, ticker, fonte string
	sgs                        int // série do Banco Central, quando não é ETF
}

var comparacoes = []comparacao{
	{chave: "ibov", nome: "Ibovespa", ticker: "BOVA11", fonte: "BOVA11 (ETF do Ibovespa), COTAHIST da B3, com proventos reinvestidos"},
	{chave: "cdi", nome: "CDI", sgs: bcb.SerieCDI, fonte: "CDI diário, Banco Central (série 12 do SGS)"},
	{chave: "ipca", nome: "IPCA", sgs: bcb.SerieIPCA, fonte: "IPCA mensal, Banco Central (série 433 do SGS), distribuído pelos dias do mês"},
	{chave: "sp500", nome: "S&P 500 (em R$)", ticker: "IVVB11", fonte: "IVVB11 (ETF do S&P 500 em reais: inclui a variação do dólar), COTAHIST da B3"},
	{chave: "ifix", nome: "IFIX", ticker: "XFIX11", fonte: "XFIX11 (ETF do IFIX, fundos imobiliários), COTAHIST da B3, com proventos reinvestidos"},
}

type serieEvolucao struct {
	Chave   string     `json:"chave"`
	Nome    string     `json:"nome"`
	Fonte   string     `json:"fonte"`
	Valores []*float64 `json:"valores"` // índice, 1 no primeiro dia; null sem dado
}

type linhaRentabilidade struct {
	Periodo string     `json:"periodo"`
	Parcial bool       `json:"parcial"`
	Valores []string   `json:"valores"` // na ordem das séries; "-" sem dado
	Nums    []*float64 `json:"nums"`
}

type dadosEvolucao struct {
	Vazia           bool                 `json:"vazia"`
	Data            string               `json:"data"`
	Inicio          string               `json:"inicio"`
	Ate             string               `json:"ate"`
	Datas           []string             `json:"datas"` // AAAA-MM-DD
	Series          []serieEvolucao      `json:"series"`
	Patrimonio      []float64            `json:"patrimonio"`
	Investido       []float64            `json:"investido"`
	Proventos       []float64            `json:"proventos"` // recebidos, acumulado
	Recomeco        int                  `json:"recomeco"`  // índice da base do período "Desde a volta"; -1 sem volta
	RecomecoData    string               `json:"recomecoData"`
	PatrimonioAtual string               `json:"patrimonioAtual"`
	InvestidoAtual  string               `json:"investidoAtual"`
	Rentabilidade   []linhaRentabilidade `json:"rentabilidade"`
	Notas           []string             `json:"notas"`
}

func (a *app) dadosEvolucao(ctx context.Context) (dadosEvolucao, error) {
	d := dadosEvolucao{Data: a.agora().Format("02/01/2006 15:04")}
	todas, err := a.banco.Operacoes("") // já sem as operações de terceiros
	if err != nil {
		return d, err
	}
	ocultos, err := a.banco.Ocultos()
	if err != nil {
		return d, err
	}
	var ops []carteira.Operacao
	for _, op := range todas {
		if !ehOculto(ocultos, op.Ticker) {
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		d.Vazia = true
		return d, nil
	}
	provs, err := a.banco.Proventos()
	if err != nil {
		return d, err
	}
	provs = slices.DeleteFunc(provs, func(p carteira.Provento) bool { return ehOculto(ocultos, p.Ticker) })

	// Calendário: os pregões do BOVA11.
	if err := a.atualizarPrecos(ctx, false); err != nil {
		d.Notas = append(d.Notas, "histórico de preços da B3 não atualizado: "+err.Error())
	}
	bova, err := a.banco.Precos(indiceMercado)
	if err != nil {
		return d, err
	}
	if len(bova) == 0 {
		return d, fmt.Errorf("sem o histórico do %s: rode carteira precos", indiceMercado)
	}
	var dias []time.Time
	for _, p := range bova {
		dias = append(dias, p.Data)
	}

	precos, notas := a.precosDasOperacoes(ctx, ops)
	d.Notas = append(d.Notas, notas...)
	c := evolucao.Calcular(evolucao.Entrada{Dias: dias, Operacoes: ops, Proventos: provs, Precos: precos})
	if len(c.Dias) < 2 {
		d.Vazia = true
		return d, nil
	}
	if len(c.SemPreco) > 0 {
		d.Notas = append(d.Notas, "sem preço diário em parte do tempo (BDR, ou ativo fora do histórico da B3): "+
			strings.Join(c.SemPreco, ", ")+". Nesses dias vale o preço da última compra ou venda.")
	}

	indices := [][]float64{c.Cota}
	d.Series = append(d.Series, serieEvolucao{Chave: "carteira", Nome: "Carteira", Fonte: "suas operações e proventos (método de cotas)"})
	for _, cmp := range comparacoes {
		idx, nota := a.indiceComparacao(ctx, cmp, c.Dias)
		if nota != "" {
			d.Notas = append(d.Notas, nota)
		}
		indices = append(indices, idx)
		d.Series = append(d.Series, serieEvolucao{Chave: cmp.chave, Nome: cmp.nome, Fonte: cmp.fonte})
	}
	for i, idx := range indices {
		d.Series[i].Valores = paraJSON(idx, 6)
	}

	for _, dia := range c.Dias {
		d.Datas = append(d.Datas, dia.Format("2006-01-02"))
	}
	n := len(c.Dias)
	for i := range c.Dias {
		d.Patrimonio = append(d.Patrimonio, math.Round(c.Patrimonio[i]*100)/100)
		d.Investido = append(d.Investido, math.Round(c.Investido[i]*100)/100)
		d.Proventos = append(d.Proventos, math.Round(c.Proventos[i]*100)/100)
	}
	d.Inicio, d.Ate = formato.Data(c.Dias[0]), formato.Data(c.Dias[n-1])
	d.PatrimonioAtual, d.InvestidoAtual = formato.Moeda(c.Patrimonio[n-1]), formato.Moeda(c.Investido[n-1])

	periodos := evolucao.Periodos(c.Dias)
	d.Recomeco = evolucao.Recomeco(c.Patrimonio, pregoesParaRecomeco)
	if d.Recomeco >= 0 {
		d.RecomecoData = formato.Data(c.Dias[d.Recomeco+1])
		// Logo depois dos 12 meses, antes do "Desde o início".
		volta := evolucao.Periodo{Rotulo: "Desde a volta (" + d.RecomecoData + ")", Ini: d.Recomeco, Fim: n - 1}
		periodos = slices.Insert(periodos, len(periodos)-1, volta)
	}
	for _, p := range periodos {
		l := linhaRentabilidade{Periodo: p.Rotulo, Parcial: p.Parcial}
		for _, idx := range indices {
			r, ate, ok := evolucao.Retorno(idx, p)
			if !ok {
				l.Valores, l.Nums = append(l.Valores, "-"), append(l.Nums, nil)
				continue
			}
			txt := formato.Pct(r)
			if ate < p.Fim {
				txt += " (até " + c.Dias[ate].Format("01/06") + ")"
			}
			l.Valores, l.Nums = append(l.Valores, txt), append(l.Nums, &r)
		}
		d.Rentabilidade = append(d.Rentabilidade, l)
	}
	d.Notas = append(d.Notas, fmt.Sprintf("Preços de fechamento até %s (COTAHIST da B3, atualizado 1x por semana): o patrimônio daqui pode diferir do da posição, que usa a cotação do momento.", d.Ate))
	return d, nil
}

// paraJSON arredonda e troca NaN por null (o JSON não tem NaN).
func paraJSON(v []float64, casas int) []*float64 {
	f := math.Pow(10, float64(casas))
	res := make([]*float64, len(v))
	for i, x := range v {
		if !math.IsNaN(x) {
			r := math.Round(x*f) / f
			res[i] = &r
		}
	}
	return res
}

// precosDasOperacoes busca o histórico de preço de cada ativo que já esteve
// na carteira: COTAHIST (bolsa), Tesouro Transparente (Tesouro Direto) ou
// CoinGecko (BTC). Sem histórico, o cálculo usa o preço das operações.
func (a *app) precosDasOperacoes(ctx context.Context, ops []carteira.Operacao) (map[string][]risco.Ponto, []string) {
	precos := map[string][]risco.Ponto{}
	var notas []string
	titulos := map[string]bool{}
	var primeiroBTC time.Time
	for _, op := range ops {
		switch {
		case carteira.EhTesouro(op.Ticker):
			titulos[op.Ticker] = true
		case op.Ticker == carteira.Bitcoin:
			if primeiroBTC.IsZero() || op.Data.Before(primeiroBTC) {
				primeiroBTC = op.Data
			}
		default:
			if _, ok := precos[op.Ticker]; ok {
				continue
			}
			ps, err := a.banco.Precos(op.Ticker)
			if err != nil {
				notas = append(notas, op.Ticker+": "+err.Error())
			}
			precos[op.Ticker] = nil
			for _, p := range ps {
				precos[op.Ticker] = append(precos[op.Ticker], risco.Ponto{Data: p.Data, Valor: p.Fechamento})
			}
		}
	}
	if len(titulos) > 0 {
		hist, err := a.historicoTesouro(ctx, titulos)
		if err != nil {
			notas = append(notas, "Tesouro Direto sem histórico de preços ("+err.Error()+"): vale o preço das operações")
		}
		for t, ps := range hist {
			for _, p := range ps {
				precos[t] = append(precos[t], risco.Ponto{Data: p.DataBase, Valor: p.PUVenda})
			}
		}
	}
	if !primeiroBTC.IsZero() {
		dias := int(a.agora().Sub(primeiroBTC).Hours()/24) + 3
		if dias > coingecko.MaxDiasHistorico {
			notas = append(notas, "BTC: a CoinGecko grátis só dá 1 ano de histórico; antes disso vale o preço das operações")
		}
		ps, err := a.cripto.HistoricoBitcoin(ctx, dias)
		if err != nil {
			notas = append(notas, "BTC sem histórico de preços ("+err.Error()+"): vale o preço das operações")
		}
		for _, p := range ps {
			precos[carteira.Bitcoin] = append(precos[carteira.Bitcoin], risco.Ponto{Data: p.Dia, Valor: p.Valor})
		}
	}
	return precos, notas
}

// historicoTesouro lê do CSV do Tesouro Transparente o histórico dos títulos pedidos.
func (a *app) historicoTesouro(ctx context.Context, titulos map[string]bool) (map[string][]tesouro.Preco, error) {
	caminho, err := a.arquivoTesouro(ctx)
	if err != nil {
		return nil, err
	}
	return tesouro.Historico(caminho, titulos)
}

// indiceComparacao monta o índice de uma comparação nos dias da carteira.
func (a *app) indiceComparacao(ctx context.Context, cmp comparacao, dias []time.Time) ([]float64, string) {
	semDado := func() []float64 {
		v := make([]float64, len(dias))
		for i := range v {
			v[i] = math.NaN()
		}
		return v
	}
	if cmp.ticker != "" {
		s, err := a.serie(ctx, cmp.ticker)
		if err != nil || len(s.ajustado) == 0 {
			return semDado(), cmp.nome + ": sem o histórico do " + cmp.ticker + " no COTAHIST"
		}
		return evolucao.IndicePrecos(dias, s.ajustado), s.nota
	}
	// Do mês do início, para o IPCA ter o mês inteiro.
	desde := time.Date(dias[0].Year(), dias[0].Month(), 1, 0, 0, 0, 0, time.UTC)
	vs, nota := a.serieBCB(ctx, cmp.sgs, desde)
	if len(vs) == 0 {
		return semDado(), cmp.nome + ": " + nota
	}
	var ps []risco.Ponto
	for _, v := range vs {
		ps = append(ps, risco.Ponto{Data: v.Data, Valor: v.Valor})
	}
	if nota != "" {
		nota = cmp.nome + ": " + nota
	}
	if cmp.sgs == bcb.SerieIPCA {
		return evolucao.IndiceIPCA(dias, ps), nota
	}
	return evolucao.IndiceCDI(dias, ps), nota
}

// serieBCB lê uma série do Banco Central, guardada na config por 12 horas
// (chaves sgs_<código> e sgs_<código>_em). Se o download falhar, usa a guardada.
func (a *app) serieBCB(ctx context.Context, codigo int, desde time.Time) ([]bcb.Valor, string) {
	chave := fmt.Sprintf("sgs_%d", codigo)
	var salvos []bcb.Valor
	if s, ok, _ := a.banco.ConfigTexto(chave); ok {
		json.Unmarshal([]byte(s), &salvos)
	}
	cobre := len(salvos) > 0 && !salvos[0].Data.After(desde.AddDate(0, 1, 0))
	if em, ok, _ := a.banco.ConfigTexto(chave + "_em"); ok && cobre {
		if t, err := time.Parse(time.RFC3339, em); err == nil && a.agora().Sub(t) < validadeBCB {
			return salvos, ""
		}
	}
	// Séries diárias: no máximo 10 anos por consulta.
	if limite := a.agora().AddDate(-10, 0, 1); desde.Before(limite) {
		desde = limite
	}
	vs, err := a.bcb.Serie(ctx, codigo, desde, a.agora())
	if err != nil || len(vs) == 0 {
		if err == nil {
			err = fmt.Errorf("Banco Central (série %d): resposta vazia", codigo)
		}
		if len(salvos) > 0 {
			return salvos, err.Error() + "; usada a série baixada antes"
		}
		return nil, err.Error()
	}
	if b, err := json.Marshal(vs); err == nil {
		a.banco.SalvarConfigTexto(chave, string(b))
		a.banco.SalvarConfigTexto(chave+"_em", a.agora().Format(time.RFC3339))
	}
	return vs, ""
}

// ---------- carteira evolucao ----------

func (a *app) evolucao(ctx context.Context) error {
	d, err := a.dadosEvolucao(ctx)
	if err != nil {
		return err
	}
	if d.Vazia {
		fmt.Fprintln(a.saida, "Sem operações para calcular a evolução.")
		return nil
	}
	fmt.Fprintf(a.saida, "Rentabilidade de %s a %s (método de cotas: aporte não conta como ganho; provento conta)\n\n", d.Inicio, d.Ate)
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprint(tw, "\t")
	for _, s := range d.Series {
		fmt.Fprintf(tw, "%s\t", s.Nome)
	}
	fmt.Fprintln(tw)
	for _, l := range d.Rentabilidade {
		rot := l.Periodo
		if l.Parcial {
			rot += "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t\n", rot, strings.Join(l.Valores, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(a.saida, "* ano incompleto\n\nPatrimônio em %s: R$ %s · compras − vendas desde o início: R$ %s\n", d.Ate, d.PatrimonioAtual, d.InvestidoAtual)
	if i := d.Recomeco; i >= 0 {
		n := len(d.Patrimonio) - 1
		aportes := d.Investido[n] - d.Investido[i]
		provs := d.Proventos[n] - d.Proventos[i]
		fmt.Fprintf(a.saida, "Desde a volta (%s): entraram R$ %s, vale R$ %s e pagou R$ %s de proventos: resultado de R$ %s\n",
			d.RecomecoData, formato.Moeda(aportes), formato.Moeda(d.Patrimonio[n]), formato.Moeda(provs),
			formato.Moeda(d.Patrimonio[n]-d.Patrimonio[i]-aportes+provs))
	}
	fmt.Fprintln(a.saida, "\nFontes:")
	for _, s := range d.Series {
		fmt.Fprintf(a.saida, "  %s: %s\n", s.Nome, s.Fonte)
	}
	imprimirLista(a.saida, "Observações:", d.Notas)
	fmt.Fprintln(a.saida, "\nGráfico: carteira web, aba Evolução")
	return nil
}
