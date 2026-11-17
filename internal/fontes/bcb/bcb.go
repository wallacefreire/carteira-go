// Package bcb busca a cotação oficial do dólar (PTAX) na API de dados abertos
// do Banco Central (Olinda), sem chave. A PTAX de cada dia útil sai por volta
// das 13h.
package bcb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const urlBase = "https://olinda.bcb.gov.br/olinda/servico/PTAX/versao/v1/odata"

// Cliente consulta o Banco Central.
type Cliente struct {
	HTTP *http.Client
	URL  string // trocado nos testes
}

// Novo cria o cliente com o endereço do Banco Central.
func Novo() *Cliente {
	return &Cliente{HTTP: &http.Client{Timeout: 20 * time.Second}, URL: urlBase}
}

// Cotacao é a PTAX de um dia, em reais por dólar.
type Cotacao struct {
	Dia    time.Time // só a data (meia-noite, horário local)
	Compra float64
	Venda  float64
}

// Periodo devolve as PTAX publicadas entre ini e fim (só dias úteis), em ordem.
func (c *Cliente) Periodo(ctx context.Context, ini, fim time.Time) ([]Cotacao, error) {
	// A API usa datas no formato americano entre aspas simples: '11-16-2026'.
	q := url.Values{}
	q.Set("@dataInicial", "'"+ini.Format("01-02-2006")+"'")
	q.Set("@dataFinalCotacao", "'"+fim.Format("01-02-2006")+"'")
	q.Set("$format", "json")
	endereco := c.URL + "/CotacaoDolarPeriodo(dataInicial=@dataInicial,dataFinalCotacao=@dataFinalCotacao)?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Banco Central (PTAX): HTTP %d", resp.StatusCode)
	}
	var r struct {
		Value []struct {
			Compra   float64 `json:"cotacaoCompra"`
			Venda    float64 `json:"cotacaoVenda"`
			DataHora string  `json:"dataHoraCotacao"` // "2026-11-16 13:06:32.509132"
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("Banco Central (PTAX): resposta inesperada: %w", err)
	}
	var res []Cotacao
	for _, v := range r.Value {
		if len(v.DataHora) < 10 {
			return nil, fmt.Errorf("Banco Central (PTAX): data inesperada %q", v.DataHora)
		}
		dia, err := time.ParseInLocation("2006-01-02", v.DataHora[:10], time.Local)
		if err != nil {
			return nil, fmt.Errorf("Banco Central (PTAX): data inesperada %q", v.DataHora)
		}
		res = append(res, Cotacao{Dia: dia, Compra: v.Compra, Venda: v.Venda})
	}
	return res, nil
}

// Dolar devolve a PTAX do dia ou, se não houver (fim de semana, feriado ou
// antes das 13h), a do último dia útil antes dele.
func (c *Cliente) Dolar(ctx context.Context, dia time.Time) (Cotacao, error) {
	// 10 dias para trás cobrem o feriado mais longo (Carnaval).
	cs, err := c.Periodo(ctx, dia.AddDate(0, 0, -10), dia)
	if err != nil {
		return Cotacao{}, err
	}
	if len(cs) == 0 {
		return Cotacao{}, errors.New("Banco Central (PTAX): sem cotação do dólar nos 10 dias até " + dia.Format("02/01/2006"))
	}
	return cs[len(cs)-1], nil
}
