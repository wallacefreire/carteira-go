package radar

import (
	"testing"

	"carteira/internal/indicadores"
)

func ok(v float64) indicadores.Valor { return indicadores.Valor{Numero: v, OK: true} }

func resultado(lucros []float64, pl, roe, roic, beta, correl indicadores.Valor) indicadores.Resultado {
	r := indicadores.Resultado{Ticker: "TEST3", ValorMercado: 2e9}
	for i, l := range lucros {
		r.Colunas = append(r.Colunas, indicadores.Coluna{Rotulo: string(rune('A' + i)), Valores: map[string]indicadores.Valor{"lucro": ok(l), "roe": roe, "roic": roic}})
	}
	r.Colunas = append(r.Colunas,
		indicadores.Coluna{Rotulo: "12m", Valores: map[string]indicadores.Valor{"pl": pl, "beta": beta, "correlacao": correl, "dl_ebitda": ok(1), "pvp": ok(1.5)}},
		indicadores.Coluna{Rotulo: "Média 5a", Valores: map[string]indicadores.Valor{"roe": roe, "roic": roic}},
	)
	return r
}

func TestAvaliar(t *testing.T) {
	r := resultado([]float64{1, 2, 3, 4, 5}, ok(8), ok(0.20), ok(0.15), ok(0.7), ok(0.3))
	av := Avaliar(r, Padrao)
	if av.Cumpridos != 8 || av.Total != 8 || av.HistoricoCurto {
		t.Errorf("tudo bom devia cumprir 8/8: %+v", av)
	}

	// Banco (ROIC não se aplica), com um prejuízo, P/L alto e sem correlação.
	banco := resultado([]float64{1, -2, 3}, ok(25), ok(0.15),
		indicadores.Valor{Motivo: "não se aplica a bancos e seguradoras"}, ok(1.5), indicadores.Valor{Motivo: "sem dado"})
	av = Avaliar(banco, Padrao)
	if av.Total != 7 || av.Cumpridos != 3 || !av.HistoricoCurto {
		t.Errorf("banco: cumpridos %d de %d, curto %v", av.Cumpridos, av.Total, av.HistoricoCurto)
	}
	if av.Criterios[0].Valor != "1 prej." || av.Criterios[3].Valor != "n/a" {
		t.Errorf("critérios: %+v", av.Criterios)
	}

	// P/L negativo (prejuízo em 12 meses) não passa.
	if av := Avaliar(resultado([]float64{1}, ok(-5), ok(0.2), ok(0.2), ok(1), ok(0.1)), Padrao); av.Criterios[1].OK {
		t.Error("P/L negativo não pode passar")
	}
}

func TestOrdenar(t *testing.T) {
	// Mesma nota: a de ROIC maior, P/L menor e beta menor vem primeiro.
	boa := Avaliar(resultado([]float64{1, 2, 3, 4, 5}, ok(5), ok(0.2), ok(0.2), ok(0.5), ok(0.3)), Padrao)
	boa.Ticker = "BOA3"
	media := Avaliar(resultado([]float64{1, 2, 3, 4, 5}, ok(9), ok(0.2), ok(0.2), ok(0.9), ok(0.3)), Padrao)
	media.Ticker = "MEDI3"
	ruim := Avaliar(resultado([]float64{1, -2, 3, 4, 5}, ok(4), ok(0.2), ok(0.2), ok(0.4), ok(0.3)), Padrao)
	ruim.Ticker = "RUIM3"
	avs := []Avaliacao{ruim, media, boa}
	Ordenar(avs)
	if avs[0].Ticker != "BOA3" || avs[1].Ticker != "MEDI3" || avs[2].Ticker != "RUIM3" {
		t.Errorf("ordem: %s, %s, %s", avs[0].Ticker, avs[1].Ticker, avs[2].Ticker)
	}
}
