package main

// Tela web (Fase 5): um servidor HTTP que devolve, em JSON, os mesmos dados
// que os comandos do terminal imprimem, e a página que mostra esses dados.
// Por enquanto só atende neste PC (127.0.0.1); login e acesso pela internet
// vêm depois.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"carteira/internal/indicadores"
)

// go:embed coloca o arquivo dentro do executável na hora de compilar:
// o programa continua sendo um arquivo só, sem pasta de página ao lado.
//
//go:embed web/index.html
var paginaWeb []byte

const (
	portaPadrao   = "8080"
	limiteExtrato = 20 << 20 // 20 MB
	// Radar e indicadores podem baixar CVM e B3 na primeira vez.
	tempoMaximoPedido = 20 * time.Minute
)

// servidor atende um pedido por vez: o banco e os caches do app não foram
// feitos para uso simultâneo, e é uma pessoa só usando.
type servidor struct {
	base *app
	mu   sync.Mutex
}

func (a *app) web(args []string) error {
	porta := portaPadrao
	if len(args) == 1 {
		if _, err := strconv.Atoi(args[0]); err != nil {
			return fmt.Errorf("porta inválida %q", args[0])
		}
		porta = args[0]
	} else if len(args) > 1 {
		return errors.New("uso: carteira web [PORTA]")
	}

	s := &servidor{base: a}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.pagina)
	mux.HandleFunc("GET /api/posicao", s.json(func(ctx context.Context, a *app, r *http.Request) (any, error) {
		return a.dadosPosicao(ctx)
	}))
	mux.HandleFunc("GET /api/proventos", s.json(func(ctx context.Context, a *app, r *http.Request) (any, error) {
		ano, _ := strconv.Atoi(r.URL.Query().Get("ano"))
		return a.dadosProventos(ano)
	}))
	mux.HandleFunc("GET /api/agenda", s.json(func(ctx context.Context, a *app, r *http.Request) (any, error) {
		return a.dadosAgenda(ctx)
	}))
	mux.HandleFunc("GET /api/indicadores", s.json(func(ctx context.Context, a *app, r *http.Request) (any, error) {
		return a.dadosIndicadores(ctx, r.URL.Query().Get("ativos"))
	}))
	mux.HandleFunc("GET /api/radar", s.json(func(ctx context.Context, a *app, r *http.Request) (any, error) {
		d, err := a.dadosRadar(ctx)
		if err != nil && len(d.Notas) > 0 {
			err = fmt.Errorf("%w (%s)", err, strings.Join(d.Notas, "; "))
		}
		return d, err
	}))
	mux.HandleFunc("POST /api/importar", s.json(s.importar))

	// Só neste PC: outro aparelho da rede não consegue abrir.
	endereco := net.JoinHostPort("127.0.0.1", porta)
	fmt.Fprintf(a.saida, "Tela da carteira em http://localhost:%s (Ctrl+C para parar)\n", porta)
	srv := &http.Server{Addr: endereco, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

func (s *servidor) pagina(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(paginaWeb)
}

// novoApp cria um app limpo para cada pedido: os caches (séries de preços,
// Tesouro) são de uma execução só, como no terminal.
func (s *servidor) novoApp() *app {
	b := s.base
	return &app{banco: b.banco, brapi: b.brapi, saida: os.Stdout, agora: time.Now, dirDados: b.dirDados, b3: b.b3, cripto: b.cripto}
}

type tratador func(ctx context.Context, a *app, r *http.Request) (any, error)

// json roda o tratador (um por vez) e responde em JSON; erro vira {"erro": "..."}.
func (s *servidor) json(f tratador) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !mesmaOrigem(r) {
			http.Error(w, "origem não permitida", http.StatusForbidden)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), tempoMaximoPedido)
		defer cancel()

		inicio := time.Now()
		dados, err := f(ctx, s.novoApp(), r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(inicio).Round(time.Millisecond))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"erro": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(dados)
	}
}

// mesmaOrigem barra outro site aberto no navegador de mandar um extrato
// para cá pelas costas (o navegador sempre informa a origem num POST).
func mesmaOrigem(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// importar recebe o .xlsx do formulário, grava num temporário e importa.
func (s *servidor) importar(ctx context.Context, a *app, r *http.Request) (any, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, limiteExtrato)
	arq, cab, err := r.FormFile("extrato")
	if err != nil {
		return nil, fmt.Errorf("arquivo não recebido: %w", err)
	}
	defer arq.Close()
	if !strings.EqualFold(filepath.Ext(cab.Filename), ".xlsx") {
		return nil, errors.New("o arquivo precisa ser o extrato em Excel (.xlsx); o PDF não traz a coluna Entrada/Saída")
	}
	tmp, err := os.CreateTemp("", "movimentacao-*.xlsx")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, arq); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return a.importarArquivo(tmp.Name())
}

// ---------- indicadores para a tela ----------

// celulaIndicador é um valor da tabela com a memória de cálculo (painel ⓘ).
type celulaIndicador struct {
	Texto   string       `json:"texto"` // como na tabela do terminal
	OK      bool         `json:"ok"`
	Motivo  string       `json:"motivo,omitempty"`
	Periodo string       `json:"periodo"`
	Longo   string       `json:"longo,omitempty"` // valor por extenso (R$ 579,4 mi)
	Partes  []parteTexto `json:"partes,omitempty"`
	Nota    string       `json:"nota,omitempty"`
}

type parteTexto struct {
	Nome   string `json:"nome"`
	Valor  string `json:"valor"`
	Origem string `json:"origem,omitempty"`
}

type linhaIndicador struct {
	Chave   string            `json:"chave"`
	Nome    string            `json:"nome"`
	Unidade string            `json:"unidade"`
	Formula string            `json:"formula"`
	ComoLer string            `json:"comoLer"`
	Fonte   string            `json:"fonte"`
	Celulas []celulaIndicador `json:"celulas"`
}

type ativoIndicadores struct {
	Ticker        string           `json:"ticker"`
	Nome          string           `json:"nome"`
	UltimoBalanco string           `json:"ultimoBalanco"`
	Financeira    bool             `json:"financeira"`
	Colunas       []string         `json:"colunas"`
	Linhas        []linhaIndicador `json:"linhas"`
	Avisos        []string         `json:"avisos"`
}

type dadosIndicadores struct {
	Ativos []ativoIndicadores `json:"ativos"`
	Notas  []string           `json:"notas"`
}

// dadosIndicadores: ativos separados por vírgula ou espaço; vazio = carteira + observação.
func (a *app) dadosIndicadores(ctx context.Context, lista string) (dadosIndicadores, error) {
	var d dadosIndicadores
	tickers, err := a.listaTickers(strings.FieldsFunc(lista, func(r rune) bool { return r == ',' || r == ' ' }))
	if err != nil {
		return d, err
	}
	if len(tickers) == 0 {
		return d, errors.New("nenhum ativo: informe um código (ex.: GRND3) ou importe a carteira")
	}
	res, notas, err := a.calcularIndicadores(ctx, tickers)
	if err != nil {
		return d, err
	}
	d.Notas = notas
	fontes := map[string]string{}
	for _, def := range indicadores.Lista {
		fontes[def.Chave] = a.fonteIndicador(def.Chave)
	}
	for _, r := range res {
		at := ativoIndicadores{Ticker: r.Ticker, Nome: r.Nome, Financeira: r.Financeira, Avisos: r.Avisos}
		if !r.UltimoBalanco.IsZero() {
			at.UltimoBalanco = r.UltimoBalanco.Format("02/01/2006")
		}
		for _, c := range r.Colunas {
			at.Colunas = append(at.Colunas, c.Rotulo)
		}
		for _, def := range indicadores.Lista {
			l := linhaIndicador{Chave: def.Chave, Nome: def.Nome, Unidade: def.Unidade, Formula: def.Formula, ComoLer: def.ComoLer, Fonte: fontes[def.Chave]}
			for _, c := range r.Colunas {
				v := c.Valores[def.Chave]
				cel := celulaIndicador{Texto: formatarValor(v, def.Unidade), OK: v.OK, Motivo: v.Motivo, Periodo: c.Periodo, Nota: v.Nota}
				if v.Periodo != "" {
					cel.Periodo = v.Periodo
				}
				if v.OK {
					cel.Longo = formatarValorLongo(v.Numero, def.Unidade)
				}
				for _, p := range v.Partes {
					cel.Partes = append(cel.Partes, parteTexto{Nome: p.Nome, Valor: textoParte(p), Origem: p.Origem})
				}
				l.Celulas = append(l.Celulas, cel)
			}
			at.Linhas = append(at.Linhas, l)
		}
		d.Ativos = append(d.Ativos, at)
	}
	return d, nil
}
