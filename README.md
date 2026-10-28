# Carteira Go

Controle de investimentos pessoal, em Go, no terminal.

Os indicadores não são copiados de sites: o programa os calcula a partir das **fontes oficiais** (CVM e B3). Cada número guarda a sua **memória de cálculo**: de onde veio cada valor, a fórmula e o período.

```
$ carteira explicar SOJA3 pl
```

## O que faz

- **Carteira:** importa o extrato de Movimentação da B3 (todas as corretoras), calcula o preço médio pelo método da Receita e mostra posição, resultado e pesos por ativo e por classe, com alerta de teto por ativo.
- **Indicadores** (5 anos + últimos 12 meses + média): lucro, P/L, ROE, ROIC, dívida líquida/EBITDA, dívida líquida/PL e liquidez corrente, a partir dos balanços da CVM (DFP e ITR).
- **Risco:** beta, volatilidade e correlação com a carteira, com preços diários da B3 ajustados por proventos, desdobramentos e grupamentos.
- **Radar:** aplica um filtro configurável a todas as ações da B3 e ordena as aprovadas.
- **Proventos:** recebidos por mês e por ativo, e a agenda dos anunciados a receber.
- **Tesouro Direto e bitcoin:** cotados pelo Tesouro Transparente e pela CoinGecko.

## Fontes de dados

| Dado | Fonte |
|---|---|
| Balanços (DRE, balanço patrimonial, FCA) | CVM, dados abertos |
| Histórico de preços | COTAHIST da B3 (BOVA11 no lugar do Ibovespa) |
| Proventos e desdobramentos | site da B3 |
| Cotação atual das ações | brapi.dev |
| Tesouro Direto | Tesouro Transparente |
| Bitcoin | CoinGecko |
| Operações e proventos recebidos | extrato de Movimentação da B3 |

## Casos que o programa trata

- **Lucro do acionista ≠ lucro consolidado.** A Boa Safra (SOJA3) consolida o fundo SNAG11, cujo lucro é dos cotistas do fundo. O programa usa o lucro atribuído à controladora e avisa quando os dois divergem.
- **Bancos e seguradoras** não têm EBIT no formato padrão: ROIC, dívida e liquidez aparecem como "não se aplica".
- **Units** (TAEE11, SAPR11): o preço por ação considera quantas ações há em cada unit.
- **Extrato da B3:** distingue compra e venda de aluguel de ações, troca de código (WIZS3 → WIZC3) e transferência entre corretoras.

## Instalação

Requer Go 1.26 ou mais novo. Não precisa de CGO (o SQLite é em Go puro).

```
go install ./cmd/carteira
```

Para as cotações, crie um token gratuito em [brapi.dev](https://brapi.dev) e defina a variável `BRAPI_TOKEN`.

## Uso

```
carteira importar movimentacao.xlsx       importa o extrato da B3
carteira                                  posição, preço médio, cotação e pesos
carteira compra BTC 0,001 430000 01/10/2026
carteira indicadores [ATIVO...]           indicadores dos balanços
carteira explicar <ATIVO> <INDICADOR>     memória de cálculo
carteira radar                            ações da B3 ordenadas pelo filtro
carteira proventos [ANO]                  proventos recebidos
carteira agenda                           proventos a receber
carteira ajuda                            todos os comandos
```

Os dados ficam num arquivo SQLite em `%AppData%\carteira` (Windows) ou na pasta de configuração do usuário. Use `CARTEIRA_DB` para escolher outro arquivo.

## Estrutura

```
cmd/carteira/          linha de comando
internal/carteira/     operações, preço médio e pesos
internal/importacao/   extrato de Movimentação da B3 (.xlsx)
internal/indicadores/  indicadores com memória de cálculo
internal/risco/        ajuste de preços, beta, volatilidade e correlação
internal/fontes/       CVM, B3, brapi, Tesouro e CoinGecko
internal/armazenamento/ SQLite
internal/formato/      números e datas no padrão brasileiro
```

O plano do projeto e as decisões estão em [PLANO.md](PLANO.md).

## Como foi feito

Projeto de aprendizado de Go. O código foi escrito com o [Claude Code](https://claude.com/claude-code) e revisado por mim.
