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
	"strings"
	"sync"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/fontes/b3"
	"carteira/internal/fontes/baixar"
	"carteira/internal/formato"
	"carteira/internal/indicadores"
	"carteira/internal/risco"
)

const (
	// Preços e eventos mais velhos que isso são atualizados antes dos indicadores.
	validadePrecos = 7 * 24 * time.Hour
	// O Ibovespa não está no COTAHIST; o BOVA11 é o ETF que o replica.
	indiceMercado = "BOVA11"
	// Anos completos de histórico, além do ano atual.
	anosPrecos = 5
)

// ---------- carteira precos ----------

// atualizarPrecos baixa o COTAHIST de cada ano que falta, grava os preços no
// banco e apaga o .zip. Ano fechado é baixado uma vez só; o ano atual, de
// novo quando passa de 7 dias (ou sempre, com forcar).
func (a *app) atualizarPrecos(ctx context.Context, forcar bool) error {
	agora := a.agora()
	dir := filepath.Join(a.dirDados, "b3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cli := &http.Client{Timeout: 15 * time.Minute}
	for ano := agora.Year() - anosPrecos; ano <= agora.Year(); ano++ {
		chave := fmt.Sprintf("cotahist_%d", ano)
		feito, ok, err := a.banco.ConfigTexto(chave)
		if err != nil {
			return err
		}
		if ok && feito == "completo" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, feito); ok && err == nil && !forcar && agora.Sub(t) < validadePrecos {
			continue
		}

		nome := b3.NomeCotahist(ano)
		caminho := filepath.Join(dir, nome)
		baixadoEm := agora
		if info, err := os.Stat(caminho); err == nil && !forcar && agora.Sub(info.ModTime()) < validadePrecos {
			baixadoEm = info.ModTime() // .zip já baixado e recente (ex.: execução interrompida)
		} else {
			fmt.Fprintf(a.saida, "baixando %s da B3 (até 90 MB)…\n", nome)
			if err := baixar.Arquivo(ctx, cli, b3.URLCotahist(ano), caminho); err != nil {
				if errors.Is(err, baixar.ErrNaoExiste) && ano == agora.Year() {
					continue // começo de janeiro: o arquivo do ano ainda não saiu
				}
				return fmt.Errorf("baixar %s: %w", nome, err)
			}
		}
		fmt.Fprintf(a.saida, "lendo %s…\n", nome)
		precos, err := b3.LerCotahist(caminho)
		if err != nil {
			return fmt.Errorf("ler %s: %w", nome, err)
		}
		if err := a.banco.SubstituirPrecos(ano, precos); err != nil {
			return fmt.Errorf("gravar %s: %w", nome, err)
		}
		marca := baixadoEm.Format(time.RFC3339)
		if baixadoEm.Year() > ano {
			marca = "completo" // baixado depois do fim do ano: não muda mais
		}
		if err := a.banco.SalvarConfigTexto(chave, marca); err != nil {
			return err
		}
		// Os preços já estão no banco; o .zip não é mais usado.
		if err := os.Remove(caminho); err != nil {
			fmt.Fprintf(a.saida, "aviso: não consegui apagar %s: %v\n", caminho, err)
		}
	}
	return nil
}

// ---------- eventos (proventos e desdobramentos) ----------

// eventos devolve os eventos de uma empresa, do banco ou do site da B3.
func (a *app) eventos(ctx context.Context, empresa string) ([]b3.Evento, string) {
	chave := "b3_eventos_" + empresa
	s, salvo, _ := a.banco.ConfigTexto(chave)
	if t, err := time.Parse(time.RFC3339, s); salvo && err == nil && a.agora().Sub(t) < validadePrecos {
		if evs, err := a.banco.Eventos(empresa); err == nil {
			return evs, ""
		}
	}
	err := a.eventosFalhos[empresa]
	var evs []b3.Evento
	if err == nil {
		evs, err = a.b3.Eventos(ctx, empresa)
	}
	if err != nil {
		if salvo {
			antigos, _ := a.banco.Eventos(empresa)
			return antigos, fmt.Sprintf("%v; usados os eventos salvos antes", err)
		}
		return nil, fmt.Sprintf("%v; preços sem ajuste por proventos e desdobramentos", err)
	}
	if err := a.banco.SubstituirEventos(empresa, evs); err != nil {
		return evs, fmt.Sprintf("erro ao salvar eventos de %s: %v", empresa, err)
	}
	if err := a.banco.SalvarConfigTexto(chave, a.agora().Format(time.RFC3339)); err != nil {
		return evs, err.Error()
	}
	return evs, ""
}

// serie é o histórico de um ativo, sem ajuste e ajustado.
type serie struct {
	bruto, ajustado []risco.Ponto
	ajustes         []risco.Ajuste
	nota            string
}

// serie lê os preços do banco e ajusta pelos eventos do mesmo ISIN (ON, PN
// e unit da mesma empresa têm ISINs e proventos diferentes). Guarda o
// resultado para não repetir a conta na mesma execução.
func (a *app) serie(ctx context.Context, ticker string) (serie, error) {
	if s, ok := a.series[ticker]; ok {
		return s, nil
	}
	ps, err := a.banco.Precos(ticker)
	if err != nil {
		return serie{}, err
	}
	var s serie
	if len(ps) > 0 {
		isin := ps[len(ps)-1].ISIN
		especie := b3.EspecieDoTicker(ticker)
		evs, nota := a.eventos(ctx, ticker[:4])
		if nota != "" {
			s.nota = ticker + ": " + nota
		}
		var revs []risco.Evento
		for _, e := range evs {
			// Provento vem por espécie (ON, PN, UNT); evento em ações, por ISIN.
			if (e.Dinheiro && e.Especie != especie) || (!e.Dinheiro && e.ISIN != isin) {
				continue
			}
			if e.Dinheiro {
				revs = append(revs, risco.Evento{DataCom: e.DataCom, Provento: e.Valor,
					Descricao: fmt.Sprintf("%s de R$ %s com data-com %s", strings.ToLower(e.Tipo), formato.Quantidade(e.Valor), formato.Data(e.DataCom))})
			} else if f, ok := e.FatorAcoes(); ok {
				revs = append(revs, risco.Evento{DataCom: e.DataCom, FatorAcoes: f, Descricao: descreverEvento(e, f)})
			}
		}
		for _, p := range ps {
			s.bruto = append(s.bruto, risco.Ponto{Data: p.Data, Valor: p.Fechamento})
		}
		s.ajustado, s.ajustes = risco.Ajustar(s.bruto, revs)
	}
	if a.series == nil {
		a.series = map[string]serie{}
	}
	a.series[ticker] = s
	return s, nil
}

func descreverEvento(e b3.Evento, fator float64) string {
	data := formato.Data(e.DataCom)
	switch e.Tipo {
	case "DESDOBRAMENTO":
		return fmt.Sprintf("desdobramento em %s (cada ação virou %s)", data, formato.Quantidade(fator))
	case "GRUPAMENTO":
		return fmt.Sprintf("grupamento em %s (%s ações viraram 1)", data, formato.Quantidade(1/fator))
	default:
		return fmt.Sprintf("bonificação de %s%% em %s", formato.Quantidade(e.Valor), data)
	}
}

// ---------- carteira para a correlação ----------

// semanasCarteira guarda os retornos semanais e os pesos de hoje de cada ativo da carteira.
type semanasCarteira struct {
	retornos map[string][]risco.Ponto
	pesos    map[string]float64 // valor em R$ de hoje
}

func (a *app) semanasCarteira(ctx context.Context, ate time.Time) (semanasCarteira, []string, error) {
	sc := semanasCarteira{retornos: map[string][]risco.Ponto{}, pesos: map[string]float64{}}
	ops, err := a.banco.Operacoes("")
	if err != nil {
		return sc, nil, err
	}
	pos, _ := carteira.CalcularPosicoes(ops)
	pos, _, err = a.separarTerceiros(pos)
	if err != nil {
		return sc, nil, err
	}
	var abertos []string
	for t, p := range pos {
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
	linhas, _ := carteira.Pesos(pos, precos)
	for _, l := range linhas {
		if l.Valor <= 0 {
			continue
		}
		s, err := a.serie(ctx, l.Ticker)
		if err != nil {
			return sc, nil, err
		}
		if len(s.ajustado) == 0 {
			notas = append(notas, l.Ticker+": sem histórico na B3; fica fora da correlação com a carteira")
			continue
		}
		sc.retornos[l.Ticker] = risco.Retornos(risco.Semanal(risco.Entre(s.ajustado, ate.AddDate(-2, 0, 0), ate)))
		sc.pesos[l.Ticker] = l.Valor
	}
	return sc, notas, nil
}

// semAtivo devolve os retornos da carteira sem o ativo e a descrição dos pesos.
func (sc semanasCarteira) semAtivo(ticker string) ([]risco.Ponto, string) {
	pesos := maps.Clone(sc.pesos)
	delete(pesos, ticker)
	var total float64
	for _, v := range pesos {
		total += v
	}
	if total <= 0 {
		return nil, ""
	}
	tickers := slices.Collect(maps.Keys(pesos))
	slices.SortFunc(tickers, func(x, y string) int { return cmp.Compare(pesos[y], pesos[x]) })
	var partes []string
	for _, t := range tickers {
		partes = append(partes, fmt.Sprintf("%s %s", t, formato.Pct(pesos[t]/total)))
	}
	return risco.RetornoCarteira(sc.retornos, pesos), strings.Join(partes, ", ")
}

// mercado é o que todos os ativos usam no cálculo de risco: o BOVA11, o
// último pregão e os retornos da carteira.
type mercado struct {
	indice serie
	ate    time.Time
	cart   semanasCarteira
}

// prepararMercado atualiza os preços e monta o BOVA11 e a carteira. Erro no
// download não impede os indicadores dos balanços: vira observação.
func (a *app) prepararMercado(ctx context.Context) (mercado, []string, bool) {
	if err := a.atualizarPrecos(ctx, false); err != nil {
		return mercado{}, []string{"histórico de preços: " + err.Error() + "; P/L por ano, beta, volatilidade e correlação ficam sem valor"}, false
	}
	idx, err := a.serie(ctx, indiceMercado)
	if err != nil || len(idx.ajustado) == 0 {
		return mercado{}, []string{"sem o histórico do BOVA11: beta, volatilidade e correlação ficam sem valor"}, false
	}
	m := mercado{indice: idx, ate: idx.ajustado[len(idx.ajustado)-1].Data}
	cart, notas, err := a.semanasCarteira(ctx, m.ate)
	if err != nil {
		return mercado{}, []string{"carteira para a correlação: " + err.Error()}, false
	}
	m.cart = cart
	if idx.nota != "" {
		notas = append(notas, idx.nota)
	}
	if err := a.banco.SalvarConfigTexto("precos_ate", m.ate.Format(time.RFC3339)); err != nil {
		notas = append(notas, err.Error())
	}
	return m, notas, true
}

// historico monta o histórico de um ativo para os indicadores.
func (a *app) historico(ctx context.Context, t string, m mercado) (indicadores.Historico, []string) {
	s, err := a.serie(ctx, t)
	if err != nil {
		return indicadores.Historico{}, []string{t + ": " + err.Error()}
	}
	var notas []string
	if s.nota != "" {
		notas = append(notas, s.nota)
	}
	if len(s.bruto) == 0 {
		return indicadores.Historico{}, append(notas, t+": sem histórico de preços no COTAHIST")
	}
	h := indicadores.Historico{
		Precos: s.bruto, Ajustado: s.ajustado, Ajustes: s.ajustes,
		Indice: m.indice.ajustado, Fonte: "COTAHIST (B3)", Ate: m.ate,
	}
	h.Carteira, h.PesosCarteira = m.cart.semAtivo(t)
	return h, notas
}

// historicos monta o histórico de cada ativo pedido.
func (a *app) historicos(ctx context.Context, tickers []string) (map[string]indicadores.Historico, []string) {
	res := map[string]indicadores.Historico{}
	m, notas, ok := a.prepararMercado(ctx)
	if !ok {
		return res, notas
	}
	for _, t := range tickers {
		h, n := a.historico(ctx, t, m)
		notas = append(notas, n...)
		if len(h.Precos) > 0 {
			res[t] = h
		}
	}
	return res, notas
}

// adiantarEventos busca no site da B3, 4 empresas por vez, os eventos que
// faltam ou estão velhos e grava no banco; depois a.eventos acha tudo salvo.
//
// Novidade de Go: "go func()" roda a função em paralelo (goroutine), e os
// canais (chan) passam dados entre elas com segurança. Aqui 4 goroutines
// pegam empresas do canal "pedidos" e devolvem o resultado em "respostas";
// só a goroutine principal grava no banco.
func (a *app) adiantarEventos(ctx context.Context, empresas []string) {
	var faltam []string
	for _, e := range empresas {
		s, ok, _ := a.banco.ConfigTexto("b3_eventos_" + e)
		if t, err := time.Parse(time.RFC3339, s); !ok || err != nil || a.agora().Sub(t) >= validadePrecos {
			faltam = append(faltam, e)
		}
	}
	if len(faltam) == 0 {
		return
	}
	fmt.Fprintf(a.saida, "buscando proventos e desdobramentos de %d empresas no site da B3…\n", len(faltam))

	type resposta struct {
		empresa string
		evs     []b3.Evento
		err     error
	}
	pedidos := make(chan string)
	respostas := make(chan resposta)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range pedidos {
				evs, err := a.b3.Eventos(ctx, e)
				respostas <- resposta{e, evs, err}
			}
		}()
	}
	go func() {
		for _, e := range faltam {
			pedidos <- e
		}
		close(pedidos) // avisa as goroutines que acabou
	}()
	go func() {
		wg.Wait() // espera as 4 terminarem para fechar as respostas
		close(respostas)
	}()

	if a.eventosFalhos == nil {
		a.eventosFalhos = map[string]error{}
	}
	for r := range respostas {
		if r.err != nil {
			a.eventosFalhos[r.empresa] = r.err // a.eventos não tenta de novo
			continue
		}
		if err := a.banco.SubstituirEventos(r.empresa, r.evs); err != nil {
			a.eventosFalhos[r.empresa] = err
			continue
		}
		a.banco.SalvarConfigTexto("b3_eventos_"+r.empresa, a.agora().Format(time.RFC3339))
	}
}
