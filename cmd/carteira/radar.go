package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"carteira/internal/fontes/cvm"
	"carteira/internal/formato"
	"carteira/internal/indicadores"
	"carteira/internal/radar"
)

// Ação negociada em menos que isso dos pregões do último ano fica fora do
// radar: sem liquidez, o preço não é confiável e é difícil comprar ou vender.
const liquidezMinima = 0.9

// Quantas ações o radar mostra sem "todas".
const radarTopo = 30

// ---------- carteira filtro ----------

// itemFiltro liga o nome usado no comando à chave no banco.
type itemFiltro struct {
	nome, descricao string
	pct             bool // guardado como fração, mostrado em %
	campo           func(*radar.Limites) *float64
}

var itensFiltro = []itemFiltro{
	{"pl", "P/L máximo (12 meses)", false, func(l *radar.Limites) *float64 { return &l.PLMax }},
	{"roe", "ROE mínimo (média de 5 anos)", true, func(l *radar.Limites) *float64 { return &l.ROEMin }},
	{"roic", "ROIC mínimo (média de 5 anos; não vale para bancos)", true, func(l *radar.Limites) *float64 { return &l.ROICMin }},
	{"beta", "Beta máximo (semanal, 2 anos)", false, func(l *radar.Limites) *float64 { return &l.BetaMax }},
	{"correlacao", "Correlação máxima com a carteira (semanal, 2 anos)", false, func(l *radar.Limites) *float64 { return &l.CorrelMax }},
}

func (a *app) limites() (radar.Limites, error) {
	lim := radar.Padrao
	for _, it := range itensFiltro {
		p := it.campo(&lim)
		v, err := a.banco.ConfigNumero("filtro_"+it.nome, *p)
		if err != nil {
			return lim, err
		}
		*p = v
	}
	return lim, nil
}

func (a *app) filtro(args []string) error {
	if len(args) == 2 {
		v, err := formato.LerNumero(args[1])
		if err != nil {
			return fmt.Errorf("valor inválido %q", args[1])
		}
		i := slices.IndexFunc(itensFiltro, func(it itemFiltro) bool { return it.nome == strings.ToLower(args[0]) })
		if i < 0 {
			return fmt.Errorf("critério desconhecido %q", args[0])
		}
		if itensFiltro[i].pct {
			v /= 100 // o usuário digita 12 para 12%
		}
		if err := a.banco.SalvarConfigNumero("filtro_"+itensFiltro[i].nome, v); err != nil {
			return err
		}
	} else if len(args) != 0 {
		return errors.New("uso: carteira filtro [CRITÉRIO VALOR]  (ex.: carteira filtro roe 15)")
	}
	lim, err := a.limites()
	if err != nil {
		return err
	}
	fmt.Fprintln(a.saida, "Filtro do radar:")
	for _, it := range itensFiltro {
		v := *it.campo(&lim)
		txt := formato.Numero(v, 2)
		if it.pct {
			txt = formato.Pct(v)
		}
		fmt.Fprintf(a.saida, "  %-11s %-8s %s\n", it.nome, txt, it.descricao)
	}
	fmt.Fprintln(a.saida, "  e lucro em todos os anos (5 anos de balanço).")
	fmt.Fprintln(a.saida, "Para mudar: carteira filtro <critério> <valor>  (ex.: carteira filtro roe 15)")
	return nil
}

// ---------- carteira radar ----------

type candidato struct {
	tk   cvm.Ticker
	dias int // pregões com negócio no último ano
}

// itemRadar é uma ação avaliada pelo filtro.
type itemRadar struct {
	Ticker     string          `json:"ticker"`
	Nome       string          `json:"nome"`
	NaCarteira bool            `json:"naCarteira"`
	Cumpridos  int             `json:"cumpridos"`
	Total      int             `json:"total"`
	Criterios  []criterioRadar `json:"criterios"`
	EVEbitda   string          `json:"evEbitda"` // só informativo, fora da nota
	Obs        string          `json:"obs"`
}

type criterioRadar struct {
	Nome      string `json:"nome"`
	Valor     string `json:"valor"`
	OK        bool   `json:"ok"`
	NaoAplica bool   `json:"naoAplica"`
}

type dadosRadar struct {
	Data        string      `json:"data"`
	Analisadas  int         `json:"analisadas"`
	SemLiquidez int         `json:"semLiquidez"`
	PrecosAte   string      `json:"precosAte"`
	Criterios   []string    `json:"criterios"`
	Itens       []itemRadar `json:"itens"` // todas, da melhor para a pior
	Notas       []string    `json:"notas"`
}

func (a *app) radar(ctx context.Context, args []string) error {
	todas := len(args) == 1 && strings.EqualFold(args[0], "todas")
	if len(args) > 0 && !todas {
		return errors.New("uso: carteira radar [todas]")
	}
	d, err := a.dadosRadar(ctx)
	if err != nil {
		imprimirLista(a.saida, "Observações:", d.Notas)
		return err
	}
	mostrar := d.Itens
	if !todas && len(mostrar) > radarTopo {
		mostrar = mostrar[:radarTopo]
	}
	fmt.Fprintf(a.saida, "Radar em %s · %d empresas analisadas (%d códigos sem liquidez ficaram fora) · preços até %s\n\n",
		d.Data, d.Analisadas, d.SemLiquidez, d.PrecosAte)
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', 0)
	cab := append([]string{"Ativo", "Nota"}, d.Criterios...)
	fmt.Fprintln(tw, strings.Join(append(cab, "EV/EBITDA", "Obs."), "\t"))
	for _, it := range mostrar {
		nome := it.Ticker
		if it.NaCarteira {
			nome = "* " + nome
		}
		linha := []string{nome, fmt.Sprintf("%d/%d", it.Cumpridos, it.Total)}
		for _, c := range it.Criterios {
			marca := " ✗"
			switch {
			case c.NaoAplica:
				marca = ""
			case c.OK:
				marca = " ✓"
			}
			linha = append(linha, c.Valor+marca)
		}
		linha = append(linha, it.EVEbitda, it.Obs)
		fmt.Fprintln(tw, strings.Join(linha, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if !todas && len(d.Itens) > radarTopo {
		fmt.Fprintf(a.saida, "\nMostrando as %d melhores de %d. Para ver todas: carteira radar todas\n", radarTopo, len(d.Itens))
	}
	fmt.Fprintln(a.saida, "\n* = já na carteira · nenhum critério elimina sozinho: a nota é quantos o ativo cumpre · n/a = não se aplica (ROIC e EV/EBITDA de banco) · EV/EBITDA é só informativo")
	fmt.Fprintln(a.saida, "P/L com o fechamento do último pregão do COTAHIST · limites: carteira filtro · conta de um número: carteira explicar <ATIVO> <indicador>")
	return nil
}

// dadosRadar avalia pelo filtro todas as ações com liquidez.
func (a *app) dadosRadar(ctx context.Context) (dadosRadar, error) {
	d := dadosRadar{Data: formato.Data(a.agora())}
	lim, err := a.limites()
	if err != nil {
		return d, err
	}
	if err := a.garantirCVM(ctx); err != nil {
		return d, err
	}
	m, notas, ok := a.prepararMercado(ctx)
	d.Notas = notas
	if !ok {
		return d, errors.New("o radar precisa do histórico de preços")
	}

	// Pregões do último ano, pelo BOVA11.
	umAno := m.ate.AddDate(-1, 0, 0)
	pregoes := 0
	for _, p := range m.indice.bruto {
		if p.Data.After(umAno) {
			pregoes++
		}
	}

	// Um código por empresa: o negociado em mais pregões no último ano.
	tks, err := a.banco.TickersCVM()
	if err != nil {
		return d, err
	}
	porEmpresa := map[string]candidato{}
	semLiquidez := 0
	for _, tk := range tks {
		ps, err := a.banco.Precos(tk.Codigo)
		if err != nil {
			return d, err
		}
		dias := 0
		for _, p := range ps {
			if p.Data.After(umAno) {
				dias++
			}
		}
		if float64(dias) < liquidezMinima*float64(pregoes) {
			semLiquidez++
			continue
		}
		atual, existe := porEmpresa[tk.CNPJ]
		if !existe || dias > atual.dias || (dias == atual.dias && preferencia(tk.Codigo) > preferencia(atual.tk.Codigo)) {
			porEmpresa[tk.CNPJ] = candidato{tk, dias}
		}
	}
	var escolhidos []candidato
	var empresas []string
	for _, c := range porEmpresa {
		escolhidos = append(escolhidos, c)
		empresas = append(empresas, c.tk.Codigo[:4])
	}
	slices.Sort(empresas)
	empresas = slices.Compact(empresas)
	a.adiantarEventos(ctx, empresas)

	carteiraHoje, err := a.posicoesAbertas()
	if err != nil {
		return d, err
	}
	var avs []radar.Avaliacao
	for _, c := range escolhidos {
		contas, comps, err := a.banco.Balancos(c.tk.CNPJ)
		if err != nil {
			return d, err
		}
		h, _ := a.historico(ctx, c.tk.Codigo, m)
		if len(h.Precos) == 0 {
			continue
		}
		ultimo := h.Precos[len(h.Precos)-1]
		cot := indicadores.Cotacao{Preco: ultimo.Valor, Fonte: "COTAHIST (B3), fechamento de " + formato.Data(ultimo.Data)}
		r := indicadores.Calcular(c.tk, contas, comps, cot, h)
		if len(r.Colunas) == 0 {
			continue // sem balanço anual
		}
		avs = append(avs, radar.Avaliar(r, lim))
	}
	slices.SortFunc(avs, func(x, y radar.Avaliacao) int {
		if c := cmp.Compare(y.Nota(), x.Nota()); c != 0 {
			return c
		}
		return cmp.Compare(plOrdem(x.PL), plOrdem(y.PL))
	})

	if len(avs) == 0 {
		return d, errors.New("nenhuma empresa com balanço e preços para avaliar")
	}
	d.Analisadas, d.SemLiquidez, d.PrecosAte = len(avs), semLiquidez, formato.Data(m.ate)
	for _, c := range avs[0].Criterios {
		d.Criterios = append(d.Criterios, c.Nome)
	}
	for _, av := range avs {
		it := itemRadar{Ticker: av.Ticker, Nome: av.Nome, NaCarteira: carteiraHoje[av.Ticker], Cumpridos: av.Cumpridos, Total: av.Total, EVEbitda: av.EVEbitda}
		for _, c := range av.Criterios {
			it.Criterios = append(it.Criterios, criterioRadar{Nome: c.Nome, Valor: c.Valor, OK: c.OK, NaoAplica: c.NaoAplica})
		}
		var obs []string
		if av.HistoricoCurto {
			obs = append(obs, "histórico curto")
		}
		if len(av.Trimestres) > 0 {
			obs = append(obs, "prejuízo em "+strings.Join(av.Trimestres, ", "))
		}
		it.Obs = strings.Join(obs, "; ")
		d.Itens = append(d.Itens, it)
	}
	return d, nil
}

// preferencia desempata códigos da mesma empresa: unit, depois PN, depois ON.
func preferencia(codigo string) int {
	switch {
	case strings.HasSuffix(codigo, "11"):
		return 3
	case strings.HasSuffix(codigo, "4"):
		return 2
	}
	return 1
}

// plOrdem põe P/L negativo ou sem dado no fim.
func plOrdem(pl float64) float64 {
	if pl <= 0 {
		return 1e9
	}
	return pl
}
