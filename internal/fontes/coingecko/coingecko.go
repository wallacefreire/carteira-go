// Package coingecko busca a cotação do bitcoin em reais na API pública da
// CoinGecko (sem chave; limite de cerca de 30 consultas por minuto).
package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const urlPreco = "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=brl&include_last_updated_at=true"

// Cliente consulta a CoinGecko.
type Cliente struct {
	HTTP *http.Client
	URL  string // trocado nos testes
}

// Novo cria o cliente com o endereço da CoinGecko.
func Novo() *Cliente {
	return &Cliente{HTTP: &http.Client{Timeout: 20 * time.Second}, URL: urlPreco}
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
