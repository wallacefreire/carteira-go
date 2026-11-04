package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"carteira/internal/armazenamento"
	"carteira/internal/carteira"
	"carteira/internal/fontes/b3"
	"carteira/internal/fontes/baixar"
	"carteira/internal/fontes/tesouro"
	"carteira/internal/formato"
)

// O CSV do Tesouro é atualizado uma vez por dia útil.
const validadeTesouro = 12 * time.Hour

// IR retido na fonte sobre juros sobre capital próprio.
const irJCP = 0.15

// ---------- cotações por tipo de ativo ----------

// buscarCotacao escolhe a fonte pelo tipo de ativo: Tesouro Transparente,
// CoinGecko (BTC) ou brapi (bolsa).
func (a *app) buscarCotacao(ctx context.Context, t string) (armazenamento.Cotacao, error) {
	switch {
	case carteira.EhTesouro(t):
		ps, err := a.precosTesouro(ctx)
		if err != nil {
			return armazenamento.Cotacao{}, err
		}
		p, ok := ps[t]
		if !ok {
			return armazenamento.Cotacao{}, errors.New("título não encontrado nos preços do Tesouro Transparente")
		}
		dia := time.Date(p.DataBase.Year(), p.DataBase.Month(), p.DataBase.Day(), 0, 0, 0, 0, time.Local)
		return armazenamento.Cotacao{Ticker: t, Preco: p.PUVenda, Horario: dia, Fonte: "Tesouro Transparente (preço de venda)"}, nil
	case t == carteira.Bitcoin:
		v, h, err := a.cripto.Bitcoin(ctx)
		if err != nil {
			return armazenamento.Cotacao{}, err
		}
		return armazenamento.Cotacao{Ticker: t, Preco: v, Horario: h, Fonte: "CoinGecko"}, nil
	default:
		c, err := a.brapi.Cotacao(ctx, t)
		if err != nil {
			return armazenamento.Cotacao{}, err
		}
		return armazenamento.Cotacao{Ticker: t, Preco: c.Preco, Horario: c.Horario, Fonte: "brapi"}, nil
	}
}

// precosTesouro baixa o CSV do Tesouro Transparente (~15 MB) se tiver mais
// de 12 horas e lê o preço mais recente de cada título.
func (a *app) precosTesouro(ctx context.Context) (map[string]tesouro.Preco, error) {
	if a.tesouro != nil {
		return a.tesouro, nil
	}
	caminho := filepath.Join(a.dirDados, "tesouro", "PrecoTaxaTesouroDireto.csv")
	if info, err := os.Stat(caminho); err != nil || a.agora().Sub(info.ModTime()) > validadeTesouro {
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			return nil, err
		}
		fmt.Fprintln(a.saida, "baixando os preços do Tesouro Direto (Tesouro Transparente)…")
		cli := &http.Client{Timeout: 5 * time.Minute}
		if err := baixar.Arquivo(ctx, cli, tesouro.URL, caminho); err != nil {
			if _, errVelho := os.Stat(caminho); errVelho != nil {
				return nil, fmt.Errorf("preços do Tesouro: %w", err)
			}
			fmt.Fprintf(a.saida, "aviso: não consegui atualizar os preços do Tesouro (%v); usando o arquivo anterior\n", err)
		}
	}
	ps, err := tesouro.Ler(caminho)
	if err != nil {
		return nil, fmt.Errorf("preços do Tesouro: %w", err)
	}
	a.tesouro = ps
	return ps, nil
}

// ---------- classes ----------

// classe agrupa os ativos para os pesos por classe.
func (a *app) classe(t string) string {
	switch {
	case carteira.EhTesouro(t):
		return "Tesouro Direto"
	case t == carteira.Bitcoin:
		return "Cripto"
	}
	if _, ok, err := a.banco.TickerCVM(t); err == nil && ok {
		return "Ações"
	}
	if !strings.HasSuffix(t, "11") {
		return "Ações" // sem dados da CVM ainda: 3, 4, 5, 6 são ações
	}
	return "FIIs e ETFs"
}

// classes soma o valor por classe, da maior para a menor.
func (a *app) classes(linhas []carteira.Linha, total float64) []itemValor {
	if total <= 0 {
		return nil
	}
	soma := map[string]float64{}
	for _, l := range linhas {
		if l.Cotacao > 0 {
			soma[a.classe(l.Ticker)] += l.Valor
		}
	}
	nomes := slices.Collect(maps.Keys(soma))
	slices.SortFunc(nomes, func(x, y string) int { return cmp.Compare(soma[y], soma[x]) })
	var l []itemValor
	for _, c := range nomes {
		l = append(l, itemValor{Nome: c, Valor: formato.Moeda(soma[c]), Pct: formato.Pct(soma[c] / total)})
	}
	return l
}

// ---------- carteira proventos ----------

// anoProventos é uma linha da tabela ano × mês.
type anoProventos struct {
	Ano      int         `json:"ano"`
	Meses    [12]string  `json:"meses"` // "-" quando não houve
	MesesNum [12]float64 `json:"mesesNum"`
	Total    string      `json:"total"`
}

// ativoProventos é o total recebido de um ativo.
type ativoProventos struct {
	Ticker     string `json:"ticker"`
	NaCarteira bool   `json:"naCarteira"`
	Total      string `json:"total"`
	Pct        string `json:"pct"`
	Pagamentos int    `json:"pagamentos"`
	Ultimo     string `json:"ultimo"`
}

type dadosProventos struct {
	Ano        int              `json:"ano"` // 0 = todos
	Anos       []anoProventos   `json:"anos"`
	Ativos     []ativoProventos `json:"ativos"`
	Tipos      []itemValor      `json:"tipos"`
	Total      string           `json:"total"`
	Pagamentos int              `json:"pagamentos"`
	Lista      []pagamento      `json:"lista"` // cada crédito, para o detalhe do mês na tela
}

// pagamento é um provento creditado na conta.
type pagamento struct {
	Ano    int    `json:"ano"`
	Mes    int    `json:"mes"` // 1 a 12
	Data   string `json:"data"`
	Ticker string `json:"ticker"`
	Tipo   string `json:"tipo"`
	Valor  string `json:"valor"`
}

func (a *app) proventos(args []string) error {
	ano := 0
	if len(args) > 1 {
		return errors.New("uso: carteira proventos [ANO]")
	}
	if len(args) == 1 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1990 || v > 2100 {
			return fmt.Errorf("ano inválido %q", args[0])
		}
		ano = v
	}
	d, err := a.dadosProventos(ano)
	if err != nil {
		return err
	}
	if d.Pagamentos == 0 {
		fmt.Fprintln(a.saida, "Nenhum provento recebido no período. Os proventos vêm do extrato da B3: carteira importar <movimentacao.xlsx>")
		return nil
	}

	titulo := "Proventos recebidos (R$)"
	if ano != 0 {
		titulo += fmt.Sprintf(" em %d", ano)
	}
	fmt.Fprintln(a.saida, titulo+"\n")
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "Ano\tJan\tFev\tMar\tAbr\tMai\tJun\tJul\tAgo\tSet\tOut\tNov\tDez\tTotal\t")
	for _, y := range d.Anos {
		linha := append([]string{strconv.Itoa(y.Ano)}, y.Meses[:]...)
		fmt.Fprintln(tw, strings.Join(append(linha, y.Total), "\t")+"\t")
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(a.saida, "\nPor ativo (* = ainda na carteira):")
	tw = tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "Ativo\tTotal\t%\tPagamentos\tÚltimo\t")
	for _, t := range d.Ativos {
		nome := t.Ticker
		if t.NaCarteira {
			nome = "* " + nome
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t\n", nome, t.Total, t.Pct, t.Pagamentos, t.Ultimo)
	}
	fmt.Fprintf(tw, "Total\t%s\t\t%d\t\t\n", d.Total, d.Pagamentos)
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(a.saida, "\nPor tipo:")
	for _, t := range d.Tipos {
		fmt.Fprintf(a.saida, "  %s: R$ %s\n", t.Nome, t.Valor)
	}
	fmt.Fprintln(a.saida, "\nValores líquidos, como creditados (o JCP já vem com 15% de IR descontado). Fonte: extrato de Movimentação da B3.")
	return nil
}

// dadosProventos monta as tabelas de proventos recebidos (ano 0 = todos).
func (a *app) dadosProventos(ano int) (dadosProventos, error) {
	d := dadosProventos{Ano: ano}
	todos, err := a.banco.Proventos()
	if err != nil {
		return d, err
	}
	var ps []carteira.Provento
	for _, p := range todos {
		if ano == 0 || p.Data.Year() == ano {
			ps = append(ps, p)
		}
	}
	if len(ps) == 0 {
		return d, nil
	}

	// Ano × mês, e totais por ativo.
	porMes := map[int]*[12]float64{}
	porAtivo := map[string]float64{}
	pagamentos := map[string]int{}
	ultimo := map[string]time.Time{}
	porTipo := map[string]float64{}
	var total float64
	for _, p := range ps {
		if porMes[p.Data.Year()] == nil {
			porMes[p.Data.Year()] = &[12]float64{}
		}
		porMes[p.Data.Year()][p.Data.Month()-1] += p.Valor
		porAtivo[p.Ticker] += p.Valor
		pagamentos[p.Ticker]++
		if p.Data.After(ultimo[p.Ticker]) {
			ultimo[p.Ticker] = p.Data
		}
		porTipo[p.Tipo] += p.Valor
		d.Lista = append(d.Lista, pagamento{Ano: p.Data.Year(), Mes: int(p.Data.Month()), Data: formato.Data(p.Data),
			Ticker: p.Ticker, Tipo: p.Tipo, Valor: formato.Moeda(p.Valor)})
		total += p.Valor
	}

	for _, y := range slices.Sorted(maps.Keys(porMes)) {
		linha := anoProventos{Ano: y, MesesNum: *porMes[y]}
		var soma float64
		for i, v := range porMes[y] {
			soma += v
			linha.Meses[i] = valorOuTraco(v)
		}
		linha.Total = formato.Moeda(soma)
		d.Anos = append(d.Anos, linha)
	}

	pos, err := a.posicoesAbertas()
	if err != nil {
		return d, err
	}
	ativos := slices.Collect(maps.Keys(porAtivo))
	slices.SortFunc(ativos, func(x, y string) int { return cmp.Compare(porAtivo[y], porAtivo[x]) })
	for _, t := range ativos {
		d.Ativos = append(d.Ativos, ativoProventos{Ticker: t, NaCarteira: pos[t], Total: formato.Moeda(porAtivo[t]),
			Pct: formato.Pct(porAtivo[t] / total), Pagamentos: pagamentos[t], Ultimo: formato.Data(ultimo[t])})
	}
	for _, t := range slices.Sorted(maps.Keys(porTipo)) {
		d.Tipos = append(d.Tipos, itemValor{Nome: t, Valor: formato.Moeda(porTipo[t])})
	}
	d.Total = formato.Moeda(total)
	d.Pagamentos = len(ps)
	return d, nil
}

// posicoesAbertas diz quais ativos têm quantidade hoje.
func (a *app) posicoesAbertas() (map[string]bool, error) {
	ops, err := a.banco.Operacoes("")
	if err != nil {
		return nil, err
	}
	pos, _ := carteira.CalcularPosicoes(ops)
	abertas := map[string]bool{}
	for t, p := range pos {
		if p.Quantidade > 0 {
			abertas[t] = true
		}
	}
	return abertas, nil
}

func valorOuTraco(v float64) string {
	if v == 0 {
		return "-"
	}
	return formato.Moeda(v)
}

// ---------- carteira agenda ----------

type itemAgenda struct {
	ticker     string
	anuncio    b3.Anuncio
	qtd        float64
	bruto, liq float64
	comFutura  bool
}

// linhaAgenda é um provento a receber, já formatado.
type linhaAgenda struct {
	Pagamento string `json:"pagamento"` // "a definir" sem data
	Ticker    string `json:"ticker"`
	Tipo      string `json:"tipo"`
	DataCom   string `json:"dataCom"`
	ComFutura bool   `json:"comFutura"` // data-com ainda não passou
	Qtd       string `json:"qtd"`
	ValorAcao string `json:"valorAcao"`
	Bruto     string `json:"bruto"`
	Liquido   string `json:"liquido"`
	Referente string `json:"referente"`
}

type dadosAgenda struct {
	Data         string        `json:"data"`
	Itens        []linhaAgenda `json:"itens"`
	TotalBruto   string        `json:"totalBruto"`
	TotalLiquido string        `json:"totalLiquido"`
	Notas        []string      `json:"notas"`
}

// agenda: proventos anunciados pelas empresas da carteira que ainda não foram pagos.
func (a *app) agenda(ctx context.Context) error {
	d, err := a.dadosAgenda(ctx)
	if err != nil {
		return err
	}
	if len(d.Itens) == 0 {
		fmt.Fprintln(a.saida, "Nenhum provento anunciado a receber.")
		imprimirLista(a.saida, "Observações:", d.Notas)
		return nil
	}
	fmt.Fprintf(a.saida, "Proventos a receber em %s (R$)\n\n", d.Data)
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "Pagamento\tAtivo\tTipo\tData-com\tQtd\tR$/ação\tBruto\tLíquido\t")
	algumaFutura := false
	for _, it := range d.Itens {
		com := it.DataCom
		if it.ComFutura {
			com += " *"
			algumaFutura = true
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", it.Pagamento, it.Ticker, it.Tipo, com, it.Qtd, it.ValorAcao, it.Bruto, it.Liquido)
	}
	fmt.Fprintf(tw, "Total\t\t\t\t\t\t%s\t%s\t\n", d.TotalBruto, d.TotalLiquido)
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(a.saida, "\nQuantidade = ações que você tinha na data-com (pelas operações gravadas). JCP tem 15% de IR retido.")
	if algumaFutura {
		fmt.Fprintln(a.saida, "* data-com ainda não passou: vale a quantidade que você tiver nesse dia (usada a de hoje).")
	}
	fmt.Fprintln(a.saida, "Fonte: site da B3 (empresas listadas). Compras que ainda não estão no extrato não entram na conta.")
	imprimirLista(a.saida, "Observações:", d.Notas)
	return nil
}

// dadosAgenda busca na B3 os proventos anunciados e calcula quanto cabe a você.
func (a *app) dadosAgenda(ctx context.Context) (dadosAgenda, error) {
	d := dadosAgenda{Data: formato.Data(a.agora())}
	ops, err := a.banco.Operacoes("")
	if err != nil {
		return d, err
	}
	hoje := a.agora()
	hojeData := time.Date(hoje.Year(), hoje.Month(), hoje.Day(), 0, 0, 0, 0, time.UTC)
	pos, _ := carteira.CalcularPosicoes(ops)

	// Ativos da bolsa que tem hoje ou que movimentou nos últimos 12 meses
	// (pode ter vendido depois da data-com e ainda ter a receber).
	ultimaOp := map[string]time.Time{}
	for _, op := range ops {
		if op.Data.After(ultimaOp[op.Ticker]) {
			ultimaOp[op.Ticker] = op.Data
		}
	}
	var tickers []string
	for t, p := range pos {
		if carteira.EhB3(t) && (p.Quantidade > 0 || hoje.Sub(ultimaOp[t]) < 365*24*time.Hour) {
			tickers = append(tickers, t)
		}
	}
	slices.Sort(tickers)
	porEmpresa := map[string][]string{}
	for _, t := range tickers {
		porEmpresa[t[:4]] = append(porEmpresa[t[:4]], t)
	}

	var itens []itemAgenda
	var notas []string
	for _, emp := range slices.Sorted(maps.Keys(porEmpresa)) {
		anuncios, err := a.b3.Anunciados(ctx, emp)
		if err != nil {
			notas = append(notas, err.Error())
			continue
		}
		for _, an := range anuncios {
			if !an.Pagamento.IsZero() && an.Pagamento.Before(hojeData) {
				continue // já pago: aparece no extrato e em "carteira proventos"
			}
			for _, t := range porEmpresa[emp] {
				if b3.EspecieDoISIN(an.ISIN) != b3.EspecieDoTicker(t) {
					continue
				}
				it := itemAgenda{ticker: t, anuncio: an, comFutura: an.DataCom.After(hojeData) || an.DataCom.Equal(hojeData)}
				if it.comFutura {
					it.qtd = pos[t].Quantidade // ainda dá para comprar ou vender até a data-com
				} else {
					it.qtd = quantidadeEm(ops, t, an.DataCom)
				}
				if it.qtd <= 0 {
					continue
				}
				it.bruto = it.qtd * an.Valor
				it.liq = it.bruto
				if strings.Contains(an.Tipo, "JRS") {
					it.liq = it.bruto * (1 - irJCP)
				}
				itens = append(itens, it)
			}
		}
	}
	d.Notas = notas
	if len(itens) == 0 {
		return d, nil
	}
	slices.SortStableFunc(itens, func(x, y itemAgenda) int {
		px, py := x.anuncio.Pagamento, y.anuncio.Pagamento
		switch {
		case px.IsZero() && !py.IsZero():
			return 1
		case !px.IsZero() && py.IsZero():
			return -1
		}
		if c := px.Compare(py); c != 0 {
			return c
		}
		return cmp.Compare(x.ticker, y.ticker)
	})

	var totBruto, totLiq float64
	for _, it := range itens {
		pag := "a definir"
		if !it.anuncio.Pagamento.IsZero() {
			pag = formato.Data(it.anuncio.Pagamento)
		}
		d.Itens = append(d.Itens, linhaAgenda{Pagamento: pag, Ticker: it.ticker, Tipo: nomeProvento(it.anuncio.Tipo),
			DataCom: formato.Data(it.anuncio.DataCom), ComFutura: it.comFutura, Qtd: formato.Quantidade(it.qtd),
			ValorAcao: formato.Numero(it.anuncio.Valor, 4), Bruto: formato.Moeda(it.bruto), Liquido: formato.Moeda(it.liq),
			Referente: it.anuncio.Referente})
		totBruto += it.bruto
		totLiq += it.liq
	}
	d.TotalBruto, d.TotalLiquido = formato.Moeda(totBruto), formato.Moeda(totLiq)
	return d, nil
}

// quantidadeEm: quantidade do ativo no fim do dia d.
func quantidadeEm(ops []carteira.Operacao, ticker string, d time.Time) float64 {
	var ate []carteira.Operacao
	for _, op := range ops {
		if !op.Data.After(d) {
			ate = append(ate, op)
		}
	}
	pos, _ := carteira.CalcularPosicoes(ate)
	if p, ok := pos[ticker]; ok {
		return p.Quantidade
	}
	return 0
}

func nomeProvento(tipo string) string {
	switch tipo {
	case "JRS CAP PROPRIO":
		return "JCP"
	case "DIVIDENDO":
		return "Dividendo"
	case "RENDIMENTO":
		return "Rendimento"
	}
	return strings.ToLower(tipo)
}
