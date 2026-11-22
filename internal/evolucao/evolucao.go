// Package evolucao calcula, dia a dia, a rentabilidade da carteira pelo
// método de cotas e os índices usados na comparação (ETFs da B3, CDI, IPCA).
//
// Método de cotas (o mesmo dos fundos): no primeiro dia a carteira vale 1
// cota. Em cada pregão, o rendimento do dia é
//
//	(valor de hoje − dinheiro que entrou hoje) ÷ valor de ontem
//
// em que "dinheiro que entrou" = compras − vendas − proventos recebidos.
// Assim aporte não conta como ganho, e provento recebido conta (ele saiu da
// carteira, mas foi rendimento). A cota acumula os rendimentos diários.
package evolucao

import (
	"math"
	"sort"
	"time"

	"carteira/internal/carteira"
	"carteira/internal/risco"
)

// Dia tira a hora: só a data, em UTC (o padrão das datas no banco).
func Dia(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Entrada é o que o cálculo da carteira precisa.
type Entrada struct {
	Dias      []time.Time // pregões, em ordem: o calendário do gráfico
	Operacoes []carteira.Operacao
	Proventos []carteira.Provento
	Precos    map[string][]risco.Ponto // preço de mercado de cada ativo, em ordem de data
}

// Carteira é a evolução da carteira, um valor por pregão desde a primeira operação.
type Carteira struct {
	Dias       []time.Time
	Cota       []float64 // 1 no primeiro dia
	Patrimonio []float64 // valor de mercado no fim do dia
	Investido  []float64 // compras − vendas, acumulado
	Proventos  []float64 // proventos recebidos, acumulado
	// SemPreco são os ativos que, em algum dia, ficaram sem preço de mercado
	// recente e foram avaliados pelo preço da última operação (ou pelo preço médio).
	SemPreco []string
}

// Preço de mercado mais velho que isso (em relação ao dia) conta como ausente.
const precoVelho = 10 * 24 * time.Hour

// Calcular monta a cota, o patrimônio e o total investido de cada pregão.
// Operação ou provento em dia sem pregão (fim de semana, feriado) entra no
// pregão seguinte; o que for depois do último pregão fica de fora.
func Calcular(e Entrada) Carteira {
	var c Carteira
	n := len(e.Dias)
	indice := func(t time.Time) int {
		d := Dia(t)
		return sort.Search(n, func(i int) bool { return !Dia(e.Dias[i]).Before(d) })
	}
	opsDia := map[int][]carteira.Operacao{}
	inicio := n
	for _, op := range e.Operacoes {
		if i := indice(op.Data); i < n {
			opsDia[i] = append(opsDia[i], op)
			inicio = min(inicio, i)
		}
	}
	if inicio == n {
		return c
	}
	proventos := map[int]float64{}
	for _, p := range e.Proventos {
		if i := indice(p.Data); i >= inicio && i < n {
			proventos[i] += p.Valor
		}
	}

	type ultimaOperacao struct {
		data  time.Time
		preco float64
	}
	ultima := map[string]ultimaOperacao{}
	proximo := map[string]int{} // em Precos[ativo], o primeiro preço depois do dia atual
	semPreco := map[string]bool{}
	var aplicadas []carteira.Operacao
	var pos map[string]*carteira.Posicao
	cota, investido, recebido, ontem := 1.0, 0.0, 0.0, 0.0

	for i := inicio; i < n; i++ {
		d := Dia(e.Dias[i])
		fluxo := -proventos[i]
		recebido += proventos[i]
		if ops := opsDia[i]; len(ops) > 0 {
			for _, op := range ops {
				switch op.Tipo {
				case carteira.Compra:
					fluxo += op.Quantidade * op.Preco
					investido += op.Quantidade * op.Preco
				case carteira.Venda:
					fluxo -= op.Quantidade * op.Preco
					investido -= op.Quantidade * op.Preco
				default:
					continue // ajuste, bonificação e troca não mexem em dinheiro
				}
				if u, ok := ultima[op.Ticker]; !ok || !Dia(op.Data).Before(u.data) {
					ultima[op.Ticker] = ultimaOperacao{Dia(op.Data), op.Preco}
				}
			}
			// Mesma regra da posição (ordem no dia, trocas de código, vendas
			// maiores que a posição): recalcula com todas as operações até hoje.
			aplicadas = append(aplicadas, ops...)
			pos, _ = carteira.CalcularPosicoes(aplicadas)
		}

		var valor float64
		for t, p := range pos {
			if p.Quantidade <= 0 {
				continue
			}
			ps := e.Precos[t]
			k := proximo[t]
			for k < len(ps) && !Dia(ps[k].Data).After(d) {
				k++
			}
			proximo[t] = k
			var preco float64
			var dataPreco time.Time
			if k > 0 {
				preco, dataPreco = ps[k-1].Valor, Dia(ps[k-1].Data)
			}
			u, temOp := ultima[t]
			switch {
			case temOp && (preco == 0 || u.data.After(dataPreco)):
				// Operação mais nova que o último preço (ex.: compra num sábado).
				if preco == 0 || d.Sub(dataPreco) > precoVelho {
					semPreco[t] = true
				}
				preco = u.preco
			case preco == 0 || d.Sub(dataPreco) > precoVelho:
				if preco == 0 {
					preco = p.PrecoMedio() // ex.: código novo depois de uma troca, ainda sem pregão
				}
				semPreco[t] = true
			}
			valor += p.Quantidade * preco
		}

		if ontem > 0 {
			cota *= (valor - fluxo) / ontem
		}
		c.Dias = append(c.Dias, d)
		c.Cota = append(c.Cota, cota)
		c.Patrimonio = append(c.Patrimonio, valor)
		c.Investido = append(c.Investido, investido)
		c.Proventos = append(c.Proventos, recebido)
		ontem = valor
	}
	for t := range semPreco {
		c.SemPreco = append(c.SemPreco, t)
	}
	sort.Strings(c.SemPreco)
	return c
}

// IndicePrecos devolve, para cada dia, o último preço até ele dividido pelo
// do primeiro dia com preço (1 = começo). Antes do primeiro preço: NaN.
func IndicePrecos(dias []time.Time, ps []risco.Ponto) []float64 {
	res := make([]float64, len(dias))
	k, base := 0, 0.0
	for i, d := range dias {
		for k < len(ps) && !Dia(ps[k].Data).After(Dia(d)) {
			k++
		}
		if k == 0 || ps[k-1].Valor <= 0 {
			res[i] = math.NaN()
			continue
		}
		if base == 0 {
			base = ps[k-1].Valor
		}
		res[i] = ps[k-1].Valor / base
	}
	return res
}

// IndiceCDI acumula a taxa diária do CDI (% ao dia). A taxa de um dia rende
// até o dia útil seguinte, então o valor de um pregão soma as taxas dos dias
// anteriores a ele. Passados 5 dias da última taxa publicada: NaN.
func IndiceCDI(dias []time.Time, taxas []risco.Ponto) []float64 {
	res := make([]float64, len(dias))
	if len(taxas) == 0 {
		for i := range res {
			res[i] = math.NaN()
		}
		return res
	}
	limite := Dia(taxas[len(taxas)-1].Data).AddDate(0, 0, 5)
	k, nivel := 0, 1.0
	for k < len(taxas) && len(dias) > 0 && Dia(taxas[k].Data).Before(Dia(dias[0])) {
		k++
	}
	for i, d := range dias {
		d = Dia(d)
		if i > 0 {
			for k < len(taxas) && Dia(taxas[k].Data).Before(d) {
				nivel *= 1 + taxas[k].Valor/100
				k++
			}
		}
		res[i] = nivel
		if d.After(limite) {
			res[i] = math.NaN()
		}
	}
	return res
}

// IndiceIPCA transforma o IPCA mensal (% no mês, data no dia 1) num valor
// por dia: dentro do mês, a inflação é distribuída pelos dias corridos
// (juros compostos). Mês ainda sem IPCA publicado: NaN.
func IndiceIPCA(dias []time.Time, mensal []risco.Ponto) []float64 {
	res := make([]float64, len(dias))
	// nivelMes[m] = inflação acumulada até o começo do mês m.
	type mes struct{ ano, mes int }
	nivelMes := map[mes]float64{}
	taxaMes := map[mes]float64{}
	nivel := 1.0
	for _, v := range mensal {
		m := mes{v.Data.Year(), int(v.Data.Month())}
		nivelMes[m], taxaMes[m] = nivel, v.Valor/100
		nivel *= 1 + v.Valor/100
	}
	base := math.NaN()
	for i, d := range dias {
		m := mes{d.Year(), int(d.Month())}
		ini, ok := nivelMes[m]
		if !ok {
			res[i] = math.NaN()
			continue
		}
		diasNoMes := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		v := ini * math.Pow(1+taxaMes[m], float64(d.Day())/float64(diasNoMes))
		if math.IsNaN(base) {
			base = v
		}
		res[i] = v / base
	}
	return res
}

// Periodo é um intervalo de pregões para medir a rentabilidade: o retorno
// vai do fechamento de Ini ao de Fim.
type Periodo struct {
	Rotulo  string
	Ini     int
	Fim     int
	Parcial bool // ano que não começa no primeiro pregão (ano da primeira operação ou o ano atual)
}

// Periodos devolve cada ano, os últimos 12 meses e o período todo.
func Periodos(dias []time.Time) []Periodo {
	n := len(dias)
	if n < 2 {
		return nil
	}
	var res []Periodo
	ini := 0
	for i := 1; i <= n; i++ {
		if i < n && dias[i].Year() == dias[i-1].Year() {
			continue
		}
		// i-1 é o último pregão do ano; a base é o último pregão do ano anterior.
		ano := dias[i-1].Year()
		p := Periodo{Rotulo: time.Date(ano, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006"), Ini: ini, Fim: i - 1}
		p.Parcial = ini == 0 || i == n && dias[n-1].Month() != time.December
		if p.Fim > p.Ini {
			res = append(res, p)
		}
		ini = i - 1
	}
	umAno := Dia(dias[n-1]).AddDate(-1, 0, 0)
	i12 := sort.Search(n, func(i int) bool { return Dia(dias[i]).After(umAno) }) - 1
	if i12 >= 0 {
		res = append(res, Periodo{Rotulo: "12 meses", Ini: i12, Fim: n - 1})
	}
	res = append(res, Periodo{Rotulo: "Desde o início", Ini: 0, Fim: n - 1})
	return res
}

// Retorno mede a variação do índice no período. Se faltar o valor do fim
// (ex.: IPCA do mês ainda não publicado), usa o último que houver dentro do
// período e devolve ate < Fim. ok é false se não há como medir.
func Retorno(idx []float64, p Periodo) (r float64, ate int, ok bool) {
	ini := p.Ini
	for ini <= p.Fim && math.IsNaN(idx[ini]) {
		ini++
	}
	fim := p.Fim
	for fim > ini && math.IsNaN(idx[fim]) {
		fim--
	}
	if fim <= ini || idx[ini] == 0 {
		return 0, 0, false
	}
	return idx[fim]/idx[ini] - 1, fim, true
}

// Recomeco acha a volta depois do último intervalo de pelo menos minZerado
// pregões com a carteira vazia e devolve o índice do pregão anterior à volta
// (a base do retorno, para contar o primeiro dia). Sem intervalo assim: -1.
func Recomeco(patrimonio []float64, minZerado int) int {
	zerado, volta := 0, -1
	for i, v := range patrimonio {
		if v > 0 {
			if zerado >= minZerado && i > 0 {
				volta = i - 1
			}
			zerado = 0
			continue
		}
		zerado++
	}
	return volta
}
