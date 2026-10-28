// Comando carteira: controle da carteira de investimentos no terminal.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"carteira/internal/armazenamento"
	"carteira/internal/carteira"
	"carteira/internal/fontes/b3"
	"carteira/internal/fontes/brapi"
	"carteira/internal/fontes/coingecko"
)

const ajuda = `carteira: controle de investimentos

Uso:
  carteira [posicao]                        posição, preço médio, cotação e pesos
  carteira importar <movimentacao.xlsx>     importa o extrato de Movimentação da B3
  carteira compra <ATIVO> <QTD> <PREÇO> <DATA>
  carteira venda  <ATIVO> <QTD> <PREÇO> <DATA>
  carteira ajuste <ATIVO> <QTD> <DATA>      muda só a quantidade (desdobro, grupamento);
                                            QTD negativa tira ações
  carteira operacoes [ATIVO]                lista as operações gravadas
  carteira remover <ID>                     apaga uma operação (o ID aparece em "operacoes")
  carteira observar <ATIVO>...              põe ativos na lista de observação
  carteira desobservar <ATIVO>...           tira ativos da lista de observação
  carteira observacao                       cotação dos ativos observados
  carteira teto [PERCENTUAL]                mostra ou muda o teto por ativo (padrão 25; não vale para o Tesouro)
  carteira radar [todas]                    ações da B3 ordenadas pelo filtro (padrão: as 30 melhores)
  carteira filtro [CRITÉRIO VALOR]          mostra ou muda os limites do radar (ex.: carteira filtro roe 15)
  carteira proventos [ANO]                  proventos recebidos por mês e por ativo
  carteira agenda                           proventos anunciados que ainda vão ser pagos
  carteira indicadores [ATIVO...]           indicadores dos balanços (padrão: carteira + observação)
  carteira explicar <ATIVO> <INDICADOR>     memória de cálculo (lucro, pl, roe, roic, dl_ebitda, dl_pl,
                                            liquidez, beta, volatilidade, correlacao)
  carteira cvm                              baixa de novo os balanços da CVM (é automático 1x por semana)
  carteira precos                           baixa de novo o histórico de preços da B3 (automático 1x por semana)

Exemplos:
  carteira importar movimentacao.xlsx
  carteira compra BBAS3 100 27,50 10/05/2018
  carteira compra "Tesouro Selic 2031" 0,05 20000 03/09/2026
  carteira compra BTC 0,001 430000 01/10/2026
  carteira ajuste MGLU3 -900 02/07/2024

Variáveis de ambiente:
  BRAPI_TOKEN   token do brapi.dev, para as cotações
  CARTEIRA_DB   arquivo do banco (agora: %s)

O extrato da B3 começa em nov/2019: lance as compras anteriores com "carteira compra".
Operação importada e removida volta se o mesmo extrato for importado de novo.
`

func main() {
	if err := executar(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func caminhoBanco() (string, error) {
	if c := os.Getenv("CARTEIRA_DB"); c != "" {
		return c, nil
	}
	dir, err := os.UserConfigDir() // no Windows: %AppData%
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "carteira", "carteira.db"), nil
}

func executar(args []string, saida io.Writer) error {
	caminho, err := caminhoBanco()
	if err != nil {
		return err
	}

	cmd := "posicao"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "ajuda" || cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Fprintf(saida, ajuda, caminho)
		return nil
	}

	banco, err := armazenamento.Abrir(caminho)
	if err != nil {
		return fmt.Errorf("abrir banco %s: %w", caminho, err)
	}
	defer banco.Fechar()

	a := &app{
		banco:    banco,
		brapi:    brapi.Novo(os.Getenv("BRAPI_TOKEN")),
		saida:    saida,
		agora:    time.Now,
		dirDados: filepath.Dir(caminho),
		b3:       b3.Novo(),
		cripto:   coingecko.Novo(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	switch cmd {
	case "posicao":
		return a.posicao(ctx)
	case "importar":
		return a.importar(args)
	case "compra":
		return a.lancar(carteira.Compra, args)
	case "venda":
		return a.lancar(carteira.Venda, args)
	case "ajuste":
		return a.lancar(carteira.Ajuste, args)
	case "operacoes":
		return a.operacoes(args)
	case "remover":
		return a.remover(args)
	case "observar":
		return a.observar(args)
	case "desobservar":
		return a.desobservar(args)
	case "observacao":
		return a.observacao(ctx)
	case "cvm":
		return a.atualizarCVM(ctx)
	case "precos":
		return a.atualizarPrecos(ctx, true)
	case "indicadores":
		return a.indicadores(ctx, args)
	case "explicar":
		return a.explicar(ctx, args)
	case "proventos":
		return a.proventos(args)
	case "agenda":
		return a.agenda(ctx)
	case "radar":
		return a.radar(ctx, args)
	case "filtro":
		return a.filtro(args)
	case "teto":
		return a.mudarTeto(args)
	default:
		return fmt.Errorf("comando desconhecido %q (veja: carteira ajuda)", cmd)
	}
}
