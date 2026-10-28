package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"carteira/internal/armazenamento"
	"carteira/internal/carteira"
	"carteira/internal/fontes/b3"
	"carteira/internal/fontes/brapi"
	"carteira/internal/fontes/coingecko"
	"carteira/internal/fontes/tesouro"
	"carteira/internal/formato"
	"carteira/internal/importacao"
)

// Cotação mais nova que isso é reaproveitada sem consultar o brapi de novo.
const validadeCotacao = 15 * time.Minute

const tetoPadrao = 25.0 // em %

type app struct {
	banco *armazenamento.Banco
	brapi *brapi.Cliente
	saida io.Writer
	agora func() time.Time
	// dirDados é a pasta do banco; os arquivos da CVM ficam em dirDados/cvm
	// e os da B3, enquanto são lidos, em dirDados/b3.
	dirDados      string
	b3            *b3.Cliente
	cripto        *coingecko.Cliente
	tesouro       map[string]tesouro.Preco // preços do Tesouro já lidos nesta execução
	series        map[string]serie         // históricos já lidos nesta execução
	eventosFalhos map[string]error         // empresas cuja busca de eventos falhou nesta execução
}

// ---------- posição ----------

func (a *app) posicao(ctx context.Context) error {
	ops, err := a.banco.Operacoes("")
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		fmt.Fprintln(a.saida, "Carteira vazia. Importe o extrato da B3: carteira importar <movimentacao.xlsx>")
		return nil
	}

	pos, avisos := carteira.CalcularPosicoes(ops)
	// Ativos de terceiros ("carteira ocultar") saem do total, dos pesos e do resultado.
	pos, terceiros, err := a.separarTerceiros(pos)
	if err != nil {
		return err
	}
	var abertos []string
	var realizado float64
	for t, p := range pos {
		realizado += p.Realizado
		if p.Quantidade > 0 {
			abertos = append(abertos, t)
		}
	}
	for t, p := range terceiros {
		if p.Quantidade > 0 {
			abertos = append(abertos, t)
		}
	}
	slices.Sort(abertos)

	cots, notas := a.cotacoes(ctx, abertos)
	precos := map[string]float64{}
	for t, c := range cots {
		precos[t] = c.Preco
	}
	if len(cots) < len(abertos) {
		notas = append(notas, "Ativos sem cotação ficam fora do total e dos pesos.")
	}
	linhas, total := carteira.Pesos(pos, precos)
	teto, err := a.teto()
	if err != nil {
		return err
	}

	fmt.Fprintf(a.saida, "Carteira em %s (valores em R$)\n", a.agora().Format("02/01/2006 15:04"))
	if aviso := a.avisoImportacao(); aviso != "" {
		fmt.Fprintln(a.saida, aviso)
	}
	fmt.Fprintln(a.saida)
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "Ativo\tQtd\tPreço médio\tCotação\tValor\tResultado\t%\tPeso\t")
	var custoCotado float64
	for _, l := range linhas {
		cot, valor, res, resPct, peso, alerta := "-", "-", "-", "-", "-", ""
		if l.Cotacao > 0 {
			custoCotado += l.Custo
			cot = formato.Moeda(l.Cotacao)
			valor = formato.Moeda(l.Valor)
			res = formato.Moeda(l.Valor - l.Custo)
			if l.Custo > 0 {
				resPct = formato.Pct((l.Valor - l.Custo) / l.Custo)
			}
			peso = formato.Pct(l.Peso)
			if l.Peso > teto && !carteira.EhTesouro(l.Ticker) {
				alerta = "  acima do teto de " + formato.Pct(teto)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.Ticker, formato.Quantidade(l.Quantidade),
			formato.Moeda(l.PrecoMedio()), cot, valor, res, resPct, peso, alerta)
	}
	resTotal, resTotalPct := "-", "-"
	if total > 0 {
		resTotal = formato.Moeda(total - custoCotado)
		if custoCotado > 0 {
			resTotalPct = formato.Pct((total - custoCotado) / custoCotado)
		}
	}
	fmt.Fprintf(tw, "Total\t\t\t\t%s\t%s\t%s\t\t\n", formato.Moeda(total), resTotal, resTotalPct)
	if err := tw.Flush(); err != nil {
		return err
	}

	if err := a.imprimirClasses(linhas, total); err != nil {
		return err
	}
	if err := a.imprimirTerceiros(terceiros, precos); err != nil {
		return err
	}

	proventos, err := a.banco.TotalProventos()
	if err != nil {
		return err
	}
	fmt.Fprintf(a.saida, "\nResultado das vendas já feitas: R$ %s\n", formato.Moeda(realizado))
	fmt.Fprintf(a.saida, "Proventos recebidos: R$ %s (detalhe: carteira proventos · a receber: carteira agenda)\n", formato.Moeda(proventos))
	imprimirLista(a.saida, "Cotações:", notas)
	imprimirLista(a.saida, "Confira:", avisos)
	return nil
}

// cotacoes usa a cotação salva se tiver menos de 15 min; senão consulta o brapi,
// um ativo por vez (limite do plano grátis). Se o brapi falhar, usa a última
// cotação salva, avisando de quando ela é.
func (a *app) cotacoes(ctx context.Context, tickers []string) (map[string]armazenamento.Cotacao, []string) {
	res := map[string]armazenamento.Cotacao{}
	var notas []string
	if a.brapi.Token == "" && slices.ContainsFunc(tickers, carteira.EhB3) {
		notas = append(notas, "BRAPI_TOKEN não definido: sem token, o brapi só responde PETR4, MGLU3, VALE3 e ITUB4.")
	}
	agora := a.agora()
	for _, t := range tickers {
		salva, temSalva, err := a.banco.Cotacao(t)
		if err != nil {
			notas = append(notas, fmt.Sprintf("%s: erro ao ler cotação salva: %v", t, err))
		}
		if temSalva && agora.Sub(salva.BuscadoEm) < validadeCotacao {
			res[t] = salva
			continue
		}

		nova, err := a.buscarCotacao(ctx, t)
		if err == nil {
			nova.BuscadoEm = agora
			if err := a.banco.SalvarCotacao(nova); err != nil {
				notas = append(notas, fmt.Sprintf("%s: erro ao salvar cotação: %v", t, err))
			}
			res[t] = nova
			continue
		}
		if temSalva {
			res[t] = salva
			notas = append(notas, fmt.Sprintf("%s: %v; usando a cotação de %s", t, err,
				salva.Horario.Local().Format("02/01/2006 15:04")))
			continue
		}
		notas = append(notas, fmt.Sprintf("%s: sem cotação (%v)", t, err))
	}
	return res, notas
}

// separarTerceiros tira das posições os ativos ocultos (de terceiros).
func (a *app) separarTerceiros(pos map[string]*carteira.Posicao) (minhas, terceiros map[string]*carteira.Posicao, err error) {
	ocultos, err := a.banco.Ocultos()
	if err != nil {
		return nil, nil, err
	}
	minhas, terceiros = map[string]*carteira.Posicao{}, map[string]*carteira.Posicao{}
	for t, p := range pos {
		if ehOculto(ocultos, t) {
			terceiros[t] = p
		} else {
			minhas[t] = p
		}
	}
	return minhas, terceiros, nil
}

// ehOculto: "Tesouro*" oculta todos os títulos do Tesouro.
func ehOculto(ocultos map[string]bool, t string) bool {
	return ocultos[t] || (ocultos[todoTesouro] && carteira.EhTesouro(t))
}

const todoTesouro = "Tesouro*"

func (a *app) imprimirTerceiros(terceiros map[string]*carteira.Posicao, precos map[string]float64) error {
	linhas, total := carteira.Pesos(terceiros, precos)
	if len(linhas) == 0 {
		return nil
	}
	fmt.Fprintln(a.saida, "\nDe terceiros (fora do total, dos pesos e da correlação):")
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', tabwriter.AlignRight)
	for _, l := range linhas {
		valor := "sem cotação"
		if l.Cotacao > 0 {
			valor = formato.Moeda(l.Valor)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t\n", l.Ticker, formato.Quantidade(l.Quantidade), valor)
	}
	if len(linhas) > 1 {
		fmt.Fprintf(tw, "  Total\t\t%s\t\n", formato.Moeda(total))
	}
	return tw.Flush()
}

// ocultar marca ativos como de terceiros; "tesouro" vale para todos os títulos.
func (a *app) ocultar(args []string) error {
	if len(args) == 0 {
		return errors.New(`uso: carteira ocultar <ATIVO>...  (ex.: carteira ocultar tesouro · carteira ocultar "Tesouro Selic 2031")`)
	}
	for _, arg := range args {
		t, ok := nomeOculto(arg)
		if !ok {
			return fmt.Errorf("ativo inválido: %q", arg)
		}
		if err := a.banco.Ocultar(t); err != nil {
			return err
		}
		fmt.Fprintf(a.saida, "%s oculto: aparece à parte, fora do total e dos pesos (desfazer: carteira mostrar %s).\n", descreverOculto(t), arg)
	}
	return nil
}

func (a *app) mostrar(args []string) error {
	if len(args) == 0 {
		return errors.New("uso: carteira mostrar <ATIVO>...")
	}
	for _, arg := range args {
		t, _ := nomeOculto(arg)
		ok, err := a.banco.Mostrar(t)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(a.saida, "%s volta a contar na carteira.\n", descreverOculto(t))
		} else {
			fmt.Fprintf(a.saida, "%s não estava oculto.\n", descreverOculto(t))
		}
	}
	return nil
}

func nomeOculto(arg string) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(arg), "tesouro") {
		return todoTesouro, true
	}
	return carteira.NormalizarAtivo(arg)
}

func descreverOculto(t string) string {
	if t == todoTesouro {
		return "Todo o Tesouro Direto"
	}
	return t
}

// avisoImportacao lembra de importar o extrato quando o mês virou desde a
// última importação. Sem registro (bancos antigos), não avisa.
func (a *app) avisoImportacao() string {
	s, ok, err := a.banco.ConfigTexto("ultima_importacao")
	if err != nil || !ok {
		return ""
	}
	ultima, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	agora := a.agora()
	inicioMes := time.Date(agora.Year(), agora.Month(), 1, 0, 0, 0, 0, agora.Location())
	if !ultima.Before(inicioMes) {
		return ""
	}
	return fmt.Sprintf(">> Mês novo: importe o extrato de Movimentação da B3 (última importação em %s). investidor.b3.com.br > Extratos > Movimentação, depois: carteira importar movimentacao.xlsx",
		ultima.Local().Format("02/01/2006"))
}

func (a *app) teto() (float64, error) {
	pct, err := a.banco.ConfigNumero("teto", tetoPadrao)
	return pct / 100, err
}

// ---------- importação e lançamentos ----------

func (a *app) importar(args []string) error {
	if len(args) != 1 {
		return errors.New("uso: carteira importar <movimentacao.xlsx>")
	}
	res, err := importacao.LerMovimentacao(args[0])
	if err != nil {
		return err
	}
	// Tesouro só entra se estiver ligado ("carteira tesouro sim"): hoje os
	// títulos do extrato são de terceiros. Ligar depois e importar de novo
	// traz tudo, porque as linhas puladas nunca foram gravadas.
	tesouroLigado, err := a.tesouroLigado()
	if err != nil {
		return err
	}
	if !tesouroLigado {
		var pulados int
		res.Operacoes = slices.DeleteFunc(res.Operacoes, func(op carteira.Operacao) bool {
			if carteira.EhTesouro(op.Ticker) {
				pulados++
				return true
			}
			return false
		})
		if pulados > 0 {
			res.Ignorados["Tesouro (desligado: carteira tesouro sim)"] += pulados
		}
	}
	novasOps, novosProvs, err := a.banco.Importar(res.Operacoes, res.Proventos)
	if err != nil {
		return err
	}
	if err := a.banco.SalvarConfigTexto("ultima_importacao", a.agora().Format(time.RFC3339)); err != nil {
		return err
	}

	fmt.Fprintf(a.saida, "Linhas lidas: %d\n", res.Linhas)
	fmt.Fprintf(a.saida, "Operações: %d novas, %d já estavam no banco\n", novasOps, len(res.Operacoes)-novasOps)
	fmt.Fprintf(a.saida, "Proventos: %d novos, %d já estavam no banco\n", novosProvs, len(res.Proventos)-novosProvs)
	if len(res.Ignorados) > 0 {
		var linhas []string
		for _, tipo := range slices.Sorted(maps.Keys(res.Ignorados)) {
			linhas = append(linhas, fmt.Sprintf("%s: %d", tipo, res.Ignorados[tipo]))
		}
		imprimirLista(a.saida, "Linhas não usadas, por tipo:", linhas)
	}
	imprimirLista(a.saida, "Confira:", res.Avisos)
	return nil
}

func (a *app) tesouroLigado() (bool, error) {
	v, err := a.banco.ConfigNumero("importar_tesouro", 0)
	return v == 1, err
}

// tesouro liga ou desliga a importação do Tesouro Direto do extrato.
func (a *app) mudarTesouro(args []string) error {
	if len(args) == 1 {
		var v float64
		switch strings.ToLower(args[0]) {
		case "sim":
			v = 1
		case "nao", "não":
			v = 0
		default:
			return errors.New("uso: carteira tesouro [sim|nao]")
		}
		if err := a.banco.SalvarConfigNumero("importar_tesouro", v); err != nil {
			return err
		}
	} else if len(args) > 1 {
		return errors.New("uso: carteira tesouro [sim|nao]")
	}
	ligado, err := a.tesouroLigado()
	if err != nil {
		return err
	}
	if ligado {
		fmt.Fprintln(a.saida, "Tesouro Direto: ligado. O extrato da B3 traz as compras e vendas; importe de novo para pegar as antigas.")
	} else {
		fmt.Fprintln(a.saida, "Tesouro Direto: desligado. As linhas de Tesouro do extrato são puladas (ligue com: carteira tesouro sim).")
	}
	return nil
}

func (a *app) lancar(tipo carteira.Tipo, args []string) error {
	var qtdTxt, precoTxt, dataTxt string
	switch {
	case tipo == carteira.Ajuste && len(args) == 3:
		qtdTxt, dataTxt = args[1], args[2]
	case tipo != carteira.Ajuste && len(args) == 4:
		qtdTxt, precoTxt, dataTxt = args[1], args[2], args[3]
	case tipo == carteira.Ajuste:
		return errors.New("uso: carteira ajuste <ATIVO> <QTD> <DATA>")
	default:
		return fmt.Errorf("uso: carteira %s <ATIVO> <QTD> <PREÇO> <DATA>", tipo)
	}

	ticker, _ := carteira.NormalizarAtivo(args[0])
	qtd, err := formato.LerNumero(qtdTxt)
	if err != nil {
		return fmt.Errorf("quantidade inválida %q", qtdTxt)
	}
	var preco float64
	if precoTxt != "" {
		if preco, err = formato.LerNumero(precoTxt); err != nil {
			return fmt.Errorf("preço inválido %q", precoTxt)
		}
	}
	data, err := formato.LerData(dataTxt)
	if err != nil {
		return fmt.Errorf("data inválida %q (use dd/mm/aaaa)", dataTxt)
	}

	op := carteira.Operacao{Data: data, Ticker: ticker, Tipo: tipo, Quantidade: qtd, Preco: preco, Descricao: "manual", Origem: "manual"}
	if err := op.Validar(); err != nil {
		return err
	}
	id, err := a.banco.InserirOperacao(op)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.saida, "Operação #%d gravada: %s %s %s em %s\n", id, tipo, formato.Quantidade(qtd), ticker, formato.Data(data))
	return nil
}

func (a *app) operacoes(args []string) error {
	ticker := ""
	if len(args) > 0 {
		ticker, _ = carteira.NormalizarAtivo(args[0])
	}
	ops, err := a.banco.Operacoes(ticker)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		fmt.Fprintln(a.saida, "Nenhuma operação.")
		return nil
	}
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tData\tAtivo\tTipo\tQtd\tPreço\tOrigem\tMovimentação")
	for _, op := range ops {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", op.ID, formato.Data(op.Data), op.Ticker, op.Tipo,
			formato.Quantidade(op.Quantidade), formato.Moeda(op.Preco), op.Origem, op.Descricao)
	}
	return tw.Flush()
}

func (a *app) remover(args []string) error {
	if len(args) != 1 {
		return errors.New("uso: carteira remover <ID>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("ID inválido %q", args[0])
	}
	ok, err := a.banco.RemoverOperacao(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("operação #%d não existe", id)
	}
	fmt.Fprintf(a.saida, "Operação #%d removida.\n", id)
	return nil
}

// ---------- lista de observação ----------

func (a *app) observar(args []string) error {
	if len(args) == 0 {
		return errors.New("uso: carteira observar <ATIVO>...")
	}
	for _, arg := range args {
		t, ok := carteira.NormalizarTicker(arg)
		if !ok {
			return fmt.Errorf("código de ativo inválido: %q", arg)
		}
		if err := a.banco.Observar(t); err != nil {
			return err
		}
		fmt.Fprintf(a.saida, "%s na lista de observação.\n", t)
	}
	return nil
}

func (a *app) desobservar(args []string) error {
	if len(args) == 0 {
		return errors.New("uso: carteira desobservar <ATIVO>...")
	}
	for _, arg := range args {
		t, _ := carteira.NormalizarTicker(arg)
		ok, err := a.banco.Desobservar(t)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(a.saida, "%s saiu da lista de observação.\n", t)
		} else {
			fmt.Fprintf(a.saida, "%s não estava na lista.\n", t)
		}
	}
	return nil
}

func (a *app) observacao(ctx context.Context) error {
	tickers, err := a.banco.Observacao()
	if err != nil {
		return err
	}
	if len(tickers) == 0 {
		fmt.Fprintln(a.saida, "Lista de observação vazia. Ex.: carteira observar GRND3 SAPR11 WEGE3")
		return nil
	}
	cots, notas := a.cotacoes(ctx, tickers)
	tw := tabwriter.NewWriter(a.saida, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Ativo\tCotação (R$)\tHorário")
	for _, t := range tickers {
		c, ok := cots[t]
		if !ok {
			fmt.Fprintf(tw, "%s\t-\t-\n", t)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t, formato.Moeda(c.Preco), c.Horario.Local().Format("02/01 15:04"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	imprimirLista(a.saida, "Cotações:", notas)
	return nil
}

// ---------- teto ----------

func (a *app) mudarTeto(args []string) error {
	if len(args) == 0 {
		t, err := a.teto()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.saida, "Teto por ativo: %s\n", formato.Pct(t))
		return nil
	}
	pct, err := formato.LerNumero(args[0])
	if err != nil || pct <= 0 || pct > 100 {
		return fmt.Errorf("teto inválido %q: use um percentual entre 0 e 100, ex.: carteira teto 25", args[0])
	}
	if err := a.banco.SalvarConfigNumero("teto", pct); err != nil {
		return err
	}
	fmt.Fprintf(a.saida, "Teto por ativo agora é %s.\n", formato.Pct(pct/100))
	return nil
}

func imprimirLista(w io.Writer, titulo string, itens []string) {
	if len(itens) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", titulo)
	for _, s := range itens {
		fmt.Fprintf(w, "  - %s\n", s)
	}
}
