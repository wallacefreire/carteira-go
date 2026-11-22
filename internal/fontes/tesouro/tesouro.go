// Package tesouro lê os preços oficiais do Tesouro Direto publicados no
// Tesouro Transparente: um CSV único com o preço de todos os títulos em
// todos os dias úteis desde 2002.
package tesouro

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"carteira/internal/formato"
)

// URL do CSV "Preços e taxas históricas dos títulos negociados no Tesouro Direto".
const URL = "https://www.tesourotransparente.gov.br/ckan/dataset/df56aa42-484a-4a59-8184-7676580c81e3/resource/796d2059-14e9-44e3-80c9-2d9e30b405c1/download/PrecoTaxaTesouroDireto.csv"

// Preco é o preço de um título numa data.
type Preco struct {
	Titulo     string // como no extrato da B3: tipo + ano do vencimento ("Tesouro Selic 2031")
	Vencimento time.Time
	DataBase   time.Time
	PUVenda    float64 // quanto o Tesouro paga para recomprar: valor de mercado da posição
	PUCompra   float64 // quanto custa comprar
}

// Ler devolve o preço mais recente de cada título.
func Ler(caminho string) (map[string]Preco, error) {
	precos := map[string]Preco{}
	err := percorrer(caminho, func(p Preco) {
		if atual, ok := precos[p.Titulo]; !ok || p.DataBase.After(atual.DataBase) {
			precos[p.Titulo] = p
		}
	})
	return precos, err
}

// Historico devolve todos os preços dos títulos pedidos, em ordem de data.
func Historico(caminho string, titulos map[string]bool) (map[string][]Preco, error) {
	hist := map[string][]Preco{}
	err := percorrer(caminho, func(p Preco) {
		if titulos[p.Titulo] {
			hist[p.Titulo] = append(hist[p.Titulo], p)
		}
	})
	for _, l := range hist {
		slices.SortFunc(l, func(a, b Preco) int { return a.DataBase.Compare(b.DataBase) })
	}
	return hist, err
}

// percorrer lê o CSV e chama f para cada linha válida. Colunas:
// Tipo Titulo;Data Vencimento;Data Base;Taxa Compra Manha;Taxa Venda Manha;PU Compra Manha;PU Venda Manha;PU Base Manha
func percorrer(caminho string, f func(Preco)) error {
	arq, err := os.Open(caminho)
	if err != nil {
		return err
	}
	defer arq.Close()
	r := csv.NewReader(arq)
	r.Comma = ';'
	cab, err := r.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, c := range cab {
		col[strings.TrimSpace(c)] = i
	}
	for _, c := range []string{"Tipo Titulo", "Data Vencimento", "Data Base", "PU Compra Manha", "PU Venda Manha"} {
		if _, ok := col[c]; !ok {
			return fmt.Errorf("CSV do Tesouro sem a coluna %q: o formato mudou", c)
		}
	}
	for {
		l, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		venc, err1 := formato.LerData(l[col["Data Vencimento"]])
		base, err2 := formato.LerData(l[col["Data Base"]])
		venda, err3 := formato.LerNumero(l[col["PU Venda Manha"]])
		compra, _ := formato.LerNumero(l[col["PU Compra Manha"]])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		f(Preco{
			Titulo:     strings.TrimSpace(l[col["Tipo Titulo"]]) + " " + strconv.Itoa(venc.Year()),
			Vencimento: venc, DataBase: base, PUVenda: venda, PUCompra: compra,
		})
	}
}
