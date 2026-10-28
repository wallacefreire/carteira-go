# Carteira Go — plano do projeto

Controle de investimentos em Go, com dados reais e indicadores calculados por nós, a partir das fontes oficiais.

## 1. O que o sistema mostra

### Carteira
- Posição por ativo: quantidade, preço médio, cotação atual, valor e resultado (R$ e %)
- **Peso por ativo e por classe** (ações, Tesouro, cripto), com alerta quando um ativo passar do teto (25%)
- **Dividendos recebidos**, por ativo e por mês, e o total
- Classes cobertas: ações da B3, Tesouro IPCA+ e BTC

### Quais ativos aparecem: três listas
1. **Carteira:** o que eu tenho. Mostra posição, pesos, proventos e indicadores.
2. **Lista de observação:** ativos que eu escolho acompanhar (ex.: GRND3, SAPR11, WEGE3). Mostra só cotação e indicadores.
3. **Radar:** todas as ações líquidas da B3 passam pelo **filtro do Wallace** e aparecem ordenadas por quantos critérios cumprem (nenhum critério elimina sozinho). Atualiza 1× por semana.

Os balanços da CVM vêm num arquivo único com todas as empresas, então calcular indicadores para a bolsa inteira não custa mais. O que pesa é o beta, que precisa do histórico de preço de cada ação. Por isso o radar roda semanalmente e fica em cache.

### Indicadores por ação: ano a ano (últimos 5 anos) + últimos 12 meses + média de 5 anos

| Grupo | Indicador | Fórmula |
|---|---|---|
| Resultado | **Lucro líquido** | lucro atribuído aos sócios da controladora (DRE consolidada 3.11.01); 12 meses = último ano + acumulado deste ano − mesmo acumulado do ano anterior |
| Valuation | **P/L** | (nº de ações × cotação) ÷ lucro líquido |
| Rentabilidade | **ROE** | lucro líquido ÷ patrimônio líquido médio |
| Rentabilidade | **ROIC** | EBIT × (1 − 34%) ÷ (patrimônio líquido + dívida líquida) |
| Diversificação | **Correlação com a carteira** | correlação(retorno do ativo, retorno ponderado da carteira) |
| Endividamento | **Dívida líquida / EBITDA** | (empréstimos − caixa) ÷ EBITDA |
| Endividamento | **Dívida líquida / PL** | (empréstimos − caixa) ÷ patrimônio líquido |
| Liquidez | **Liquidez corrente** | ativo circulante ÷ passivo circulante |
| Risco | **Beta** | covariância(retorno ação, retorno Ibovespa) ÷ variância(retorno Ibovespa) |
| Risco | **Volatilidade anual** | desvio-padrão dos retornos diários × √252 |

- Beta e volatilidade são calculados **ano a ano** com os preços diários de cada ano. Assim dá para ver se a ação ficou mais ou menos arriscada.
- **Beta atual:** retornos **semanais dos últimos 2 anos** contra o Ibovespa (padrão de mercado), recalculado **1× por mês**.
- **Correlação com a minha carteira:** para cada ativo da lista de observação e do radar, a correlação dos retornos semanais (2 anos) com os retornos da minha carteira ponderada. Mostra quais ativos **diversificam** e quais repetem o risco que já tenho. Recalculada 1× por mês, junto com o beta.
- **Bancos** (ex.: BBAS3): dívida líquida/EBITDA, ROIC e liquidez corrente não se aplicam, e o balanço de banco na CVM usa outras contas. Esses indicadores aparecem como "não se aplica" e o radar não os usa para reprovar bancos.
- **Units** (ex.: SAPR11, TAEE11): no P/L, o valor de mercado soma ações ON e PN da empresa. Não é nº de units × cotação da unit.

### Botão "i": memória de cálculo
Cada indicador tem um **ⓘ**. Ao clicar, aparecem:
1. **Fórmula** em linguagem simples
2. **Números usados**, com valores e de qual demonstração vieram (ex.: "Lucro líquido 12m: R$ 89,6 mi = 3T25 67,9 + 4T25 −8,4 + 1T26 27,4 + 2T26 2,7")
3. **Período** considerado
4. **Fonte** de cada dado (CVM, brapi, Tesouro, CoinGecko) e **data da última atualização**
5. **Como ler** o resultado (faixas de bom e ruim)

### Como a correlação com a carteira é calculada
1. **Preços:** fechamento semanal dos últimos 2 anos (~104 semanas), **ajustado por proventos**, para dividendos não parecerem queda de preço.
2. **Retorno semanal** de cada ativo: preço da semana ÷ preço da semana anterior − 1.
3. **Retorno da carteira** em cada semana: soma de (peso atual do ativo × retorno do ativo). Usa os **pesos de hoje**, mostrando como a carteira atual teria se comportado.
4. **Ativo que já está na carteira:** ele é comparado com o **resto da carteira, sem ele mesmo**. Senão a correlação sairia inflada.
5. **Fórmula:** correlação de Pearson = covariância(ativo, carteira) ÷ (desvio do ativo × desvio da carteira). É o mesmo que `=CORREL()` no Excel.
6. **Tesouro e BTC:** Tesouro pelo preço de mercado diário do Tesouro Transparente. BTC convertido para reais (preço em US$ × dólar do dia).
7. **Leitura:** acima de 0,6 repete o risco · de 0,2 a 0,6 diversifica um pouco · abaixo de 0,2 diversifica bem · negativo protege.

Exemplo do que o ⓘ mostraria:
> **SAPR11 × carteira: 0,41**
> 104 semanas (27/10/2024 a 25/10/2026) · pesos usados: WEGE3 35%, ITUB4 30%, EGIE3 20%, BTC 15% · preços: brapi (ajustados) · atualizado em 01/11/2026

### Frequência de atualização
| Dado | Frequência |
|---|---|
| Cotações | a cada consulta (cache de 15 min) |
| Beta, volatilidade e correlação | 1× por mês |
| Balanços e indicadores | a cada trimestre (quando sai o ITR/DFP) |
| Radar | 1× por semana (usa beta e balanços do cache) |
- Fica **sem P/VP**.
- **Filtro do Wallace** (configurável): P/L baixo, **5 anos sem prejuízo anual**, ROE e ROIC altos, beta baixo, correlação baixa com a carteira. Cada indicador aparece com ✅ ou ❌.
- Empresas com menos de 5 anos de bolsa não são eliminadas: aparecem com a marca **"histórico curto"**. Prejuízos trimestrais são sinalizados (⚠️), mas não reprovam.

## 2. Fontes de dados (todas reais)

| Dado | Fonte | Atualização |
|---|---|---|
| Cotação atual (ações) | brapi.dev, plano grátis | até 30 min de atraso |
| Histórico de preços (ações e Ibovespa) | **COTAHIST da B3** (arquivo oficial, todas as ações, diário); BOVA11 no lugar do Ibovespa | 1× por semana |
| Proventos e desdobramentos (para ajustar os preços) | site da B3, página de empresas listadas | 1× por semana |
| Balanços (DRE, balanço patrimonial) | **CVM — dados abertos (DFP anual e ITR trimestral)** | a cada divulgação |
| Dividendos | extrato de Movimentação da B3 (recebidos) | quando eu importar |
| Tesouro IPCA+ (preço e taxa) | Tesouro Transparente (CSV oficial) | 1× por dia |
| Bitcoin | CoinGecko | tempo real |
| Minhas operações e proventos | Extrato de **Movimentação** da Área do Investidor da B3 (todas as corretoras, desde nov/2019) + lançamento manual | quando eu importar |

Limites do brapi grátis (out/2026): 1 ativo por requisição, 1 requisição por vez, 15.000 requisições/mês, histórico de 3 meses, dividendos fora do plano.

Os indicadores são **calculados pelo sistema** a partir dos balanços da CVM, e não copiados de sites, e cada número mostra de onde veio. Exemplo da SOJA3: o P/L de ~10 usava o lucro **consolidado**, que inclui o lucro do fundo SNAG11, consolidado pela Boa Safra mas pertencente aos cotistas do fundo. Com o lucro dos acionistas da SOJA3 (R$ 23 mi em 12 meses), o P/L é ~38 a 44, como diziam os sites. O sistema avisa quando os dois lucros divergem.

## 3. Arquitetura

```
Carteira/
├── cmd/carteira/        # ponto de entrada (CLI; servidor HTTP na fase 5)
├── internal/
│   ├── fontes/          # um pacote por fonte: brapi, cvm, tesouro, coingecko
│   ├── carteira/        # operações, preço médio, pesos, proventos
│   ├── indicadores/     # P/L, ROE, ROIC, dívida, liquidez, beta, volatilidade
│   ├── importacao/      # leitura do extrato da B3 (.xlsx)
│   ├── formato/         # números e datas no padrão brasileiro
│   └── armazenamento/   # SQLite
└── web/                 # página simples com as tabelas (fase 5)
```

- **Banco:** SQLite (um arquivo só, sem servidor)
- **Cache:** balanços e preços ficam salvos, para não chamar as APIs a cada consulta

## 4. Etapas

1. **Fase 1 — Carteira + lista de observação (feita):** importação do extrato de Movimentação da B3, lançamento manual, preço médio, cotação via brapi, pesos e teto. Saída no terminal.
2. **Fase 2 — Indicadores:**
   - **2a (feita):** balanços da CVM (DFP/ITR), lucro, P/L atual, ROE, ROIC, dívida líquida/EBITDA e /PL e liquidez corrente: 5 anos + 12 meses + média, com memória de cálculo (`carteira explicar`).
   - **2b (feita):** histórico de preços pelo COTAHIST da B3, ajustado por proventos e desdobramentos: P/L ano a ano, beta, volatilidade e correlação com a carteira.
3. **Fase 3 — Tesouro, BTC e dividendos (feita):** Tesouro importado do extrato e cotado pelo Tesouro Transparente; BTC lançado à mão e cotado pela CoinGecko; pesos por classe; `carteira proventos` (por mês e por ativo) e `carteira agenda` (proventos anunciados a receber).
4. **Fase 4 — Radar (feita):** `carteira radar` e `carteira filtro`; limites: P/L até 15, ROE 5a ≥ 10%, ROIC 5a ≥ 8%, beta ≤ 1,2, correlação ≤ 0,6, lucro em todos os anos.
5. **Fase 5 — Interface:** API HTTP + página web com as tabelas e os ✅/❌ do filtro. A tela precisa ter um botão para **importar o extrato da B3** (.xlsx), que faz o mesmo que `carteira importar` e mostra o resumo (novas, já existentes, avisos). Quando o mês virar sem importação, a tela abre um **card na frente de tudo** (modal), com o resto da tela desfocado (blur gaussiano), o passo a passo e o botão de importar; fecha só no **X** ou clicando fora do card.
   - **Acesso (decidido em 28/10/2026):** hospedado **na nuvem** (24 h), aberto pela internet em qualquer aparelho (PC pessoal, PC do trabalho, celular), só com **login de usuário e senha**: senha guardada como hash bcrypt, sessão de 30 dias, bloqueio após várias tentativas erradas, HTTPS com certificado automático. 2FA pode vir depois. Hospedagem a escolher no início da fase (Fly.io, Render, Oracle Cloud). O banco e o token do brapi passam a ficar no servidor; importação pelo botão da tela.
   - **Layout:** abas Carteira, Proventos, Agenda, Indicadores e Radar; botão Importar sempre visível; cards de resumo no topo; ⓘ abre painel lateral com a memória de cálculo; tema claro/escuro; feito para caber no celular.

## 5. Decisões
- Teto por ativo: **25%** (muda com `carteira teto`).
- Código: Claude escreve, Wallace revisa.
- Importação do extrato da B3 antecipada para a Fase 1.
- Pendente: criar o token no brapi.dev (gratuito) e definir a variável `BRAPI_TOKEN`.
