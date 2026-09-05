# iso8583-volumetry-lab

Contexto permanente do projeto. Leia antes de qualquer alteração.

---

## 1. O que é isto e por que existe

Aparato experimental de um TCC de MBA em Engenharia de Software (USP/Esalq).
O objetivo do trabalho é responder à seguinte hipótese:

> Um injetor de mensagens ISO 8583 baseado em dados simulados permite identificar
> padrões de erro e gargalos de desempenho em sistemas autorizadores de forma mais
> precisa do que abordagens genéricas de teste de carga, especialmente sob alta
> volumetria.

**O que é avaliado é o artigo científico, não o código.** O código existe para
produzir dados que sobrevivam ao escrutínio de uma banca. Isso muda as prioridades:

| Prioridade alta | Prioridade baixa |
|---|---|
| Correção da medição | Elegância da API |
| Reprodutibilidade bit a bit | Cobertura de features do ISO 8583 |
| Saída de dados auditável | Performance do próprio injetor além do necessário |
| Registro do ambiente de execução | Interface de usuário |

Se em algum momento houver conflito entre "mais rápido" e "mensurável de forma
defensável", escolha mensurável.

---

## 2. Restrições inegociáveis

Estas não são preferências. Violá-las inviabiliza o trabalho.

1. **Nenhuma bandeira específica.** Não implemente, referencie ou aproxime a
   Mastercard Customer Interface Specification (CIS), a Visa BASE I, ou qualquer
   especificação de bandeira. Essas specs são proprietárias e confidenciais.
   Use apenas ISO 8583 genérico, perfil 1987, cujo layout é de domínio público.
2. **Nenhum dado real.** Toda a massa é sintética e gerada com semente fixa.
   PANs devem ser numéricos válidos por Luhn mas de faixas de teste, nunca BINs
   reais em uso.
3. **Nenhuma referência a empresa, cliente ou sistema interno** em código,
   comentários, nomes de variáveis, dados de teste ou mensagens de commit.
4. **Nada de rede externa.** Injetor e autorizador rodam na mesma máquina,
   via loopback.

---

## 3. Arquitetura

Dois binários independentes, comunicando por TCP em `127.0.0.1`.

```
cmd/injector/     gerador de carga
cmd/authorizer/   SUT — autorizador mock
internal/iso8583/ montagem e parse das mensagens
internal/ratelimit/ controle de taxa em modelo aberto
internal/metrics/ coleta de latência e consolidação
internal/massa/   leitura dos CSVs de entrada
data/             massa sintética
results/          saída bruta, uma pasta por rodada
analysis/         scripts de estatística e gráficos
docs/experimento.md  ambiente, versões, procedimento
```

**Transporte:** TCP puro com framing de prefixo de tamanho de 2 bytes,
big-endian, precedendo cada mensagem. É a convenção mais comum em autorizadores
e é trivial de documentar no artigo.

**Codificação das mensagens:** ASCII, bitmap primário apenas.

**Subconjunto de data elements** (autorização, MTI `0100` → `0110`):

| DE | Conteúdo | Formato |
|----|----------|---------|
| 2  | PAN | LLVAR n..19 |
| 3  | Processing code | n6 |
| 4  | Valor da transação | n12 |
| 7  | Data/hora de transmissão | n10 (MMDDhhmmss) |
| 11 | STAN | n6 |
| 12 | Hora local | n6 |
| 13 | Data local | n4 |
| 18 | MCC | n4 |
| 22 | POS entry mode | n3 |
| 32 | ID da instituição adquirente | LLVAR n..11 |
| 37 | RRN | an12 |
| 39 | Código de resposta | an2 (só na resposta) |
| 41 | Terminal ID | ans8 |
| 49 | Moeda | n3 |

O critério de seleção deste subconjunto deve estar documentado em
`docs/experimento.md`: são os elementos mínimos para compor uma requisição de
autorização interoperável, sem entrar em subelementos privados de bandeira.

**Biblioteca:** use `github.com/moov-io/iso8583` para marshal/unmarshal.
Isso é deliberado e coerente com a tese do trabalho — a lacuna identificada na
literatura é justamente que existem boas bibliotecas de manipulação de mensagens
mas nenhuma ferramenta de volumetria com análise de desempenho em cima delas.
Construir a camada que falta, e não reimplementar a que já existe, é o argumento
central do TCC.

---

## 4. Autorizador mock (`cmd/authorizer`)

Um SUT precisa ser **previsível**, não realista. Se o comportamento dele for
opaco, não há como atribuir uma variação de latência ao injetor ou ao autorizador.

Servidor TCP que aceita conexões concorrentes, lê `0100`, responde `0110`
ecoando os DEs de eco e preenchendo o DE 39.

Configurável por flags, tudo determinístico dada uma semente:

- `--latency-base` — latência de serviço base
- `--latency-jitter` — dispersão, com distribuição declarada (exponencial ou
  lognormal; documente qual, no artigo isso importa)
- `--approval-rate` — proporção de `00`
- `--decline-dist` — distribuição dos códigos de recusa (ex.: `51:40,05:30,14:20,91:10`)
- `--max-conns` — teto de conexões simultâneas, para permitir provocar saturação
- `--seed`

Não adicione lógica de negócio, cache, ou qualquer adaptação dinâmica ao volume.
Qualquer não-linearidade no resultado precisa vir de contenção real de recursos,
não de esperteza do mock.

---

## 5. Injetor (`cmd/injector`) — a parte crítica

### 5.1 Modelo aberto, obrigatoriamente

O gerador de carga **deve** operar em modelo aberto: as chegadas são
independentes das conclusões. Um ticker dispara a cada `1/TPS` e cada envio
ocorre em sua própria goroutine.

O anti-padrão a evitar:

```go
// ERRADO — modelo fechado. Não faça isto.
for {
    inicio := time.Now()
    enviar()
    aguardarResposta()
    registrar(time.Since(inicio))
}
```

Num loop fechado, uma resposta lenta atrasa a próxima requisição, o sistema
nunca é submetido à taxa pretendida e a cauda da distribuição desaparece da
medição. A diferença de comportamento entre modelo aberto e fechado é enorme
e está documentada em Schroeder, Wierman & Harchol-Balter (2006), *Open Versus
Closed: A Cautionary Tale*, NSDI'06 — que é citado no artigo.

### 5.2 Omissão coordenada

Registre a latência a partir do **instante de chegada pretendido** (o tick
agendado), não do instante em que o envio efetivamente ocorreu. Se o injetor
atrasar por contenção interna, esse atraso pertence à latência observada pelo
cliente e precisa aparecer no número.

```go
// Correto
agendado := inicio.Add(time.Duration(i) * intervalo)
// ... envio pode ocorrer depois de 'agendado'
latencia := time.Since(agendado)   // não time.Since(momentoDoEnvio)
```

Registre as duas grandezas em colunas separadas no CSV bruto
(`latencia_servico` e `latencia_resposta`), para que a análise possa comparar e
o artigo possa discutir a diferença.

### 5.3 Coleta

- `github.com/HdrHistogram/hdrhistogram-go` para latência. Nunca calcule
  percentis sobre média móvel ou amostragem.
- Warm-up descartado, duração configurável via `--warmup`.
- Duração fixa por rodada via `--duration`, não número fixo de requisições.
- `--seed` para a ordem de consumo da massa.

### 5.4 Saída

Duas coisas, por rodada, em `results/<timestamp>-<tps>-<rep>/`:

1. `raw.csv` — uma linha por requisição: `stan, ts_agendado, ts_envio,
   ts_resposta, latencia_servico_us, latencia_resposta_us, de39, erro_transporte`
2. `summary.json` — o resumo consolidado **mais o ambiente completo**:
   versão do Go, `GOMAXPROCS`, número de CPUs, sistema operacional, todas as
   flags dos dois processos, semente, e a linha de comando exata.

Sem o bloco de ambiente no `summary.json` a rodada é inútil para o artigo.

---

## 6. Métricas obrigatórias

- **Throughput alvo vs. alcançado.** Esta é a métrica mais importante e a mais
  esquecida. Se o alcançado ficar abaixo do alvo, o resultado daquele nível de
  carga mede a saturação de algum componente — e é preciso saber qual.
- Latência: média, mediana, p95, p99, p99.9, máximo, desvio-padrão.
- Taxa de aprovação e recusa; distribuição dos valores de DE 39.
- **Erros de transporte e timeouts contados separadamente das recusas.** Uma
  recusa é resposta de negócio; um timeout é falha de desempenho. Misturar os
  dois invalida a análise.

---

## 7. Máquina única — o problema que precisa ser tratado de frente

Injetor e autorizador rodam no mesmo host e disputam CPU. Isso é uma ameaça
à validade real e será declarada no artigo. O código precisa dar meios de
medi-la, não de escondê-la.

1. **Isolamento de recursos.** Permita fixar `GOMAXPROCS` de cada processo
   separadamente, e documente o uso de `taskset` para prender cada binário a
   conjuntos disjuntos de núcleos.
2. **Baseline de calibração — faça antes de qualquer experimento.** Crie um
   modo ou um alvo trivial de eco (`cmd/authorizer --echo-only`, resposta
   imediata sem latência artificial) e descubra em que TPS o *injetor* satura
   sozinho.

   Se o injetor satura em 800 TPS, o experimento de 1000 TPS mede o injetor e
   não o autorizador. Descobrir isso depois de rodar tudo é o pior cenário
   possível. O teto de calibração vai para o artigo como limite declarado do
   aparato.
3. Registre `GOGC` e considere fixá-lo. Pausas do coletor de lixo do Go afetam
   os dois processos e contaminam a cauda — Dean & Barroso (2013) tratam
   exatamente disso e são citados no artigo.

---

## 8. Convenções

- Go 1.22+, `go.mod` com versão travada.
- Sem dependências além de `moov-io/iso8583`, `hdrhistogram-go` e a biblioteca
  padrão. Cada dependência nova precisa ser justificável em uma frase no artigo.
- Testes unitários para montagem e parse de mensagem: sem eles não há como
  afirmar no texto que as mensagens geradas são válidas.
- Licença MIT.
- `README.md` deve permitir que um terceiro reproduza os experimentos do zero.
  A replicabilidade é critério de avaliação, não um detalhe.

---

## 9. Ordem de execução

O prazo é curto e o marco que importa é ter números em mãos. Nesta ordem:

1. Autorizador mock responde a um `0100` fixo. Sem métricas, sem flags.
2. Injetor envia um `0100` e lê a resposta. Ponta a ponta.
3. Controle de taxa em modelo aberto, a 10 TPS.
4. Coleta de latência e escrita de `raw.csv` e `summary.json`.
5. Flags de configuração do mock.
6. Baseline de calibração.
7. Só então: rodar os experimentos.

Não avance para o passo seguinte antes de o anterior estar funcionando de
verdade. Não construa abstração para requisito que ainda não apareceu.
