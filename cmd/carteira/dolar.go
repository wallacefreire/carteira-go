package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/fontes/bcb"
	"carteira/internal/formato"
)

// ---------- dólar (PTAX) e ativos cotados em dólar ----------

// emDolar diz se o ativo é cotado lá fora em dólar (hoje só o bitcoin). Na
// posição, o resultado dele é separado em parte do ativo e parte do câmbio.
func emDolar(t string) bool { return t == carteira.Bitcoin }

// dolar devolve a PTAX de venda do dia ou, sem cotação nele, a do último dia
// útil antes. Dia já passado fica guardado no banco (config
// ptax_AAAA-MM-DD = "AAAA-MM-DD;valor"), porque a PTAX dele não muda mais.
func (a *app) dolar(ctx context.Context, dia time.Time) (bcb.Cotacao, error) {
	dia = time.Date(dia.Year(), dia.Month(), dia.Day(), 0, 0, 0, 0, time.Local)
	chave := "ptax_" + dia.Format("2006-01-02")
	if c, ok := a.dolares[chave]; ok {
		return c, nil
	}
	agora := a.agora()
	hoje := time.Date(agora.Year(), agora.Month(), agora.Day(), 0, 0, 0, 0, time.Local)
	passado := dia.Before(hoje)
	if passado {
		if s, ok, err := a.banco.ConfigTexto(chave); err == nil && ok {
			if c, ok := lerPTAXSalva(s); ok {
				a.guardarDolar(chave, c)
				return c, nil
			}
		}
	}
	c, err := a.bcb.Dolar(ctx, dia)
	if err != nil {
		return c, err
	}
	if passado {
		valor := c.Dia.Format("2006-01-02") + ";" + strconv.FormatFloat(c.Venda, 'f', -1, 64)
		if err := a.banco.SalvarConfigTexto(chave, valor); err != nil {
			return c, err
		}
	}
	a.guardarDolar(chave, c)
	return c, nil
}

// guardarDolar guarda a PTAX na memória desta execução. Em Go, um mapa nil
// pode ser lido, mas não recebe escrita: por isso o make na primeira vez.
func (a *app) guardarDolar(chave string, c bcb.Cotacao) {
	if a.dolares == nil {
		a.dolares = map[string]bcb.Cotacao{}
	}
	a.dolares[chave] = c
}

func lerPTAXSalva(s string) (bcb.Cotacao, bool) {
	diaTxt, valorTxt, ok := strings.Cut(s, ";")
	if !ok {
		return bcb.Cotacao{}, false
	}
	dia, err := time.ParseInLocation("2006-01-02", diaTxt, time.Local)
	if err != nil {
		return bcb.Cotacao{}, false
	}
	v, err := strconv.ParseFloat(valorTxt, 64)
	if err != nil || v <= 0 {
		return bcb.Cotacao{}, false
	}
	return bcb.Cotacao{Dia: dia, Venda: v}, true
}

// linhaDolar separa o resultado de um ativo cotado em dólar em quanto veio do
// próprio ativo (o preço em dólar) e quanto veio do câmbio. As duas partes
// somam o resultado em reais da posição.
type linhaDolar struct {
	Ticker          string   `json:"ticker"`
	CustoUSD        string   `json:"custoUSD"`
	ValorUSD        string   `json:"valorUSD"`
	ResultadoUSDPct string   `json:"resultadoUSDPct"`
	DolarCompra     string   `json:"dolarCompra"` // médio: custo em R$ ÷ custo em US$
	DolarHoje       string   `json:"dolarHoje"`
	DiaDolarHoje    string   `json:"diaDolarHoje"`
	CambioPct       string   `json:"cambioPct"`
	ParteAtivo      string   `json:"parteAtivo"`
	ParteAtivoNum   float64  `json:"parteAtivoNum"`
	ParteCambio     string   `json:"parteCambio"`
	ParteCambioNum  float64  `json:"parteCambioNum"`
	Resultado       string   `json:"resultado"`
	ResultadoNum    float64  `json:"resultadoNum"`
	Contas          []string `json:"contas"` // memória de cálculo
}

// separarDolar monta a linhaDolar de cada ativo cotado em dólar da posição.
//
// Cada operação é convertida para dólar pela PTAX de venda do dia dela e o
// custo em dólar sai pelo mesmo preço médio da Receita. Com C = custo, V =
// valor hoje, d₀ = dólar médio das compras (C em R$ ÷ C em US$) e d₁ = dólar
// de hoje (V em US$ = V em R$ ÷ d₁):
//
//	parte do ativo  = (V em US$ − C em US$) × d₀
//	parte do câmbio = V em US$ × (d₁ − d₀)
//
// e as duas somam V em R$ − C em R$.
func (a *app) separarDolar(ctx context.Context, ops []carteira.Operacao, linhas []carteira.Linha) ([]linhaDolar, error) {
	var res []linhaDolar
	for _, l := range linhas {
		if !emDolar(l.Ticker) || l.Cotacao <= 0 || l.Quantidade <= 0 || l.Custo <= 0 {
			continue
		}
		var contas []string
		var emUSD []carteira.Operacao
		for _, op := range ops {
			if op.Ticker != l.Ticker {
				continue
			}
			if op.Preco > 0 {
				d, err := a.dolar(ctx, op.Data)
				if err != nil {
					return nil, err
				}
				contas = append(contas, fmt.Sprintf("%s %s %s %s: R$ %s ÷ dólar %s (PTAX de %s) = US$ %s",
					formato.Data(op.Data), op.Tipo, formato.Quantidade(op.Quantidade), op.Ticker, formato.Moeda(op.Quantidade*op.Preco),
					formato.Numero(d.Venda, 4), formato.Data(d.Dia), formato.Moeda(op.Quantidade*op.Preco/d.Venda)))
				op.Preco /= d.Venda // op é uma cópia: o original não muda
			}
			emUSD = append(emUSD, op)
		}
		pos, _ := carteira.CalcularPosicoes(emUSD)
		p, ok := pos[l.Ticker]
		if !ok || p.Custo <= 0 {
			continue
		}
		hoje, err := a.dolar(ctx, a.agora())
		if err != nil {
			return nil, err
		}

		custoUSD := p.Custo
		valorUSD := l.Valor / hoje.Venda
		d0, d1 := l.Custo/custoUSD, hoje.Venda
		ld := linhaDolar{
			Ticker:          l.Ticker,
			CustoUSD:        formato.Moeda(custoUSD),
			ValorUSD:        formato.Moeda(valorUSD),
			ResultadoUSDPct: formato.Pct(valorUSD/custoUSD - 1),
			DolarCompra:     formato.Numero(d0, 4),
			DolarHoje:       formato.Numero(d1, 4),
			DiaDolarHoje:    formato.Data(hoje.Dia),
			CambioPct:       formato.Pct(d1/d0 - 1),
			ParteAtivoNum:   (valorUSD - custoUSD) * d0,
			ParteCambioNum:  valorUSD * (d1 - d0),
			ResultadoNum:    l.Valor - l.Custo,
		}
		ld.ParteAtivo = formato.Moeda(ld.ParteAtivoNum)
		ld.ParteCambio = formato.Moeda(ld.ParteCambioNum)
		ld.Resultado = formato.Moeda(ld.ResultadoNum)
		ld.Contas = append(contas,
			fmt.Sprintf("Custo em US$ (preço médio da Receita): US$ %s; em R$: %s; dólar médio das compras = %s ÷ %s = %s",
				ld.CustoUSD, formato.Moeda(l.Custo), formato.Moeda(l.Custo), ld.CustoUSD, ld.DolarCompra),
			fmt.Sprintf("Valor hoje: R$ %s ÷ dólar %s (PTAX de %s) = US$ %s", formato.Moeda(l.Valor), ld.DolarHoje, ld.DiaDolarHoje, ld.ValorUSD),
			fmt.Sprintf("Parte do ativo = (US$ %s − US$ %s) × %s = R$ %s", ld.ValorUSD, ld.CustoUSD, ld.DolarCompra, ld.ParteAtivo),
			fmt.Sprintf("Parte do câmbio = US$ %s × (%s − %s) = R$ %s", ld.ValorUSD, ld.DolarHoje, ld.DolarCompra, ld.ParteCambio),
			fmt.Sprintf("Soma = R$ %s = valor R$ %s − custo R$ %s", ld.Resultado, formato.Moeda(l.Valor), formato.Moeda(l.Custo)),
		)
		res = append(res, ld)
	}
	return res, nil
}
