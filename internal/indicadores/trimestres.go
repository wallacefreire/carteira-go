package indicadores

import (
	"fmt"
	"time"

	"carteira/internal/formato"
)

// ListaTrimestral são as linhas da tabela por trimestre.
var ListaTrimestral = []Def{
	{"receita", "Receita líquida", "R$", "receita do trimestre (DRE 3.01); o ITR traz o acumulado do ano: trimestre = acumulado até ele − acumulado até o anterior; 4º trimestre = ano (DFP) − 9 meses",
		"compare com o mesmo trimestre do ano anterior: muitos negócios têm sazonalidade"},
	{"ebit", "EBIT", "R$", "resultado antes do resultado financeiro e dos tributos do trimestre (DRE 3.05), pela mesma conta de acumulados",
		"o lucro da operação, antes de juros e impostos; não se aplica a bancos"},
	{"lucro", "Lucro líquido", "R$", "lucro dos acionistas da empresa no trimestre (3.11.01), pela mesma conta de acumulados",
		"um trimestre ruim não reprova sozinho; vários seguidos pedem atenção"},
	{"lucro_rec", "Lucro sem descontinuadas", "R$", "lucro do trimestre − resultado de operações descontinuadas (3.10)",
		"diferente do lucro líquido = venda ou fechamento de negócio no trimestre"},
}

// TrimestresMostrados é quantos trimestres a tabela mostra (2 anos).
const TrimestresMostrados = 8

// BuscarTrimestral devolve a definição de uma linha da tabela por trimestre.
func BuscarTrimestral(chave string) (Def, bool) {
	for _, d := range ListaTrimestral {
		if d.Chave == chave {
			return d, true
		}
	}
	return Def{}, false
}

// trimestres monta as colunas dos últimos trimestres, do mais antigo ao mais
// novo. Cada valor é a diferença entre dois acumulados do ano seguidos.
func (c calc) trimestres(anos []periodo, financeira bool) []Coluna {
	var inicios []time.Time
	for _, a := range anos[max(0, len(anos)-2):] {
		inicios = append(inicios, a.ini)
	}
	inicios = append(inicios, anos[len(anos)-1].fim.AddDate(0, 0, 1)) // ano atual

	acumulado := map[string]func(ini, fim time.Time) (float64, Parte, bool){
		"receita": func(ini, fim time.Time) (float64, Parte, bool) {
			return c.primeiro("Receita", ini, fim, fonte{"receita", true}, fonte{"receita", false})
		},
		"ebit":  c.ebit,
		"lucro": c.lucro,
		"lucro_rec": func(ini, fim time.Time) (float64, Parte, bool) {
			l, p, ok := c.lucro(ini, fim)
			if !ok {
				return 0, Parte{}, false
			}
			if d, _, okD := c.primeiro("", ini, fim, fonte{"lucro_descontinuadas", true}, fonte{"lucro_descontinuadas", false}); okD && d != 0 {
				p.Nome = "Lucro sem descontinuadas"
				p.Origem += fmt.Sprintf(" − descontinuadas (3.10) de R$ %s", formato.Moeda(d))
				p.Valor = l - d
				return l - d, p, true
			}
			p.Nome = "Lucro sem descontinuadas"
			return l, p, true
		},
	}

	var cols []Coluna
	for _, ini := range inicios {
		for q := 1; q <= 4; q++ {
			fim := ini.AddDate(0, 3*q, -1)
			if _, _, ok := c.lucro(ini, fim); !ok {
				continue // trimestre ainda não publicado
			}
			col := Coluna{
				Rotulo:  fmt.Sprintf("%dT%02d", q, fim.Year()%100),
				Periodo: formato.Data(ini.AddDate(0, 3*(q-1), 0)) + " a " + formato.Data(fim),
				Valores: map[string]Valor{},
			}
			for _, d := range ListaTrimestral {
				if d.Chave == "ebit" && financeira {
					col.Valores[d.Chave] = Valor{Motivo: motivoFinanceira}
					continue
				}
				col.Valores[d.Chave] = trimestre(acumulado[d.Chave], d.Nome, ini, fim, q)
			}
			cols = append(cols, col)
		}
	}
	if len(cols) > TrimestresMostrados {
		cols = cols[len(cols)-TrimestresMostrados:]
	}
	// Começo sem dado (ex.: falta o ITR de 2 anos atrás para o 4º trimestre): some.
	for len(cols) > 0 && !cols[0].Valores["lucro"].OK {
		cols = cols[1:]
	}
	return cols
}

// trimestre = acumulado até fim − acumulado até o trimestre anterior (no 1º, o próprio acumulado).
func trimestre(g func(ini, fim time.Time) (float64, Parte, bool), nome string, ini, fim time.Time, q int) Valor {
	v, p, ok := g(ini, fim)
	if !ok {
		return Valor{Motivo: motivoSemDado}
	}
	p.Nome = nome + " " + rotuloPeriodo(ini, fim)
	if q == 1 {
		return Valor{Numero: v, OK: true, Partes: []Parte{p}}
	}
	fimAnt := ini.AddDate(0, 3*(q-1), -1)
	ant, pAnt, ok := g(ini, fimAnt)
	if !ok {
		return Valor{Motivo: "falta o acumulado até o trimestre anterior"}
	}
	pAnt.Nome = "(−) " + nome + " " + rotuloPeriodo(ini, fimAnt)
	return Valor{Numero: v - ant, OK: true, Partes: []Parte{p, pAnt}}
}
