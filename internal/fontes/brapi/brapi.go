// Package brapi busca cotações na API do brapi.dev.
//
// Plano grátis (out/2026): 1 ativo por requisição, 1 requisição por vez,
// 15.000 requisições por mês, atraso de até 30 min e histórico de só 3 meses.
// Sem token, a API responde apenas PETR4, MGLU3, VALE3 e ITUB4.
package brapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// URLPadrao é o endereço da API.
const URLPadrao = "https://brapi.dev/api"

// Cliente fala com a API. Token vazio funciona só para os ativos liberados sem token.
type Cliente struct {
	Token string
	URL   string
	HTTP  *http.Client
}

// Novo cria um cliente com o endereço padrão e tempo limite de 15 s por requisição.
func Novo(token string) *Cliente {
	return &Cliente{Token: token, URL: URLPadrao, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Cotacao é o último preço de um ativo.
type Cotacao struct {
	Ticker  string
	Preco   float64
	Horario time.Time // horário do preço no mercado
}

type resposta struct {
	Results []struct {
		Symbol             string          `json:"symbol"`
		RegularMarketPrice float64         `json:"regularMarketPrice"`
		RegularMarketTime  json.RawMessage `json:"regularMarketTime"`
	} `json:"results"`
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

// Cotacao busca o preço atual de um ativo.
func (c *Cliente) Cotacao(ctx context.Context, ticker string) (Cotacao, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/quote/"+url.PathEscape(ticker), nil)
	if err != nil {
		return Cotacao{}, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Cotacao{}, fmt.Errorf("brapi: %w", err)
	}
	defer resp.Body.Close()

	corpo, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Cotacao{}, fmt.Errorf("brapi: %w", err)
	}
	var r resposta
	errJSON := json.Unmarshal(corpo, &r)
	if resp.StatusCode != http.StatusOK || r.Error {
		if r.Message != "" {
			return Cotacao{}, fmt.Errorf("brapi: HTTP %d: %s", resp.StatusCode, r.Message)
		}
		return Cotacao{}, fmt.Errorf("brapi: HTTP %d", resp.StatusCode)
	}
	if errJSON != nil {
		return Cotacao{}, fmt.Errorf("brapi: resposta inválida: %w", errJSON)
	}
	if len(r.Results) == 0 || r.Results[0].RegularMarketPrice <= 0 {
		return Cotacao{}, fmt.Errorf("brapi: sem cotação para %s", ticker)
	}

	res := r.Results[0]
	horario, err := lerHorario(res.RegularMarketTime)
	if err != nil {
		return Cotacao{}, fmt.Errorf("brapi: horário da cotação de %s: %w", ticker, err)
	}
	return Cotacao{Ticker: ticker, Preco: res.RegularMarketPrice, Horario: horario}, nil
}

// lerHorario aceita texto ISO ("2026-10-28T20:07:00.000Z") ou segundos Unix.
func lerHorario(raw json.RawMessage) (time.Time, error) {
	s := strings.Trim(string(raw), `"`)
	if s == "" || s == "null" {
		return time.Time{}, errors.New("vazio")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	seg, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("formato desconhecido %q", s)
	}
	return time.Unix(seg, 0).UTC(), nil
}
