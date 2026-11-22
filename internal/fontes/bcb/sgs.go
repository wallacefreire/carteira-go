package bcb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Séries do SGS (Sistema Gerenciador de Séries Temporais) do Banco Central.
const (
	SerieCDI  = 12  // CDI diário, % ao dia, só dias úteis
	SerieIPCA = 433 // IPCA, % no mês; a data é o dia 1 do mês
)

const urlSGS = "https://api.bcb.gov.br/dados/serie/bcdata.sgs.%d/dados"

// Valor é um ponto de uma série do SGS.
type Valor struct {
	Data  time.Time // UTC, só a data
	Valor float64
}

// Serie devolve os valores de uma série do SGS entre ini e fim, em ordem.
// Séries diárias aceitam no máximo 10 anos por consulta.
func (c *Cliente) Serie(ctx context.Context, codigo int, ini, fim time.Time) ([]Valor, error) {
	q := url.Values{}
	q.Set("formato", "json")
	q.Set("dataInicial", ini.Format("02/01/2006"))
	q.Set("dataFinal", fim.Format("02/01/2006"))
	endereco := fmt.Sprintf(c.URLSGS, codigo) + "?" + q.Encode()

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
		return nil, fmt.Errorf("Banco Central (série %d): HTTP %d", codigo, resp.StatusCode)
	}
	// Resposta: [{"data":"03/11/2026","valor":"0.050788"}, ...]
	var r []struct {
		Data  string `json:"data"`
		Valor string `json:"valor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("Banco Central (série %d): resposta inesperada: %w", codigo, err)
	}
	res := make([]Valor, 0, len(r))
	for _, v := range r {
		d, err1 := time.Parse("02/01/2006", v.Data)
		x, err2 := strconv.ParseFloat(v.Valor, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("Banco Central (série %d): valor inesperado %q em %q", codigo, v.Valor, v.Data)
		}
		res = append(res, Valor{Data: d, Valor: x})
	}
	return res, nil
}
