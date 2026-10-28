// Package carteira calcula posição, preço médio e peso de cada ativo a partir das operações.
package carteira

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Tipo diz como a operação mexe na quantidade e no custo do ativo.
type Tipo string

const (
	Compra      Tipo = "compra"      // soma quantidade e custo (qtd × preço)
	Venda       Tipo = "venda"       // tira quantidade pelo preço médio e apura o resultado
	Ajuste      Tipo = "ajuste"      // muda só a quantidade (desdobro, grupamento); qtd negativa tira ações
	Bonificacao Tipo = "bonificacao" // soma quantidade e custo pelo valor que a empresa atribuiu
	Troca       Tipo = "troca"       // troca de código (ex.: TRPL4 → ISAE4): custo passa do código antigo para o novo
)

// Operacao é um lançamento que altera a posição de um ativo.
type Operacao struct {
	ID         int64
	Data       time.Time
	Ticker     string
	Tipo       Tipo
	Quantidade float64
	Preco      float64
	Descricao  string // texto da movimentação na B3, ou "manual"
	Origem     string // "manual" ou "b3-movimentacao"
	Chave      string // identifica a linha do extrato para não importar duas vezes; vazia no manual
	// TickerAnterior é o código antigo numa troca de código; vazio nos outros tipos.
	TickerAnterior string
}

// Provento é um dividendo, JCP ou rendimento recebido.
type Provento struct {
	ID     int64
	Data   time.Time
	Ticker string
	Tipo   string // como veio da B3: Dividendo, Juros Sobre Capital Próprio, Rendimento
	Valor  float64
	Origem string
	Chave  string
}

// Posicao é o resultado de aplicar todas as operações de um ativo.
type Posicao struct {
	Ticker     string
	Quantidade float64
	Custo      float64 // custo total da quantidade que sobrou
	Realizado  float64 // lucro ou prejuízo das vendas já feitas
}

// PrecoMedio é o custo total dividido pela quantidade.
func (p Posicao) PrecoMedio() float64 {
	if p.Quantidade == 0 {
		return 0
	}
	return p.Custo / p.Quantidade
}

// Sobras de arredondamento menores que isso contam como zero.
const quaseZero = 1e-9

// Código da B3: 4 caracteres começando por letra (B3SA tem dígito) + 1 ou 2 dígitos.
var reTicker = regexp.MustCompile(`^[A-Z][A-Z0-9]{3}[0-9]{1,2}$`)

// Bitcoin é o código usado para BTC (lançado à mão; não passa pela B3).
const Bitcoin = "BTC"

var reAno = regexp.MustCompile(`^[0-9]{4}$`)

// NormalizarAtivo aceita, além dos códigos da B3, o BTC e os títulos do
// Tesouro Direto pelo nome que a B3 usa no extrato ("Tesouro Selic 2031").
func NormalizarAtivo(s string) (string, bool) {
	if t, ok := NormalizarTicker(s); ok {
		return t, true
	}
	campos := strings.Fields(s)
	if len(campos) == 1 && strings.EqualFold(campos[0], Bitcoin) {
		return Bitcoin, true
	}
	if len(campos) >= 3 && strings.EqualFold(campos[0], "tesouro") && reAno.MatchString(campos[len(campos)-1]) {
		campos[0] = "Tesouro"
		return strings.Join(campos, " "), true
	}
	return strings.TrimSpace(s), false
}

// EhTesouro diz se o ativo é um título do Tesouro Direto.
func EhTesouro(ativo string) bool { return strings.HasPrefix(ativo, "Tesouro ") }

// EhB3 diz se o ativo é negociado na bolsa (ação, FII, ETF, unit).
func EhB3(ativo string) bool { return reTicker.MatchString(ativo) }

// NormalizarTicker deixa o código em maiúsculas e tira o nome da empresa
// ("BBAS3 - BANCO DO BRASIL S/A" vira "BBAS3") e o F do mercado fracionário
// (BBAS3F vira BBAS3). Devolve false se o texto não for um código da B3.
func NormalizarTicker(s string) (string, bool) {
	t := strings.ToUpper(strings.TrimSpace(s))
	if antes, _, ok := strings.Cut(t, " - "); ok {
		t = strings.TrimSpace(antes)
	}
	if sem, ok := strings.CutSuffix(t, "F"); ok && reTicker.MatchString(sem) {
		t = sem
	}
	return t, reTicker.MatchString(t)
}

// Validar confere se a operação faz sentido antes de gravar.
func (op Operacao) Validar() error {
	if _, ok := NormalizarAtivo(op.Ticker); !ok {
		return fmt.Errorf("código de ativo inválido: %q", op.Ticker)
	}
	if op.Data.IsZero() {
		return errors.New("operação sem data")
	}
	switch op.Tipo {
	case Compra, Venda:
		if op.Quantidade <= 0 || op.Preco <= 0 {
			return errors.New("quantidade e preço precisam ser maiores que zero")
		}
	case Bonificacao:
		if op.Quantidade <= 0 || op.Preco < 0 {
			return errors.New("bonificação precisa de quantidade maior que zero e valor não negativo")
		}
	case Ajuste:
		if op.Quantidade == 0 {
			return errors.New("ajuste com quantidade zero não muda nada")
		}
	case Troca:
		if op.Quantidade <= 0 {
			return errors.New("troca de código precisa de quantidade maior que zero")
		}
		if ant, ok := NormalizarTicker(op.TickerAnterior); !ok || ant == op.Ticker {
			return fmt.Errorf("código anterior inválido na troca: %q", op.TickerAnterior)
		}
	default:
		return fmt.Errorf("tipo de operação desconhecido: %q", op.Tipo)
	}
	return nil
}

// ordemNoDia decide a ordem das operações de um mesmo dia: entradas primeiro,
// depois trocas de código, depois saídas. Assim uma compra e uma venda do mesmo
// dia não deixam a posição negativa.
func ordemNoDia(op Operacao) int {
	switch {
	case op.Tipo == Troca:
		return 1
	case op.Tipo == Venda || (op.Tipo == Ajuste && op.Quantidade < 0):
		return 2
	default:
		return 0
	}
}

// CalcularPosicoes aplica as operações em ordem de data, pelo método do preço
// médio da Receita: compra soma ao custo; venda tira ações pelo preço médio,
// sem mudar o preço médio, e apura o resultado. Quando a posição zera, o custo zera.
//
// Os avisos apontam vendas maiores que a posição. Isso quase sempre significa
// compras antigas que faltam (o extrato da B3 só começa em nov/2019).
func CalcularPosicoes(ops []Operacao) (map[string]*Posicao, []string) {
	ordenadas := slices.Clone(ops)
	slices.SortStableFunc(ordenadas, func(a, b Operacao) int {
		if c := a.Data.Compare(b.Data); c != 0 {
			return c
		}
		return cmp.Compare(ordemNoDia(a), ordemNoDia(b))
	})

	pos := map[string]*Posicao{}
	var avisos []string
	for _, op := range ordenadas {
		p := pos[op.Ticker]
		if p == nil {
			p = &Posicao{Ticker: op.Ticker}
			pos[op.Ticker] = p
		}

		switch op.Tipo {
		case Compra, Bonificacao:
			p.Quantidade += op.Quantidade
			p.Custo += op.Quantidade * op.Preco
		case Troca:
			// A quantidade nova vem da B3 (pode mudar se a troca tiver proporção);
			// o custo é todo herdado do código antigo.
			p.Quantidade += op.Quantidade
			ant := pos[op.TickerAnterior]
			if ant == nil || ant.Quantidade == 0 {
				avisos = append(avisos, fmt.Sprintf("%s: troca de código em %s, mas não há posição em %s; entrou com custo zero.",
					op.Ticker, op.Data.Format("02/01/2006"), op.TickerAnterior))
				break
			}
			p.Custo += ant.Custo
			ant.Quantidade, ant.Custo = 0, 0
		case Ajuste:
			p.Quantidade += op.Quantidade
			if p.Quantidade < -quaseZero {
				avisos = append(avisos, fmt.Sprintf("%s: ajuste de %g em %s deixou a posição negativa; posição zerada.",
					op.Ticker, op.Quantidade, op.Data.Format("02/01/2006")))
				p.Quantidade = 0
			}
		case Venda:
			qtd := op.Quantidade
			if qtd > p.Quantidade+quaseZero {
				avisos = append(avisos, fmt.Sprintf("%s: venda de %g em %s é maior que a posição (%g). Faltam compras antigas?",
					op.Ticker, op.Quantidade, op.Data.Format("02/01/2006"), p.Quantidade))
				qtd = p.Quantidade
			}
			pm := p.PrecoMedio()
			p.Realizado += qtd * (op.Preco - pm)
			p.Custo -= qtd * pm
			p.Quantidade -= qtd
		}

		if math.Abs(p.Quantidade) < quaseZero {
			p.Quantidade = 0
			p.Custo = 0
		}
	}
	return pos, avisos
}

// Linha é uma posição com cotação, valor de mercado e peso na carteira.
type Linha struct {
	Posicao
	Cotacao float64 // 0 quando não há cotação
	Valor   float64 // quantidade × cotação
	Peso    float64 // fração (0 a 1) do valor total da carteira
}

// Pesos calcula valor e peso de cada posição aberta com as cotações dadas.
// Ativo sem cotação fica fora do total e com peso zero.
// As linhas saem da maior para a menor.
func Pesos(pos map[string]*Posicao, cotacoes map[string]float64) ([]Linha, float64) {
	var linhas []Linha
	var total float64
	for _, p := range pos {
		if p.Quantidade <= 0 {
			continue
		}
		l := Linha{Posicao: *p, Cotacao: cotacoes[p.Ticker]}
		l.Valor = l.Quantidade * l.Cotacao
		total += l.Valor
		linhas = append(linhas, l)
	}
	for i := range linhas {
		if total > 0 {
			linhas[i].Peso = linhas[i].Valor / total
		}
	}
	slices.SortFunc(linhas, func(a, b Linha) int {
		if c := cmp.Compare(b.Valor, a.Valor); c != 0 {
			return c
		}
		return cmp.Compare(a.Ticker, b.Ticker)
	})
	return linhas, total
}
