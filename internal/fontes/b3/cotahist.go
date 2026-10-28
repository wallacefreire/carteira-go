// Package b3 lê o histórico oficial de preços da B3 (COTAHIST) e os eventos
// de cada empresa (proventos, desdobramentos, grupamentos e bonificações).
package b3

import (
	"archive/zip"
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// URLCotahist é o endereço do arquivo anual com todos os pregões do ano.
func URLCotahist(ano int) string {
	return "https://bvmf.bmfbovespa.com.br/InstDados/SerHist/" + NomeCotahist(ano)
}

// NomeCotahist é o nome do .zip, ex.: COTAHIST_A2026.ZIP.
func NomeCotahist(ano int) string { return fmt.Sprintf("COTAHIST_A%d.ZIP", ano) }

// Preco é o fechamento de um ativo num pregão, sem ajuste por proventos.
type Preco struct {
	Ticker     string
	ISIN       string
	Data       time.Time
	Fechamento float64
}

// Códigos BDI guardados: ações (02 e as em situação especial 05 a 08),
// fundos imobiliários (12) e ETFs (14). Ficam de fora BDRs, opções, termo e
// mercado fracionário.
var bdiGuardados = map[string]bool{"02": true, "05": true, "06": true, "07": true, "08": true, "12": true, "14": true}

// LerCotahist lê o .zip anual do mercado à vista. O arquivo tem largura fixa
// (layout "SeriesHistoricas_Layout" da B3); cada pregão é uma linha "01".
// Posições (contando de 1): data 3-10, BDI 11-12, código 13-24, tipo de
// mercado 25-27, fechamento 109-121 (2 casas), fator de cotação 211-217, ISIN 231-242.
func LerCotahist(caminho string) ([]Preco, error) {
	z, err := zip.OpenReader(caminho)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	if len(z.File) != 1 {
		return nil, fmt.Errorf("%s: esperava 1 arquivo dentro do zip, veio %d", caminho, len(z.File))
	}
	f, err := z.File[0].Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var precos []Preco
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		l := sc.Text()
		if len(l) < 242 || l[:2] != "01" || l[24:27] != "010" || !bdiGuardados[l[10:12]] {
			continue
		}
		data, err := time.Parse("20060102", l[2:10])
		if err != nil {
			return nil, fmt.Errorf("linha %d: data %q", n, l[2:10])
		}
		centavos, err1 := strconv.ParseInt(l[108:121], 10, 64)
		fator, err2 := strconv.ParseInt(l[210:217], 10, 64)
		if err1 != nil || err2 != nil || fator <= 0 {
			return nil, fmt.Errorf("linha %d: preço ou fator inválido", n)
		}
		precos = append(precos, Preco{
			Ticker:     strings.TrimSpace(l[12:24]),
			ISIN:       l[230:242],
			Data:       data,
			Fechamento: float64(centavos) / 100 / float64(fator), // fator: preço por lote de 1 ou 1.000
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return precos, nil
}
