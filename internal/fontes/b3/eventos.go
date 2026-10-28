package b3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"carteira/internal/formato"
)

// Endereço do serviço que alimenta a página "Empresas listadas" do site da B3.
// Não é um arquivo de dados abertos documentado: se a B3 mudar o formato,
// a leitura falha e o sistema avisa em vez de calcular errado.
//   - GetListedSupplementCompany: eventos em ações (desdobramento etc.) e o
//     nome de pregão; os proventos dele cobrem só os últimos 12 meses.
//   - GetListedCashDividends: histórico completo de proventos, por espécie.
const urlEventos = "https://sistemaswebb3-listados.b3.com.br/listedCompaniesProxy/CompanyCall/"

// Tamanho de página aceito pela B3 (com 200 ela devolve lista vazia).
const tamanhoPagina = 120

// Evento é um provento em dinheiro ou uma mudança no número de ações.
type Evento struct {
	ISIN     string    // evento em ações: ativo afetado (ON, PN ou unit têm ISINs diferentes)
	Especie  string    // provento: ON, PN, PNA, PNB, UNT...
	Tipo     string    // ex.: DIVIDENDO, JRS CAP PROPRIO, DESDOBRAMENTO, GRUPAMENTO, BONIFICACAO
	DataCom  time.Time // último dia com direito; o preço cai no pregão seguinte
	Valor    float64   // provento: R$ por ação; evento em ações: fator como a B3 publica
	Dinheiro bool      // true = provento em dinheiro
}

// FatorAcoes diz em quantas ações cada ação vira. A B3 publica desdobramento
// e bonificação em % de ações novas (100 = cada ação vira 2) e grupamento
// como multiplicador (0,1 = 10 ações viram 1).
func (e Evento) FatorAcoes() (float64, bool) {
	switch e.Tipo {
	case "DESDOBRAMENTO", "BONIFICACAO":
		return 1 + e.Valor/100, e.Valor > 0
	case "GRUPAMENTO":
		return e.Valor, e.Valor > 0
	}
	return 0, false
}

// Cliente consulta os eventos no site da B3.
type Cliente struct {
	HTTP *http.Client
	Base string // trocado nos testes
}

// Novo cria o cliente com o endereço da B3.
func Novo() *Cliente {
	return &Cliente{HTTP: &http.Client{Timeout: 30 * time.Second}, Base: urlEventos}
}

// EspecieDoTicker: o número no fim do código diz a espécie (3 = ON, 4 = PN, 11 = unit).
func EspecieDoTicker(ticker string) string {
	switch {
	case strings.HasSuffix(ticker, "11"):
		return "UNT"
	case strings.HasSuffix(ticker, "3"):
		return "ON"
	case strings.HasSuffix(ticker, "4"):
		return "PN"
	case strings.HasSuffix(ticker, "5"):
		return "PNA"
	case strings.HasSuffix(ticker, "6"):
		return "PNB"
	}
	return ""
}

type respostaProventos struct {
	Page struct {
		TotalPages int `json:"totalPages"`
	} `json:"page"`
	Results []struct {
		TypeStock       string `json:"typeStock"`
		ValueCash       string `json:"valueCash"`
		CorporateAction string `json:"corporateAction"`
		LastDatePrior   string `json:"lastDatePriorEx"`
	} `json:"results"`
}

type respostaEmpresa struct {
	Code          string `json:"code"`
	TradingName   string `json:"tradingName"`
	CashDividends []struct {
		ISIN          string `json:"isinCode"`
		Rate          string `json:"rate"`
		Label         string `json:"label"`
		LastDatePrior string `json:"lastDatePrior"`
		PaymentDate   string `json:"paymentDate"`
		RelatedTo     string `json:"relatedTo"`
	} `json:"cashDividends"`
	StockDividends []struct {
		ISIN          string `json:"isinCode"`
		Factor        string `json:"factor"`
		Label         string `json:"label"`
		LastDatePrior string `json:"lastDatePrior"`
	} `json:"stockDividends"`
}

// Eventos busca os eventos de uma empresa pelo código de 4 letras (ex.: TAEE).
// O pedido vai em base64 no próprio endereço, como faz o site da B3.
func (c *Cliente) Eventos(ctx context.Context, empresa string) ([]Evento, error) {
	var r []respostaEmpresa
	if err := c.pedir(ctx, "GetListedSupplementCompany", map[string]any{"issuingCompany": empresa, "language": "pt-br"}, &r); err != nil {
		return nil, fmt.Errorf("eventos da B3 para %s: %w", empresa, err)
	}
	if len(r) == 0 || strings.TrimSpace(r[0].TradingName) == "" {
		return nil, fmt.Errorf("eventos da B3 para %s: empresa não encontrada", empresa)
	}

	var evs []Evento
	for _, d := range r[0].StockDividends {
		if e, ok := novoEvento(d.ISIN, d.Label, d.LastDatePrior, d.Factor, false); ok {
			evs = append(evs, e)
		}
	}
	// O nome de pregão vai como a B3 devolve, com os espaços do fim.
	for pagina, total := 1, 1; pagina <= total; pagina++ {
		var p respostaProventos
		pedido := map[string]any{"language": "pt-br", "pageNumber": pagina, "pageSize": tamanhoPagina, "tradingName": r[0].TradingName}
		if err := c.pedir(ctx, "GetListedCashDividends", pedido, &p); err != nil {
			return nil, fmt.Errorf("proventos da B3 para %s: %w", empresa, err)
		}
		total = p.Page.TotalPages
		for _, d := range p.Results {
			if e, ok := novoEvento("", d.CorporateAction, d.LastDatePrior, d.ValueCash, true); ok {
				e.Especie = strings.TrimSpace(d.TypeStock)
				evs = append(evs, e)
			}
		}
	}
	return evs, nil
}

// Anuncio é um provento aprovado, com a data de pagamento.
type Anuncio struct {
	ISIN      string
	Tipo      string // DIVIDENDO, JRS CAP PROPRIO, RENDIMENTO…
	DataCom   time.Time
	Pagamento time.Time // zero = a definir
	Valor     float64   // R$ por ação, bruto
	Referente string    // ex.: "2º Trimestre/2026"
}

// Anunciados devolve os proventos aprovados nos últimos 12 meses (pagos ou não).
func (c *Cliente) Anunciados(ctx context.Context, empresa string) ([]Anuncio, error) {
	var r []respostaEmpresa
	if err := c.pedir(ctx, "GetListedSupplementCompany", map[string]any{"issuingCompany": empresa, "language": "pt-br"}, &r); err != nil {
		return nil, fmt.Errorf("proventos anunciados de %s: %w", empresa, err)
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("proventos anunciados de %s: empresa não encontrada", empresa)
	}
	var l []Anuncio
	for _, d := range r[0].CashDividends {
		e, ok := novoEvento(d.ISIN, d.Label, d.LastDatePrior, d.Rate, true)
		if !ok {
			continue
		}
		pag, _ := formato.LerData(d.PaymentDate) // vazio ou texto: a definir
		l = append(l, Anuncio{ISIN: e.ISIN, Tipo: e.Tipo, DataCom: e.DataCom, Pagamento: pag, Valor: e.Valor,
			Referente: strings.TrimSpace(d.RelatedTo)})
	}
	return l, nil
}

// EspecieDoISIN lê a espécie no ISIN: BRBBASACNOR3 = ON, BRTAEECDAM10 = unit.
func EspecieDoISIN(isin string) string {
	if len(isin) < 11 {
		return ""
	}
	meio := isin[6:11]
	switch {
	case strings.HasPrefix(meio, "CDA"):
		return "UNT"
	case meio == "ACNOR":
		return "ON"
	case meio == "ACNPR":
		return "PN"
	case meio == "ACNPA":
		return "PNA"
	case meio == "ACNPB":
		return "PNB"
	}
	return ""
}

func (c *Cliente) pedir(ctx context.Context, metodo string, pedido any, destino any) error {
	j, _ := json.Marshal(pedido)
	url := c.Base + metodo + "/" + base64.StdEncoding.EncodeToString(j)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(destino); err != nil {
		return fmt.Errorf("resposta em formato inesperado: %w", err)
	}
	return nil
}

// novoEvento descarta linhas sem data ou valor (a B3 às vezes publica
// eventos ainda sem data definida).
func novoEvento(isin, tipo, data, valor string, dinheiro bool) (Evento, bool) {
	d, err := formato.LerData(data)
	if err != nil {
		return Evento{}, false
	}
	v, err := formato.LerNumero(valor)
	if err != nil || v <= 0 {
		return Evento{}, false
	}
	return Evento{ISIN: strings.TrimSpace(isin), Tipo: strings.TrimSpace(tipo), DataCom: d, Valor: v, Dinheiro: dinheiro}, true
}
