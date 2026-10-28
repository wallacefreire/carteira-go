// Package cvm baixa e lê os dados abertos da CVM: balanços anuais (DFP),
// trimestrais (ITR) e o cadastro com os códigos de negociação (FCA).
//
// Os arquivos ficam em https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/, um .zip
// por tipo e ano, com CSVs separados por ";" em Latin-1. Cada .zip traz todas
// as empresas, então o mesmo download serve para a carteira e para o radar.
package cvm

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

	"carteira/internal/fontes/baixar"
	"carteira/internal/formato"
)

const urlBase = "https://dados.cvm.gov.br/dados/CIA_ABERTA/DOC/"

// Arquivo é um .zip da CVM, ex.: DFP de 2025.
type Arquivo struct {
	Tipo string // DFP, ITR ou FCA
	Ano  int
}

// Nome é o nome do .zip, ex.: dfp_cia_aberta_2025.zip.
func (a Arquivo) Nome() string {
	return fmt.Sprintf("%s_cia_aberta_%d.zip", strings.ToLower(a.Tipo), a.Ano)
}

// URL é o endereço do .zip no portal da CVM.
func (a Arquivo) URL() string { return urlBase + a.Tipo + "/DADOS/" + a.Nome() }

// Necessarios lista os arquivos para 5 anos de balanço + os 12 últimos meses:
// DFP dos 6 últimos anos (o ano mais recente pode ainda não ter saído),
// ITR do ano atual e do anterior, e o FCA dos dois anos.
func Necessarios(hoje time.Time) []Arquivo {
	y := hoje.Year()
	var l []Arquivo
	for a := y - 6; a <= y-1; a++ {
		l = append(l, Arquivo{"DFP", a})
	}
	return append(l, Arquivo{"ITR", y - 1}, Arquivo{"ITR", y}, Arquivo{"FCA", y - 1}, Arquivo{"FCA", y})
}

// Baixar salva no diretório os arquivos que faltam ou que estão velhos.
// Arquivos dos 2 últimos anos mudam toda semana (a CVM republica) e são
// baixados de novo se tiverem mais de 7 dias; os mais antigos, só se faltarem.
// Devolve os caminhos de todos os arquivos.
func Baixar(ctx context.Context, cli *http.Client, dir string, arqs []Arquivo, hoje time.Time, progresso func(string)) (map[Arquivo]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	caminhos := map[Arquivo]string{}
	for _, a := range arqs {
		caminho := filepath.Join(dir, a.Nome())
		caminhos[a] = caminho
		info, err := os.Stat(caminho)
		recente := a.Ano >= hoje.Year()-1
		if err == nil && (!recente || hoje.Sub(info.ModTime()) < 7*24*time.Hour) {
			continue
		}
		if progresso != nil {
			progresso("baixando " + a.Nome())
		}
		if err := baixar.Arquivo(ctx, cli, a.URL(), caminho); err != nil {
			// O ano mais novo pode ainda não existir; sem ele, os cálculos usam o anterior.
			if errors.Is(err, baixar.ErrNaoExiste) {
				delete(caminhos, a)
				continue
			}
			return nil, fmt.Errorf("baixar %s: %w", a.Nome(), err)
		}
	}
	return caminhos, nil
}

// Conta é um valor de uma demonstração, já com nome canônico (Chave).
type Conta struct {
	CNPJ         string
	Consolidado  bool   // false = demonstração individual (só a controladora)
	Demonstracao string // DRE, BPA, BPP ou DVA
	DtRefer      time.Time
	Versao       int
	DtIni        time.Time // zero no balanço patrimonial (saldo numa data)
	DtFim        time.Time
	Chave        string // nome canônico, ex.: "lucro", "pl", "ebit"
	Codigo       string // código da conta na CVM, ex.: 3.11
	Descricao    string
	Valor        float64 // em reais (escala já aplicada); LPA em R$ por ação
}

// Composicao é o número de ações da empresa numa data.
type Composicao struct {
	CNPJ                string
	DtRefer             time.Time
	Versao              int
	Ordinarias          float64
	Preferenciais       float64
	TesourariaOrdinaria float64
	TesourariaPref      float64
}

// Total é o número de ações fora da tesouraria.
func (c Composicao) Total() float64 {
	return c.Ordinarias + c.Preferenciais - c.TesourariaOrdinaria - c.TesourariaPref
}

// Ticker liga um código de negociação à empresa.
type Ticker struct {
	Codigo          string
	CNPJ            string
	Nome            string
	Tipo            string  // como vem da CVM: Ações Ordinárias, Ações Preferenciais, Units
	AcoesPorUnidade float64 // 1 para ON/PN; soma da composição para units (TAEE11 = 3); 0 se desconhecido
}

var reArquivoDemonstracao = regexp.MustCompile(`^(dfp|itr)_cia_aberta_(DRE|BPA|BPP|DVA)_(con|ind)_\d{4}\.csv$`)

// LerBalancos lê um .zip de DFP ou ITR e devolve só as contas usadas nos
// indicadores, além da composição do capital.
func LerBalancos(caminho string) ([]Conta, []Composicao, error) {
	z, err := zip.OpenReader(caminho)
	if err != nil {
		return nil, nil, fmt.Errorf("abrir %s: %w", filepath.Base(caminho), err)
	}
	defer z.Close()

	var contas []Conta
	var comps []Composicao
	for _, f := range z.File {
		switch {
		case reArquivoDemonstracao.MatchString(f.Name):
			m := reArquivoDemonstracao.FindStringSubmatch(f.Name)
			cs, err := lerDemonstracao(f, m[2], m[3] == "con")
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			contas = append(contas, cs...)
		case strings.Contains(f.Name, "_composicao_capital_"):
			cs, err := lerComposicao(f)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			comps = append(comps, cs...)
		}
	}
	return contas, comps, nil
}

// abrirCSV devolve um leitor do CSV dentro do zip e o índice de cada coluna.
func abrirCSV(f *zip.File) (*csv.Reader, map[string]int, io.Closer, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, nil, nil, err
	}
	r := csv.NewReader(charmap.ISO8859_1.NewDecoder().Reader(rc))
	r.Comma = ';'
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	cab, err := r.Read()
	if err != nil {
		rc.Close()
		return nil, nil, nil, err
	}
	col := map[string]int{}
	for i, c := range cab {
		col[strings.TrimPrefix(strings.TrimSpace(c), string(rune(0xFEFF)))] = i
	}
	return r, col, rc, nil
}

func lerDemonstracao(f *zip.File, demonstracao string, consolidado bool) ([]Conta, error) {
	r, col, rc, err := abrirCSV(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	for _, c := range []string{"CNPJ_CIA", "DT_REFER", "VERSAO", "ESCALA_MOEDA", "ORDEM_EXERC", "DT_FIM_EXERC", "CD_CONTA", "DS_CONTA", "VL_CONTA"} {
		if _, ok := col[c]; !ok {
			return nil, fmt.Errorf("coluna %s não encontrada", c)
		}
	}
	saldo := demonstracao == "BPA" || demonstracao == "BPP"

	var contas []Conta
	for {
		l, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		campo := func(n string) string {
			i, ok := col[n]
			if !ok || i >= len(l) {
				return ""
			}
			return l[i]
		}
		// ÚLTIMO = exercício do documento. PENÚLTIMO só interessa no balanço
		// patrimonial: dá o saldo do fim do ano anterior (para médias).
		ordem := campo("ORDEM_EXERC")
		if ordem != "ÚLTIMO" && !(saldo && ordem == "PENÚLTIMO") {
			continue
		}
		codigo, desc := campo("CD_CONTA"), campo("DS_CONTA")
		chave := Chave(demonstracao, codigo, desc)
		if chave == "" {
			continue
		}
		valor, err := strconv.ParseFloat(campo("VL_CONTA"), 64)
		if err != nil {
			continue
		}
		if chave != "lpa" && campo("ESCALA_MOEDA") == "MIL" {
			valor *= 1000
		}
		c := Conta{
			CNPJ: campo("CNPJ_CIA"), Consolidado: consolidado, Demonstracao: demonstracao,
			Chave: chave, Codigo: codigo, Descricao: desc, Valor: valor,
		}
		c.Versao, _ = strconv.Atoi(campo("VERSAO"))
		if c.DtRefer, err = lerData(campo("DT_REFER")); err != nil {
			continue
		}
		if c.DtFim, err = lerData(campo("DT_FIM_EXERC")); err != nil {
			continue
		}
		if !saldo {
			if c.DtIni, err = lerData(campo("DT_INI_EXERC")); err != nil {
				continue
			}
		}
		contas = append(contas, c)
	}
	return contas, nil
}

// Chave dá o nome canônico de uma conta, ou "" se ela não é usada.
// O código sozinho não basta: banco e seguradora usam os mesmos números para
// outras contas (no banco, 3.05 é o resultado antes dos tributos, não o EBIT).
func Chave(demonstracao, codigo, descricao string) string {
	d := formato.Normalizar(descricao)
	nivel := strings.Count(codigo, ".") + 1
	switch demonstracao {
	case "DRE":
		switch {
		case codigo == "3.01":
			return "receita"
		case codigo == "3.05" && d == "resultado antes do resultado financeiro e dos tributos":
			return "ebit"
		case codigo == "3.11":
			return "lucro"
		case codigo == "3.11.01":
			return "lucro_controladora"
		case strings.HasPrefix(codigo, "3.99"):
			return "lpa"
		}
	case "BPA":
		switch {
		case codigo == "1.01" && d == "ativo circulante":
			return "ativo_circulante"
		case codigo == "1.01.01":
			return "caixa"
		case codigo == "1.01.02" && strings.HasPrefix(d, "aplicac"):
			return "aplicacoes"
		}
	case "BPP":
		switch {
		case codigo == "2.01" && d == "passivo circulante":
			return "passivo_circulante"
		case codigo == "2.01.04" && strings.HasPrefix(d, "emprestimos"):
			return "emprestimos_cp"
		case codigo == "2.02.01" && strings.HasPrefix(d, "emprestimos"):
			return "emprestimos_lp"
		case nivel == 2 && strings.HasPrefix(d, "patrimonio liquido"):
			return "pl"
		case nivel == 3 && strings.Contains(d, "nao controladores"):
			return "pl_nao_controladores"
		}
	case "DVA":
		if nivel == 3 && d == "depreciacao, amortizacao e exaustao" {
			return "da"
		}
	}
	return ""
}

func lerComposicao(f *zip.File) ([]Composicao, error) {
	r, col, rc, err := abrirCSV(f)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var comps []Composicao
	for {
		l, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		campo := func(n string) string {
			i, ok := col[n]
			if !ok || i >= len(l) {
				return ""
			}
			return l[i]
		}
		num := func(n string) float64 { v, _ := strconv.ParseFloat(campo(n), 64); return v }
		c := Composicao{
			CNPJ:                campo("CNPJ_CIA"),
			Ordinarias:          num("QT_ACAO_ORDIN_CAP_INTEGR"),
			Preferenciais:       num("QT_ACAO_PREF_CAP_INTEGR"),
			TesourariaOrdinaria: num("QT_ACAO_ORDIN_TESOURO"),
			TesourariaPref:      num("QT_ACAO_PREF_TESOURO"),
		}
		c.Versao, _ = strconv.Atoi(campo("VERSAO"))
		if c.DtRefer, err = lerData(campo("DT_REFER")); err != nil {
			continue
		}
		comps = append(comps, c)
	}
	return comps, nil
}

var reNumero = regexp.MustCompile(`\d+`)

// LerTickers lê os códigos de negociação de ações e units do FCA.
func LerTickers(caminho string) ([]Ticker, error) {
	z, err := zip.OpenReader(caminho)
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", filepath.Base(caminho), err)
	}
	defer z.Close()

	for _, f := range z.File {
		if !strings.Contains(f.Name, "_valor_mobiliario_") {
			continue
		}
		r, col, rc, err := abrirCSV(f)
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		// A mesma empresa pode ter várias versões do formulário; vale a maior.
		type linha struct {
			t      Ticker
			versao int
		}
		porCodigo := map[string]linha{}
		for {
			l, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			campo := func(n string) string {
				i, ok := col[n]
				if !ok || i >= len(l) {
					return ""
				}
				return strings.TrimSpace(l[i])
			}
			codigo := strings.ToUpper(campo("Codigo_Negociacao"))
			tipo := campo("Valor_Mobiliario")
			if codigo == "" || campo("Mercado") != "Bolsa" || campo("Data_Fim_Negociacao") != "" {
				continue
			}
			t := Ticker{Codigo: codigo, CNPJ: campo("CNPJ_Companhia"), Nome: campo("Nome_Empresarial"), Tipo: tipo}
			switch formato.Normalizar(tipo) {
			case "acoes ordinarias", "acoes preferenciais":
				t.AcoesPorUnidade = 1
			case "units":
				for _, n := range reNumero.FindAllString(campo("Composicao_BDR_Unit"), -1) {
					v, _ := strconv.ParseFloat(n, 64)
					t.AcoesPorUnidade += v
				}
			default:
				continue
			}
			versao, _ := strconv.Atoi(campo("Versao"))
			if atual, ok := porCodigo[codigo]; ok && atual.versao > versao {
				continue
			}
			porCodigo[codigo] = linha{t, versao}
		}
		var tickers []Ticker
		for _, l := range porCodigo {
			tickers = append(tickers, l.t)
		}
		return tickers, nil
	}
	return nil, errors.New("arquivo de valores mobiliários não encontrado no FCA")
}

func lerData(s string) (time.Time, error) { return time.Parse("2006-01-02", strings.TrimSpace(s)) }
