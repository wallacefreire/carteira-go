// Package importacao lê o extrato de Movimentação da Área do Investidor da B3 (.xlsx).
//
// O extrato traz as operações de todas as corretoras, a partir de nov/2019.
// Cada linha vira uma operação, um provento ou é deixada de lado e contada
// em Ignorados. Eventos que mudam a quantidade de um jeito incerto
// (grupamento, incorporação…) não são aplicados: viram aviso para conferir.
package importacao

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"carteira/internal/carteira"
	"carteira/internal/formato"
)

// Origem gravada nas operações e proventos vindos deste extrato.
const Origem = "b3-movimentacao"

// Resultado é o que saiu da leitura do extrato, antes de gravar no banco.
type Resultado struct {
	Operacoes []carteira.Operacao
	Proventos []carteira.Provento
	Ignorados map[string]int // tipo de movimentação → número de linhas não usadas
	Avisos    []string
	Linhas    int // linhas de dados lidas (sem contar o cabeçalho e linhas vazias)
}

// Colunas do extrato, já sem acento e em minúsculas.
const (
	colEntradaSaida = "entrada/saida"
	colData         = "data"
	colMovimentacao = "movimentacao"
	colProduto      = "produto"
	colInstituicao  = "instituicao"
	colQuantidade   = "quantidade"
	colPreco        = "preco unitario"
	colValor        = "valor da operacao"
)

var colunasObrigatorias = []string{colEntradaSaida, colData, colMovimentacao, colProduto, colQuantidade, colPreco, colValor}

// Movimentações que mudam a quantidade de ações e que não sabemos aplicar com
// segurança. Viram aviso para o usuário conferir e lançar com "carteira ajuste".
var conferirAMao = map[string]bool{
	"grupamento":   true,
	"incorporacao": true,
	"cisao":        true,
	"conversao":    true,
	"resgate":      true,
}

// LerMovimentacao abre o .xlsx exportado em Extratos > Movimentação.
func LerMovimentacao(caminho string) (*Resultado, error) {
	f, err := excelize.OpenFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", caminho, err)
	}
	defer f.Close()

	folhas := f.GetSheetList()
	if len(folhas) == 0 {
		return nil, errors.New("planilha sem abas")
	}
	// RawCellValue: números vêm como "27.5" e não como o texto formatado da célula.
	linhas, err := f.GetRows(folhas[0], excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("ler aba %q: %w", folhas[0], err)
	}
	return lerLinhas(linhas)
}

func lerLinhas(linhas [][]string) (*Resultado, error) {
	inicio, col, err := acharCabecalho(linhas)
	if err != nil {
		return nil, err
	}

	r := &Resultado{Ignorados: map[string]int{}}
	vistas := map[string]int{}
	empresas := codigosPorEmpresa(linhas[inicio+1:], col[colProduto])

	for i, l := range linhas[inicio+1:] {
		numLinha := inicio + i + 2 // número da linha no Excel
		celula := func(nome string) string {
			j, ok := col[nome]
			if !ok || j >= len(l) {
				return ""
			}
			return strings.TrimSpace(l[j])
		}
		if strings.TrimSpace(strings.Join(l, "")) == "" {
			continue
		}
		r.Linhas++

		mov := celula(colMovimentacao)
		data, err := lerData(celula(colData))
		if err != nil {
			r.Avisos = append(r.Avisos, fmt.Sprintf("linha %d: data inválida %q; linha ignorada", numLinha, celula(colData)))
			continue
		}
		credito := normalizar(celula(colEntradaSaida)) == "credito"
		qtd, temQtd := numero(celula(colQuantidade))
		preco, temPreco := numero(celula(colPreco))
		valor, temValor := numero(celula(colValor))

		// A chave identifica a linha entre importações do mesmo extrato (períodos que
		// se sobrepõem). Linhas idênticas no mesmo arquivo recebem #1, #2… para não
		// serem confundidas entre si.
		base := strings.Join([]string{
			data.Format("2006-01-02"), celula(colEntradaSaida), mov, celula(colProduto), celula(colInstituicao),
			strconv.FormatFloat(qtd, 'g', -1, 64), strconv.FormatFloat(preco, 'g', -1, 64), strconv.FormatFloat(valor, 'g', -1, 64),
		}, "|")
		vistas[base]++
		soma := sha256.Sum256(fmt.Appendf(nil, "%s#%d", base, vistas[base]))
		chave := hex.EncodeToString(soma[:16])

		if titulo, ok := carteira.NormalizarAtivo(celula(colProduto)); ok && carteira.EhTesouro(titulo) {
			// Tesouro Direto: compra é crédito; venda, resgate e vencimento são débito.
			switch normalizar(mov) {
			case "compra", "venda", "resgate", "vencimento":
			default:
				r.Ignorados[mov+" (Tesouro)"]++
				r.Avisos = append(r.Avisos, fmt.Sprintf("%s: %q em %s não foi aplicado; confira a quantidade.", titulo, mov, formato.Data(data)))
				continue
			}
			if (!temPreco || preco <= 0) && temValor && temQtd && qtd > 0 {
				preco = valor / qtd
			}
			op := carteira.Operacao{Data: data, Ticker: titulo, Tipo: carteira.Compra, Quantidade: qtd, Preco: preco,
				Descricao: mov, Origem: Origem, Chave: chave}
			if !credito {
				op.Tipo = carteira.Venda
			}
			if err := op.Validar(); err != nil {
				r.Avisos = append(r.Avisos, fmt.Sprintf("linha %d (%s %s): %v; linha ignorada", numLinha, mov, titulo, err))
				continue
			}
			r.Operacoes = append(r.Operacoes, op)
			continue
		}
		ticker, ehAcao := carteira.NormalizarTicker(celula(colProduto))
		if !ehAcao {
			// CDB, debêntures, fundos etc. não são acompanhados.
			r.Ignorados[mov+" (produto não acompanhado)"]++
			continue
		}

		op := carteira.Operacao{Data: data, Ticker: ticker, Descricao: mov, Origem: Origem, Chave: chave}
		tipo := normalizar(mov)
		switch tipo {
		case "transferencia - liquidacao":
			// Liquidação de compra (crédito) ou venda (débito) feita em qualquer corretora.
			if (!temPreco || preco <= 0) && temValor && temQtd && qtd > 0 {
				preco = valor / qtd
			}
			if preco <= 0 {
				// Sem preço nem valor: na prática é aluguel de ações (vem junto de um
				// "Empréstimo"). As ações continuam sendo suas, então a posição não muda.
				r.Ignorados[mov+" sem preço"]++
				r.Avisos = append(r.Avisos, fmt.Sprintf("%s: liquidação de %s em %s sem preço não foi aplicada (costuma ser aluguel de ações). Se foi venda, lance com \"carteira venda\".",
					ticker, formato.Quantidade(qtd), formato.Data(data)))
				continue
			}
			op.Tipo, op.Quantidade, op.Preco = carteira.Compra, qtd, preco
			if !credito {
				op.Tipo = carteira.Venda
			}
		case "desdobro":
			op.Tipo, op.Quantidade = carteira.Ajuste, qtd
			if !credito {
				op.Quantidade = -qtd
			}
		case "bonificacao em ativos":
			if !credito {
				r.Ignorados[mov+" (débito)"]++
				continue
			}
			op.Tipo, op.Quantidade = carteira.Bonificacao, qtd
			if temPreco {
				op.Preco = preco // custo atribuído pela empresa, quando o extrato informa
			}
		case "atualizacao":
			// A B3 usa "Atualização" para troca de código (TRPL4 → ISAE4) e também para
			// outras coisas, como devolução de ações alugadas. É troca quando outro
			// código do extrato tem o mesmo nome de empresa.
			anterior := ""
			if credito {
				anterior = codigoAnterior(empresas, celula(colProduto), ticker)
			}
			if anterior == "" {
				r.Ignorados[mov]++
				r.Avisos = append(r.Avisos, fmt.Sprintf("%s: \"Atualização\" de %s em %s não foi aplicada (não é troca de código). Confira se a quantidade final bate com a da corretora.",
					ticker, formato.Quantidade(qtd), formato.Data(data)))
				continue
			}
			op.Tipo, op.Quantidade, op.TickerAnterior = carteira.Troca, qtd, anterior
			op.Descricao = mov + " (troca de " + anterior + ")"
		case "dividendo", "juros sobre capital proprio", "rendimento":
			if !credito {
				r.Ignorados[mov+" (débito)"]++
				continue
			}
			v := valor
			if !temValor {
				v = qtd * preco
			}
			r.Proventos = append(r.Proventos, carteira.Provento{
				Data: data, Ticker: ticker, Tipo: mov, Valor: v, Origem: Origem, Chave: chave,
			})
			continue
		default:
			r.Ignorados[mov]++
			if conferirAMao[tipo] {
				r.Avisos = append(r.Avisos, fmt.Sprintf("%s: %q em %s não foi aplicado. Confira a quantidade e, se precisar, use \"carteira ajuste\".",
					ticker, mov, formato.Data(data)))
			}
			continue
		}

		if err := op.Validar(); err != nil {
			r.Avisos = append(r.Avisos, fmt.Sprintf("linha %d (%s %s): %v; linha ignorada", numLinha, mov, ticker, err))
			continue
		}
		r.Operacoes = append(r.Operacoes, op)
	}
	return r, nil
}

// empresa devolve o nome da empresa do produto ("TRPL4 - CTEEP - CIA…" → "cteep - cia…").
func empresa(produto string) string {
	_, nome, _ := strings.Cut(produto, " - ")
	return normalizar(nome)
}

// codigosPorEmpresa junta, para cada nome de empresa, os códigos que aparecem no extrato.
func codigosPorEmpresa(linhas [][]string, colProduto int) map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for _, l := range linhas {
		if colProduto >= len(l) {
			continue
		}
		t, ok := carteira.NormalizarTicker(l[colProduto])
		nome := empresa(l[colProduto])
		if !ok || nome == "" {
			continue
		}
		if m[nome] == nil {
			m[nome] = map[string]bool{}
		}
		m[nome][t] = true
	}
	return m
}

// codigoAnterior acha o código antigo numa troca: o único outro código com o
// mesmo nome de empresa. Devolve "" se não houver ou se houver mais de um.
func codigoAnterior(empresas map[string]map[string]bool, produto, ticker string) string {
	var achados []string
	for t := range empresas[empresa(produto)] {
		if t != ticker {
			achados = append(achados, t)
		}
	}
	if len(achados) != 1 {
		return ""
	}
	return achados[0]
}

// acharCabecalho procura a linha com os nomes das colunas e devolve a posição de cada uma.
func acharCabecalho(linhas [][]string) (int, map[string]int, error) {
	for i, l := range linhas {
		col := map[string]int{}
		for j, v := range l {
			col[normalizar(v)] = j
		}
		if _, ok := col["codigo de negociacao"]; ok {
			return 0, nil, errors.New("este é o extrato de Negociação; baixe o de Movimentação (Extratos > Movimentação), que também traz proventos e desdobramentos")
		}
		_, temMov := col[colMovimentacao]
		_, temProduto := col[colProduto]
		if !temMov || !temProduto {
			continue
		}
		var faltam []string
		for _, c := range colunasObrigatorias {
			if _, ok := col[c]; !ok {
				faltam = append(faltam, c)
			}
		}
		if len(faltam) > 0 {
			return 0, nil, fmt.Errorf("extrato sem as colunas: %s", strings.Join(faltam, ", "))
		}
		return i, col, nil
	}
	return 0, nil, errors.New("cabeçalho do extrato de Movimentação não encontrado (colunas Movimentação e Produto)")
}

// lerData aceita texto dd/mm/aaaa ou o número de série de data do Excel.
func lerData(s string) (time.Time, error) {
	if t, err := formato.LerData(s); err == nil {
		return t, nil
	}
	serie, err := strconv.ParseFloat(s, 64)
	if err != nil || serie < 1 {
		return time.Time{}, errors.New("data inválida")
	}
	t, err := excelize.ExcelDateToTime(serie, false)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

// numero lê o valor de uma célula. "-" e vazio voltam como ausentes.
func numero(s string) (float64, bool) {
	if s == "" || s == "-" {
		return 0, false
	}
	v, err := formato.LerNumero(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// normalizar compara textos da B3 sem depender de acento e maiúsculas.
func normalizar(s string) string { return formato.Normalizar(s) }
