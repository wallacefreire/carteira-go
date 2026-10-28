// Package formato escreve e lê números e datas no padrão brasileiro.
package formato

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const layoutData = "02/01/2006"

// Numero escreve v com separador de milhar "." e decimal ",", ex.: 1234.5 → "1.234,50".
func Numero(v float64, casas int) string {
	s := strconv.FormatFloat(math.Abs(v), 'f', casas, 64)
	inteiro, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	// Não escreve "-0,00" quando o valor arredondado é zero.
	if v < 0 && strings.Trim(s, "0.") != "" {
		b.WriteByte('-')
	}
	for i, r := range inteiro {
		if i > 0 && (len(inteiro)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteByte(',')
		b.WriteString(frac)
	}
	return b.String()
}

// Moeda escreve um valor em reais com 2 casas, sem o "R$".
func Moeda(v float64) string { return Numero(v, 2) }

// Quantidade escreve sem casas quando o número é inteiro (100) e com as casas
// necessárias quando não é (0,00123 BTC).
func Quantidade(v float64) string {
	s := Numero(v, 8)
	if strings.Contains(s, ",") {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ",")
	}
	return s
}

// Pct recebe uma fração (0,25) e escreve o percentual com 1 casa ("25,0%").
func Pct(fracao float64) string { return Numero(fracao*100, 1) + "%" }

// Data escreve no formato dd/mm/aaaa.
func Data(t time.Time) string { return t.Format(layoutData) }

// LerNumero aceita "1.234,56", "1234,56", "1234.56" e "R$ 27,50".
// Se houver vírgula, ela é o separador decimal e os pontos são de milhar.
func LerNumero(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "R$")
	s = strings.ReplaceAll(s, string(rune(0xA0)), "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("número inválido")
	}
	return v, nil
}

// LerData lê uma data no formato dd/mm/aaaa.
func LerData(s string) (time.Time, error) {
	return time.Parse(layoutData, strings.TrimSpace(s))
}

var semAcento = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a",
	"é", "e", "ê", "e", "í", "i",
	"ó", "o", "ô", "o", "õ", "o", "ú", "u", "ç", "c",
	string(rune(0xA0)), " ",
)

// Normalizar põe em minúsculas, tira acentos e espaços repetidos,
// para comparar textos das fontes sem depender da grafia exata.
func Normalizar(s string) string {
	return strings.Join(strings.Fields(semAcento.Replace(strings.ToLower(s))), " ")
}
