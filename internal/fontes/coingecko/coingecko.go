// Package coingecko busca a cotação do bitcoin em reais na API pública da
// CoinGecko (sem chave; limite de cerca de 30 consultas por minuto).
package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Histórico diário em reais; o plano grátis vai até 365 dias para trás.
const urlHistorico = "https://api.coingecko.com/api/v3/coins/bitcoin/market_chart?vs_currency=brl&interval=daily&days="

// MaxDiasHistorico é o máximo de dias que a API grátis devolve.
const MaxDiasHistorico = 365

const urlPreco = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=brl&include_last_updated_at=true"

// Cliente consulta a CoinGecko.
type Cliente struct {
	HTTP         *http.Client
	URL          string // trocado nos testes
	URLHistorico string // trocado nos testes
}

// Novo cria o cliente com o endereço da CoinGecko.
func Novo() *Cliente {
	return &Cliente{HTTP: &http.Client{Timeout: 20 * time.Second}, URL: urlPreco, URLHistorico: urlHistorico}
}

// Bitcoin devolve o preço de 1 BTC em reais e o horário da cotação.
func (c *Cliente) Bitcoin(ctx context.Context) (float64, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return 0, time.Time{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("CoinGecko: HTTP %d", resp.StatusCode)
	}
	var r struct {
		Bitcoin struct {
			BRL           float64 `json:"brl"`
			LastUpdatedAt int64   `json:"last_updated_at"`
		} `json:"bitcoin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, time.Time{}, fmt.Errorf("CoinGecko: resposta inesperada: %w", err)
	}
	if r.Bitcoin.BRL <= 0 {
		return 0, time.Time{}, fmt.Errorf("CoinGecko: sem preço do bitcoin")
	}
	return r.Bitcoin.BRL, time.Unix(r.Bitcoin.LastUpdatedAt, 0), nil
}

// Preco é o preço do bitcoin em reais num dia.
type Preco struct {
	Dia   time.Time // UTC, só a data
	Valor float64
}

// HistoricoBitcoin devolve um preço por dia dos últimos dias (no máximo 365),
// em ordem. O ponto de cada dia é o de 00:00 UTC; o último pode ser o de agora.
func (c *Cliente) HistoricoBitcoin(ctx context.Context, dias int) ([]Preco, error) {
	dias = min(max(dias, 1), MaxDiasHistorico)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URLHistorico+strconv.Itoa(dias), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CoinGecko (histórico): HTTP %d", resp.StatusCode)
	}
	// Resposta: {"prices":[[1785024000000, 327469.91], ...]} (milissegundos, preço)
	var r struct {
		Prices [][2]float64 `json:"prices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("CoinGecko (histórico): resposta inesperada: %w", err)
	}
	var res []Preco
	for _, p := range r.Prices {
		t := time.UnixMilli(int64(p[0])).UTC()
		dia := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		if n := len(res); n > 0 && res[n-1].Dia.Equal(dia) {
			res[n-1].Valor = p[1] // o último ponto pode repetir o dia (preço de agora)
			continue
		}
		res = append(res, Preco{Dia: dia, Valor: p[1]})
	}
	return res, nil
}
