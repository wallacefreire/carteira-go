package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/fontes/cvm"
	"carteira/internal/formato"
	"carteira/internal/indicadores"
)

// Com dados da CVM mais velhos que isso, "indicadores" atualiza sozinho antes.
const validadeCVM = 7 * 24 * time.Hour

// ---------- carteira cvm ----------

// atualizarCVM baixa os arquivos da CVM que faltam ou estão velhos e recarrega o banco.
func (a *app) atualizarCVM(ctx context.Context) error {
	agora := a.agora()
	dir := filepath.Join(a.dirDados, "cvm")
	arqs := cvm.Necessarios(agora)
	cli := &http.Client{Timeout: 10 * time.Minute}
	caminhos, err := cvm.Baixar(ctx, cli, dir, arqs, agora, func(s string) { fmt.Fprintln(a.saida, s+"…") })
	if err != nil {
		return err
	}

	// FCA do ano anterior primeiro: o do ano atual sobrescreve o que mudou.
	for _, arq := range arqs {
		caminho, ok := caminhos[arq]
		if !ok {
			continue
		}
		if arq.Tipo == "FCA" {
			tickers, err := cvm.LerTickers(caminho)
			if err != nil {
				return err
			}
			if err := a.banco.SalvarTickers(tickers); err != nil {
				return err
			}
			continue
		}
		contas, comps, err := cvm.LerBalancos(caminho)
		if err != nil {
			return err
		}
		if err := a.banco.SubstituirBalancos(arq.Nome(), contas, comps); err != nil {
			return fmt.Errorf("gravar %s: %w", arq.Nome(), err)
		}
	}
	if err := a.banco.SalvarConfigTexto("cvm_atualizado_em", agora.Format(time.RFC3339)); err != nil {
		return err
	}
	fmt.Fprintf(a.saida, "Dados da CVM atualizados (%d arquivos em %s).\n", len(caminhos), dir)
	return nil
}

func (a *app) garantirCVM(ctx context.Context) error {
	s, ok, err := a.banco.ConfigTexto("cvm_atualizado_em")
	if err != nil {
		return err
	}
	if ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil && a.agora().Sub(t) < validadeCVM {
			return nil
		}
	}
	fmt.Fprintln(a.saida, "Atualizando os dados da CVM (pode levar 1 ou 2 minutos)…")
	return a.atualizarCVM(ctx)
}

// ---------- carteira indicadores ----------

// tickersPadrao: posições abertas da carteira + lista de observação.
func (a *app) tickersPadrao() ([]string, error) {
	ops, err := a.banco.Operacoes("")
	if err != nil {
		return nil, err
	}
	pos, _ := carteira.CalcularPosicoes(ops)
	var l []string
	for t, p := range pos {
		if p.Quantidade > 0 && carteira.EhB3(t) { // Tesouro e BTC não têm balanço
			l = append(l, t)
		}
	}
	slices.Sort(l)
	obs, err := a.banco.Observacao()
	if err != nil {
		return nil, err
	}
	for _, t := range obs {
		if !slices.Contains(l, t) {
			l = append(l, t)
		}
	}
	return l, nil
}

func (a *app) calcularIndicadores(ctx context.Context, tickers []string) ([]indicadores.Resultado, []string, error) {
	if err := a.garantirCVM(ctx); err != nil {
		return nil, nil, err
	}
	cots, notas := a.cotacoes(ctx, tickers)
	hists, n := a.historicos(ctx, tickers)
	notas = append(notas, n...)
	var res []indicadores.Resultado
	for _, t := range tickers {
		tk, ok, err := a.banco.TickerCVM(t)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			notas = append(notas, t+": não é ação de empresa listada na CVM (FII, ETF e BDR não têm esses indicadores)")
			continue
		}
		contas, comps, err := a.banco.Balancos(tk.CNPJ)
		if err != nil {
			return nil, nil, err
		}
		c := cots[t]
		res = append(res, indicadores.Calcular(tk, contas, comps, indicadores.Cotacao{Preco: c.Preco, Horario: c.Horario, Fonte: c.Fonte}, hists[t]))
	}
	return res, notas, nil
}

func (a *app) indicadores(ctx context.Context, args []string) error {
	tickers, err := a.listaTickers(args)
	if err != nil {
		return err
	}
	if len(tickers) == 0 {
		return errors.New("nenhum ativo: informe um código (ex.: carteira indicadores SOJA3) ou importe a carteira")
	}
	res, notas, err := a.calcularIndicadores(ctx, tickers)
	if err != nil {
		return err
	}
	for i, r := range res {
		if i > 0 {
			fmt.Fprintln(a.saida)
		}
		if err := a.imprimirIndicadores(r); err != nil {
			return err
		}
	}
	imprimirLista(a.saida, "Observações:", notas)
	fmt.Fprintln(a.saida, "\nPara ver a conta de um indicador: carteira explicar <ATIVO> <indicador> ("+chavesIndicadores()+")")
	return nil
}

func (a *app) listaTickers(args []string) ([]string, error) {
	if len(args) == 0 {
		return a.tickersPadrao()
	}
	var l []string
	for _, arg := range args {
		t, ok := carteira.NormalizarTicker(arg)
		if !ok {
			return nil, fmt.Errorf("código de ativo inválido: %q", arg)
		}
		l = append(l, t)
	}
	return l, nil
}

func (a *app) imprimirIndicadores(r indicadores.Resultado) error {
	fmt.Fprintf(a.saida, "%s · %s", r.Ticker, r.Nome)
	if !r.UltimoBalanco.IsZero() {
		fmt.Fprintf(a.saida, " · último balanço: %s", formato.Data(r.UltimoBalanco))
	}
	if r.Financeira {
		fmt.Fprint(a.saida, " · banco/seguradora")
	}
	fmt.Fprintln(a.saida)
	if len(r.Colunas) == 0 {
		imprimirLista(a.saida, "Avisos:", r.Avisos)
		return nil
	}

	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	cab := []string{""}
	for _, c := range r.Colunas {
		cab = append(cab, c.Rotulo)
	}
	fmt.Fprintln(tw, strings.Join(cab, "\t")+"\t")
	for _, d := range indicadores.Lista {
		linha := []string{d.Nome}
		if d.Unidade == "R$" {
			linha[0] += " (R$ mi)"
		}
		for _, c := range r.Colunas {
			linha = append(linha, formatarValor(c.Valores[d.Chave], d.Unidade))
		}
		fmt.Fprintln(tw, strings.Join(linha, "\t")+"\t")
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(a.saida, "n/a = não se aplica a bancos e seguradoras · \"-\" = sem dado · beta, volatilidade e correlação usam preços da B3 ajustados por proventos")
	imprimirLista(a.saida, "Avisos:", r.Avisos)
	return nil
}

func formatarValor(v indicadores.Valor, unidade string) string {
	if !v.OK {
		if strings.HasPrefix(v.Motivo, "não se aplica") {
			return "n/a"
		}
		return "-"
	}
	switch unidade {
	case "R$":
		return formato.Numero(v.Numero/1e6, 1)
	case "%":
		return formato.Pct(v.Numero)
	default:
		return formato.Numero(v.Numero, 2)
	}
}

// ---------- carteira explicar ----------

func (a *app) explicar(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("uso: carteira explicar <ATIVO> <indicador>  (" + chavesIndicadores() + ")")
	}
	t, ok := carteira.NormalizarTicker(args[0])
	if !ok {
		return fmt.Errorf("código de ativo inválido: %q", args[0])
	}
	def, ok := indicadores.Buscar(strings.ToLower(args[1]))
	if !ok {
		return fmt.Errorf("indicador desconhecido %q: use %s", args[1], chavesIndicadores())
	}
	res, notas, err := a.calcularIndicadores(ctx, []string{t})
	if err != nil {
		return err
	}
	if len(res) == 0 {
		imprimirLista(a.saida, "Observações:", notas)
		return nil
	}
	r := res[0]
	atualizado, _, _ := a.banco.ConfigTexto("cvm_atualizado_em")
	if tm, err := time.Parse(time.RFC3339, atualizado); err == nil {
		atualizado = tm.Local().Format("02/01/2006 15:04")
	}

	fmt.Fprintf(a.saida, "%s · %s\n\n", r.Ticker, def.Nome)
	fmt.Fprintf(a.saida, "Fórmula: %s\n", def.Formula)
	fmt.Fprintf(a.saida, "Como ler: %s\n", def.ComoLer)
	switch def.Chave {
	case "beta", "volatilidade", "correlacao":
		fmt.Fprintf(a.saida, "Fonte: B3, histórico de preços (COTAHIST) até %s, ajustado pelos proventos e eventos do site da B3\n", precosAte(a))
	case "pl":
		fmt.Fprintf(a.saida, "Fonte: CVM (DFP e ITR), atualizados em %s · preços: B3 (COTAHIST) nos anos, brapi em 12m\n", atualizado)
	default:
		fmt.Fprintf(a.saida, "Fonte: CVM, dados abertos (DFP e ITR), atualizados em %s\n", atualizado)
	}

	for _, c := range r.Colunas {
		v := c.Valores[def.Chave]
		fmt.Fprintf(a.saida, "\n%s (%s): ", c.Rotulo, c.Periodo)
		if !v.OK {
			fmt.Fprintf(a.saida, "sem valor (%s)\n", v.Motivo)
			continue
		}
		fmt.Fprintln(a.saida, formatarValorLongo(v.Numero, def.Unidade))
		for _, p := range v.Partes {
			valor := formato.Quantidade(p.Valor)
			switch {
			case p.Unidade != "":
				valor = formatarValorLongo(p.Valor, p.Unidade)
			case p.Moeda:
				valor = "R$ " + formato.Moeda(p.Valor)
			}
			fmt.Fprintf(a.saida, "  %s: %s", p.Nome, valor)
			if p.Origem != "" {
				fmt.Fprintf(a.saida, "  [%s]", p.Origem)
			}
			fmt.Fprintln(a.saida)
		}
		if v.Nota != "" {
			fmt.Fprintf(a.saida, "  Obs.: %s\n", v.Nota)
		}
	}
	imprimirLista(a.saida, "Avisos:", r.Avisos)
	return nil
}

func chavesIndicadores() string {
	var l []string
	for _, d := range indicadores.Lista {
		l = append(l, d.Chave)
	}
	return strings.Join(l, ", ")
}

func precosAte(a *app) string {
	s, _, _ := a.banco.ConfigTexto("precos_ate")
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return formato.Data(t)
	}
	return "?"
}

func formatarValorLongo(v float64, unidade string) string {
	switch unidade {
	case "R$":
		return "R$ " + formato.Numero(v/1e6, 1) + " mi"
	case "%":
		return formato.Pct(v)
	default:
		return formato.Numero(v, 2)
	}
}
