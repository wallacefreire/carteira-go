# Carteira Go

Controle de investimentos pessoal do Wallace, em Go, no terminal. Os indicadores são calculados a partir de fontes oficiais (CVM, B3) e cada número guarda a sua memória de cálculo. O plano e as fases estão em [PLANO.md](PLANO.md): leia antes de começar uma fase.

## Como trabalhar aqui
- Claude escreve o código e Wallace revisa. Wallace está aprendendo Go: explique em português o que for novidade da linguagem.
- Tudo em **português**: identificadores, comentários, mensagens, testes. Mantenha esse padrão.
- Antes de afirmar algo sobre um formato de dados (CSV da CVM, extrato da B3, API), baixe uma amostra e confira. Vários bugs vieram de suposições.
- Ao terminar, rode `gofmt -l .`, `go vet ./...` e `go test ./...`.
- **Depois de cada mudança, sem perguntar:** commit (mensagem em português) e "deploy" da tela: parar o `carteira.exe` que roda a tela, `go build -o carteira.exe ./cmd/carteira` e subir `carteira.exe web` de novo (com `BRAPI_TOKEN` do registro e sem `CARTEIRA_DB`). O `git push` é com o Wallace (neste PC o push pode pedir login do GitHub).
- Para testar com dados reais, use um banco temporário: `CARTEIRA_DB=<scratchpad>/teste.db`. O banco real é `%AppData%\carteira\carteira.db`.
- Repositório git (branch `main`), espelhado em github.com/wallacefreire/carteira-go, que é **público**: extratos (`*.xlsx`), bancos (`*.db`) e o `carteira.exe` ficam fora pelo `.gitignore`. Nunca suba dados pessoais, tokens ou chaves.
- O PC principal agora é o de casa (03/11/2026): banco real em `%AppData%\carteira\carteira.db` (veio do outro PC), token do brapi no registro.

## Comandos
```
go test ./...
go run ./cmd/carteira ajuda
go run ./cmd/carteira                      # posição
go run ./cmd/carteira importar movimentacao.xlsx
go run ./cmd/carteira indicadores [ATIVO...]
go run ./cmd/carteira explicar SOJA3 pl
go run ./cmd/carteira precos               # baixa de novo o COTAHIST do ano atual
go run ./cmd/carteira proventos [ANO]
go run ./cmd/carteira agenda
go run ./cmd/carteira radar [todas]
go run ./cmd/carteira filtro [CRITÉRIO VALOR]
go run ./cmd/carteira web                  # tela em http://localhost:8080
```
`BRAPI_TOKEN` fica salvo no ambiente do usuário do Windows (registro `HKCU\Environment`). Em shell novo sem a variável, leia de lá. Nunca grave o token em arquivo.

## Estrutura
- `cmd/carteira/`: CLI (`main.go` faz o despacho, `comandos.go` a carteira, `indicadores.go` CVM e indicadores)
- `internal/carteira/`: operações, preço médio (método da Receita), pesos
- `internal/importacao/`: extrato de **Movimentação** da B3 (.xlsx)
- `internal/fontes/brapi/`: cotação atual. Plano grátis: 1 ativo por requisição, histórico de só 3 meses
- `internal/fontes/cvm/`: download e leitura de DFP, ITR e FCA (CSV em Latin-1, separado por `;`)
- `internal/fontes/b3/`: COTAHIST (preços diários, largura fixa) e eventos do site da B3 (proventos e desdobramentos)
- `internal/fontes/tesouro/`: preços do Tesouro Direto (CSV do Tesouro Transparente)
- `internal/fontes/coingecko/`: preço do bitcoin em reais
- `cmd/carteira/precos.go`: histórico da B3 e correlação; `proventos.go`: cotação por tipo de ativo, classes, `proventos` e `agenda`
- `internal/radar/`: avaliação do filtro (✓/✗ por critério); `cmd/carteira/radar.go`: comandos radar e filtro
- `internal/fontes/baixar/`: download para arquivo temporário (usado por CVM e B3)
- `internal/risco/`: ajuste de preços, retornos, beta, volatilidade e correlação
- `internal/indicadores/`: cálculo dos indicadores com memória (`Valor.Partes`); `risco.go` faz beta, volatilidade e correlação
- `internal/armazenamento/`: SQLite (`modernc.org/sqlite`, sem CGO)
- `internal/formato/`: números e datas no padrão brasileiro e `Normalizar` (sem acento)

## Regras de domínio (já decididas, não mudar sem falar com o Wallace)
- **Lucro e PL do acionista** = consolidado "atribuído à controladora" (3.11.01; PL total − não controladores). A DRE individual é só reserva: em bancos ela segue outra norma e não bate.
- Caso SOJA3: a Boa Safra consolida o fundo SNAG11, então lucro consolidado ≠ lucro do acionista. O sistema avisa quando os dois divergem mais de 20%.
- **Bancos e seguradoras** (sem a conta EBIT 3.05 padrão): ROIC, dívida e liquidez ficam "não se aplica".
- **Units**: preço por ação = preço da unit ÷ ações na unit (do FCA: TAEE11 = 3, SAPR11 = 5).
- A composição do capital da CVM às vezes vem em **milhares** (Taesa): confira com lucro ÷ LPA.
- Colunas do ano anterior podem vir **zeradas** em documento mais novo: prefira o valor diferente de zero. Antes de a empresa consolidar, use o balanço individual.
- 12 meses = último ano (DFP) + acumulado deste ano (ITR) − mesmo acumulado do ano anterior.
- Extrato da B3: "Transferência - Liquidação" é compra ou venda. "Atualização" com outro código da mesma empresa é **troca de código** (WIZS3→WIZC3, TRPL4→ISAE4). Liquidação sem preço junto de "Empréstimo" é **aluguel**, não venda. "Transferência" entre corretoras é ignorada. Tesouro fica para a Fase 3.
- Teto por ativo: 25% (configurável com `carteira teto`).
- **Preços (Fase 2b):** COTAHIST de 5 anos + o atual, só mercado à vista (BDI 02, 05–08, 12, 14). O .zip é apagado depois de gravado no banco; ano fechado vira `cotahist_<ano> = completo` na tabela config.
- **BOVA11 no lugar do Ibovespa** (o índice não está no COTAHIST).
- **P/L de cada ano** usa o fechamento **sem ajuste** do último pregão do ano e as ações da composição do mesmo ano.
- **Beta, volatilidade e correlação** usam preços **ajustados**: provento multiplica os preços até a data-com por (1 − provento ÷ preço); desdobramento, grupamento e bonificação dividem pelo fator.
- Eventos da B3: `GetListedSupplementCompany` dá desdobramento/grupamento/bonificação (por ISIN; desdobramento e bonificação em %, grupamento como multiplicador) e o nome de pregão. Os proventos dele são só de 12 meses: o histórico vem de `GetListedCashDividends` (por espécie ON/PN/UNT, `pageSize` até 120, nome de pregão com os espaços do fim).
- Beta e volatilidade por ano: retornos diários. Coluna 12m: beta e correlação com retornos semanais de 2 anos; volatilidade dos últimos 252 pregões. Correlação: carteira com os pesos de hoje, sem o próprio ativo.

- **Tesouro:** a importação pula o Tesouro até `carteira tesouro sim` (config `importar_tesouro`; no banco real já está ligado). Desde 03/11/2026 o **Tesouro IPCA+ 2032 é do Wallace** e conta na carteira; Tesouro Selic 2031 e os títulos antigos já vendidos (IPCA+ 2050, Selic 2025, 2027 e 2029) são de terceiros e estão ocultos (`carteira ocultar`). Título novo do Tesouro entra como do Wallace: se for de terceiros, ocultar.
- **Tesouro (Fase 3):** vem do extrato da B3 como Compra/Venda com o nome do título ("Tesouro Selic 2031"), que é o próprio código do ativo. Preço = PU de venda mais recente do CSV do Tesouro Transparente (~15 MB, baixado a cada 12 h em `dirDados/tesouro`). O extrato arredonda a quantidade para 0,01: o valor pode diferir do da corretora (corrigir com `carteira ajuste`).
- **BTC:** só lançamento manual (`carteira compra BTC …`); preço em reais pela CoinGecko.
- O teto por ativo **não vale para o Tesouro**. Indicadores e correlação usam só ativos da bolsa.
- **Proventos recebidos** vêm do extrato (valor líquido). **Agenda** usa os proventos anunciados no site da B3 (últimos 12 meses, com data de pagamento), quantidade na data-com pelas operações gravadas e 15% de IR no JCP.

- **Radar (Fase 4):** universo = códigos do FCA com negócio em pelo menos 90% dos pregões do último ano; um código por empresa (o mais negociado; empate: unit, PN, ON). P/L com o último fechamento do COTAHIST (sem brapi). **Nenhum critério elimina sozinho**: a nota é quantos critérios cumpre (Wallace pediu cautela nas reprovações). Limites padrão (03/11/2026, 8 critérios): lucro em todos os anos, P/L 0–15, ROE 5a ≥ 12%, ROIC 5a ≥ 8%, dív. líq./EBITDA ≤ 2, valor de mercado ≥ R$ 1 bi, beta ≤ 1,2, correlação ≤ 0,6; mudam com `carteira filtro`. Na mesma nota, a **fila** desempata (menor primeiro): 40% posição no ROIC médio de 3 anos (ROE em banco), 40% preço (75% P/L + 25% P/VP), 20% beta. EV/EBITDA, P/VP e setor (do FCA, holding conta no setor das controladas) são só informativos. Prejuízo trimestral só aparece como observação.
- **Método de longo prazo (03/11/2026):** Wallace compra e segura (sem venda anual), 6 a 8 ações de setores diferentes, de 15 a 20 ativos no total (com renda fixa, exterior e BTC). Escolha = nota máxima do radar, descendo pela fila. Revisão 1x por ano. Pendências: ROIC distorcido por caixa (PSSA3, CEBR3) e P/L errado da SANB11 (ações em milhares não detectadas; lucro de 12m pega "Reversão dos JCP" da DRE individual).
- Cuidado conhecido: ROIC e ROE explodem quando o denominador é pequeno (empresa com muito caixa, ex.: CEBR3, PSSA3, CGAS5).

## Fase 5 (em andamento)
**Feito (03/11/2026):** `carteira web [PORTA]` abre a tela em http://localhost:8080, só neste PC (127.0.0.1, sem login). `cmd/carteira/web.go` tem a API (`/api/posicao`, `proventos`, `agenda`, `indicadores?ativos=`, `radar`, `POST /api/importar`) e `web/index.html` (embutido com `go:embed`) as abas, o modal de mês novo e o painel ⓘ com a memória de cálculo. Cada comando tem uma função `dadosX` que monta os dados já formatados; o terminal e a tela usam a mesma, então os números são iguais. Um pedido por vez (mutex) e um `app` novo por pedido (caches de uma execução só).
**Na tela também:** radar ordena clicando no título da coluna; Proventos mostra primeiro os ativos de hoje, a tabela por mês (clique no mês = de quais ativos veio) e os vendidos recolhidos; cada aba tem endereço próprio (`#radar`, `#indicadores`…). Na primeira vez num banco novo, Indicadores e Radar demoram minutos (baixam CVM, COTAHIST e eventos de ~230 empresas); depois é rápido.
**Para investigar:** P/L de 0,01 da SANB11 no radar (errado; suspeita na conta de ações/unit); lucro 12m da SAPR11 (R$ 455 mi) muito abaixo do de 2025 (R$ 2.080 mi).
**Falta:** login (bcrypt, sessão 30 dias, bloqueio) e acesso pela internet. Hospedagem em aberto: a VM da Oracle não sai ("Out of host capacity"); alternativa pesquisada é deixar neste PC com Tailscale Funnel. Ver [PLANO.md](PLANO.md).

Lembrete mensal: **na tela, não no Windows** (Wallace pediu). A posição mostra um aviso no topo quando o mês virou desde a última importação (config `ultima_importacao`, gravada por `carteira importar`). Na tela web: **modal** sobre tudo, fundo com blur gaussiano (`backdrop-filter: blur`), passo a passo + botão de importar; fecha no X ou clicando fora.

Fase 5, acesso: **na nuvem** (Oracle Cloud Always Free, conta sem upgrade, VM A1.Flex Ubuntu), aberto pela internet (PC pessoal, PC do trabalho, celular), login usuário/senha (bcrypt, sessão 30 dias, bloqueio após erros, HTTPS automático). Página em HTML/JS simples embutida com `go:embed`, sem framework; a API reaproveita os cálculos do terminal. O banco e o token ficam no servidor.

## Estado da hospedagem (28/10/2026)
- Conta Oracle Cloud: tenancy `wallacecarteira`, região São Paulo (`sa-saopaulo-1`), em Free Trial. Só recursos **Always Free**, sem upgrade.
- **Rede pronta** (criada por API): VCN `carteira-vcn` (10.0.0.0/16), sub-rede pública `carteira-publica` (10.0.0.0/24), internet gateway e rota padrão. Portas 22, 80 e 443 liberadas na security list padrão.
- **VM ainda não criada:** `carteira`, `VM.Standard.A1.Flex` (1 OCPU, 6 GB), Ubuntu 26.04 aarch64. A Oracle responde "Out of host capacity" (no console e por API). No PC de casa, o script `C:\Users\Lenovo\.oci\carteira_vm.py` (SDK Python `oci`, fora do repositório) tenta criar a cada minuto enquanto está rodando (log em `C:\Users\Lenovo\.oci\carteira_vm.log`; para quando o PC desliga). Até 03/11/2026, só "sem vaga".
- As chaves (API da Oracle e SSH da VM, usuário `ubuntu`) ficam **só no PC de casa**. Nunca grave chaves no repositório.
- Quando a VM existir: liberar 80/443 também no firewall do Ubuntu (as imagens da Oracle vêm com iptables bloqueando tudo menos SSH), compilar para o servidor com `GOOS=linux GOARCH=arm64`, serviço systemd e HTTPS automático.
- A tela web já existe (ver "Fase 5 (em andamento)"); falta login e hospedagem, que não dependem da Oracle para serem programados.
