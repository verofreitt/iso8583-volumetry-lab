# iso8583-volumetry-lab

Aparato experimental de um TCC de MBA em Engenharia de Software (USP/Esalq).

Injetor de carga e autorizador mock para medição de desempenho sob alta
volumetria de mensagens ISO 8583, perfil 1987. O objetivo é produzir dados de
latência e vazão que sustentem a análise do artigo.

> **Escopo.** Apenas ISO 8583 genérico, cujo layout é de domínio público.
> Nenhuma especificação de bandeira é implementada, referenciada ou aproximada.
> Toda a massa de dados é sintética. Injetor e autorizador comunicam-se
> exclusivamente por `127.0.0.1`.

## Estado

Aparato completo e **experimentos finais executados**. Resultados na seção 10
de [docs/experimento.md](docs/experimento.md).

| Passo | Componente | Estado |
|-------|-----------|--------|
| 1 | Autorizador responde a uma `0100` | **concluído** |
| 2 | Injetor envia `0100` e lê a resposta | **concluído** |
| 3 | Controle de taxa em modelo aberto | **concluído** |
| 4 | Coleta de latência, `raw.csv` e `summary.json` | **concluído** |
| 5 | Flags de configuração do mock | **concluído** |
| 6 | Baseline de calibração | **concluído** |
| — | Massa sintética de entrada | **concluído** |
| — | Validação da detecção (controle negativo e positivo) | **concluído** |
| 7 | Experimentos finais | **concluído** |
| 7 | Execução dos experimentos | pendente |

## Requisitos

- Go 1.25.3 ou superior (a versão está travada em `go.mod`).
- Um compilador C, para o detector de corrida. No Windows:
  `winget install BrechtSanders.WinLibs.POSIX.UCRT`.

A única dependência direta é `github.com/moov-io/iso8583`.

## Compilação e testes

```sh
go build ./...
go test ./...
```

O injetor e o autorizador são concorrentes, então o detector de corrida faz
parte da verificação e não é opcional:

```sh
CGO_ENABLED=1 go test -race -count=3 ./...
```

Os testes usam um autorizador de mentira, montado sobre o mesmo código de
resposta do binário real. Para exercitar o caminho concorrente do autorizador
de verdade, compile os dois com instrumentação e rode uma carga:

```sh
CGO_ENABLED=1 go build -race -o authorizer-race ./cmd/authorizer
CGO_ENABLED=1 go build -race -o injector-race  ./cmd/injector

./authorizer-race &
GORACE="halt_on_error=1" ./injector-race -tps 1000 -duration 8s -conns 64
```

## Massa sintética

A massa é um **único CSV de entrada**, uma linha por transação, versionado em
`data/massa.csv`. As duas saídas — `raw.csv` e `summary.json` — são geradas por
rodada.

```sh
go run ./cmd/massa -seed 1 -linhas 50000
```

O CSV commitado é a **fonte de verdade** dos experimentos; o gerador documenta
o método. Reproduzir a massa só a partir da semente não é confiável entre
versões do Go — a 1.20 passou a semear as funções globais automaticamente, a
1.22 adotou o ChaCha8, e o `math/rand/v2` removeu o gerador da Go 1. Quem
replicar pega o CSV e obtém os mesmos bytes.

| Coluna | DE | Origem |
|--------|----|--------|
| `id` | — | sequencial; liga ao `raw.csv` |
| `pan` | 2 | 16 dígitos, válido por Luhn, prefixo 9 (MII reservado) |
| `processing_code` | 3 | compra ou saque |
| `amount` | 4 | lognormal truncada, mediana R$ 50,00 |
| `mcc` | 18 | 6 valores da ISO 18245 |
| `pos_entry_mode` | 22 | digitado, tarja, chip, aproximação, e-commerce |
| `acquirer_id` | 32 | 5 instituições sintéticas |
| `terminal_id` | 41 | 500 terminais |
| `currency` | 49 | `986` |

STAN, RRN e os campos de data e hora **não** são massa: são gerados por
requisição no instante do envio.

São duas sementes distintas: `cmd/massa -seed` gera a massa, `cmd/injector
-seed` define a ordem de consumo.

### Cruzando transação com resultado

O `raw.csv` traz a coluna `massa_id`, que liga cada requisição à transação que
a originou:

```
raw.csv.massa_id -> massa.csv.id -> pan, mcc, amount, pos_entry_mode, ...
raw.csv.stan     -> correlação requisição/resposta
```

É essa cadeia que permite cruzar atributos da transação com código de recusa e
latência — o cruzamento central da hipótese do trabalho.

## Executando o autorizador

```sh
go run ./cmd/authorizer --latency-base 20ms --latency-jitter 10ms   --approval-rate 0.85 --seed 42 --config-out autorizador.json
```

| Flag | Padrão | Efeito |
|------|--------|--------|
| `--latency-base` | `0s` | latência de serviço base |
| `--latency-jitter` | `0s` | média da dispersão somada à base |
| `--latency-dist` | `exponencial` | forma da dispersão: `exponencial` ou `lognormal` |
| `--approval-rate` | `1` | proporção de respostas `00` |
| `--decline-dist` | `51:40,05:30,14:20,91:10` | pesos dos códigos de recusa |
| `--decline-bias` | — | viés estático por atributo, `atributo=valor:taxa` |
| `--max-conns` | `0` | teto de requisições simultâneas; 0 remove o teto |
| `--seed` | `1` | semente das decisões |
| `--echo-only` | `false` | responde imediatamente, para calibrar o injetor |
| `--config-out` | — | arquivo com a configuração e o ambiente |
| `--quiet` | `false` | suprime o log por conexão |

Escuta em `127.0.0.1:8583`. Encerre com `Ctrl+C`.

### Determinismo

Cada decisão — a latência a aplicar e o código do DE 39 — é uma **função pura
da semente e do STAN**. Não há estado compartilhado nem trava, e o resultado
independe da ordem de chegada ou de qual goroutine atende. Duas execuções com a
mesma semente devolvem exatamente as mesmas respostas para as mesmas
transações.

Um gerador compartilhado daria uma sequência determinada pela semente, mas o
mapeamento entre valores sorteados e requisições dependeria do escalonador — a
distribuição agregada seria estável e a rodada não seria reproduzível transação
a transação.

Repetições da mesma rodada com a mesma semente recebem as mesmas respostas,
porque os STANs se repetem. Para sorteios diferentes entre repetições, mude a
semente do autorizador.

### Registrando a configuração dos dois processos

O `summary.json` precisa conter as flags dos dois processos. Em vez de
transcrevê-las à mão, o autorizador as grava e o injetor as embute:

```sh
go run ./cmd/authorizer --latency-base 20ms --config-out autorizador.json
go run ./cmd/injector   --sut-config autorizador.json -tps 200 -duration 20s
```

O arquivo traz também o ambiente do autorizador, incluindo o `GOMAXPROCS` dele
— os dois processos disputam CPU na mesma máquina, e os dois valores precisam
constar do resultado.

## Executando o injetor

Com o autorizador em execução em outro terminal:

```sh
go run ./cmd/injector -tps 200 -duration 20s -warmup 5s -conns 16
```

| Flag | Padrão | Significado |
|------|--------|-------------|
| `-tps` | `10` | taxa de chegada pretendida, em transações por segundo |
| `-duration` | `10s` | duração da rodada, incluindo o warm-up |
| `-warmup` | `0s` | período inicial descartado da análise |
| `-conns` | `8` | conexões persistentes mantidas com o autorizador |
| `-rep` | `1` | número da repetição, usado no nome da pasta de saída |
| `-seed` | `1` | semente da ordem de consumo da massa |
| `-seq` | `0` | posição na ordem de execução do experimento; 0 quando avulsa |
| `-massa` | `data/massa.csv` | CSV da massa sintética de entrada |
| `-results` | `results` | raiz onde a pasta da rodada é criada |
| `-sut-config` | — | arquivo gravado pelo autorizador com `--config-out` |

Saída de uma rodada a 200 TPS:

```
alvo          : 200 TPS por 20s, 16 conexoes
chegadas      : 3000 medidas de 4000 (warm-up de 5s descartou 1000)
respondidas   : 3000 (aprovadas 3000, recusadas 0)
falhas        : 0 erros de transporte, 0 timeouts
vazao         : 200.00 TPS (100.0% do alvo)
DE 39         : 00=3000

latencia (us)       servico   resposta
media                1677.2     3234.5
mediana                 270        909
p95                    7371      13423
p99                   31455      51711
p99.9                 73151     103871
maximo                73663     115519
desvio-padrao        5959.4     9249.9

atraso de agendamento: medio 1206 us, maximo 76037 us

resultados em results60922T093145-200tps-2
```

`Ctrl+C` interrompe o agendamento de novas chegadas, mas as requisições já em
voo são aguardadas antes do resumo e os arquivos são gravados normalmente.

## Arquivos de saída

Uma pasta por rodada, em `results/<timestamp>-<tps>-<rep>/`.

**`raw.csv`** — uma linha por requisição medida:

```
stan, ts_agendado, ts_envio, ts_resposta,
latencia_servico_us, latencia_resposta_us, de39, erro_transporte
```

**`summary.json`** — o resumo consolidado mais o bloco de ambiente completo:
versão do Go, `GOMAXPROCS`, número de CPUs, sistema operacional, `GOGC`, fonte
e resolução do relógio, todas as flags do injetor e a linha de comando exata.

### Como ler as duas latências

`latencia_servico` é o tempo entre o envio efetivo e a resposta — é a latência
que o autorizador exibe. `latencia_resposta` parte do **instante de chegada
pretendido**, e é a que o cliente observa.

A diferença entre as duas é a correção de omissão coordenada. Se o injetor
atrasou por contenção interna, esse atraso pertence à experiência do cliente e
precisa aparecer no número. Reportar apenas a primeira subestima
sistematicamente o que o sistema entrega sob carga — na rodada acima, a mediana
sobe de 270 µs para 909 µs e o p99 de 31 ms para 51 ms.

### Como ler a vazão

**Vazão alcançada vs. alvo** é a métrica mais importante. É calculada sobre a
*janela de chegadas*, não sobre o tempo decorrido até a última resposta: as N
chegadas ocupam os instantes 0, 1/TPS, …, (N−1)/TPS, uma janela que termina um
intervalo antes do fim da rodada. Usar o tempo decorrido produziria vazão acima
de 100% do alvo, o que é impossível em modelo aberto.

**Recusas, erros de transporte e timeouts são contados separadamente.** Uma
recusa é resposta de negócio; um timeout é falha de desempenho; um erro de
transporte é falha de infraestrutura. Misturá-los invalidaria a análise.

**O atraso de agendamento** mede o quanto o injetor se desviou do plano. O
médio é o diagnóstico de saturação do gerador de carga; o máximo é pior caso e
sobe com qualquer pausa do coletor de lixo. Quando o médio ultrapassa o
intervalo entre chegadas, o resumo emite um aviso explícito e a rodada passa a
medir o injetor.

## Calibração do aparato

**Faça isto antes de qualquer experimento.** Injetor e autorizador disputam CPU
na mesma máquina. Se o injetor satura em 800 TPS, o experimento de 1000 TPS
mede o injetor e não o autorizador — e descobrir isso depois de rodar tudo é o
pior cenário possível.

```sh
go build -o calibrate.exe ./cmd/calibrate
./calibrate.exe
```

> Use o binário compilado, não `go run`. O Windows Defender bloqueia de forma
> intermitente o executável temporário que o `go run` constrói em `%TEMP%`,
> como falso positivo, e a mensagem de erro não sugere a solução. Compilar
> explicitamente também tira a recompilação do caminho de execução da
> varredura.

| Flag | Padrão | Efeito |
|------|--------|--------|
| `-levels` | `100,250,500,1000,1500,2000,3000,5000` | taxas a varrer |
| `-reps` | `3` | repetições por nível |
| `-duration` | `15s` | duração de cada rodada, incluindo o warm-up |
| `-warmup` | `5s` | warm-up de cada rodada |
| `-conns` | `32` | conexões do pool do injetor |
| `-throughput-threshold` | `99` | percentual do alvo exigido para sustentar o nível |
| `-shuffle` | `true` | sorteia a ordem de execução das rodadas |
| `-order-seed` | `1` | semente do sorteio da ordem |
| `-rest` | `3s` | repouso entre rodadas |
| `-sut-args` | `--echo-only` | argumentos do autorizador; o padrão calibra contra o alvo trivial |
| `-nome` | `calibracao` | prefixo da pasta de saída |
| `-massa-seed` | `42` | semente base da ordem de consumo da massa |
| `-vary-seed` | `true` | soma o número da repetição à semente, tornando as repetições independentes também no desfecho |
| `-gomaxprocs-injector` | `0` | `GOMAXPROCS` do injetor; 0 mantém o padrão |
| `-gomaxprocs-authorizer` | `0` | `GOMAXPROCS` do autorizador; 0 mantém o padrão |
| `-gogc` | — | `GOGC` imposto aos dois processos |
| `-out` | `results` | raiz onde a pasta da calibração é criada |

O alvo é o autorizador em `--echo-only`, que responde imediatamente e sem
sorteio: o que sobra de latência e de atraso é do aparato. Cada rodada usa
processos novos, e os dois binários são compilados a partir do código corrente
no início da varredura.

**A ordem das rodadas é sorteada.** Varrer os níveis em sequência deixaria o
nível de carga confundido com a posição na varredura: se o estado da máquina
derivasse ao longo dos minutos que ela leva, a deriva apareceria como efeito do
nível. A ordem sorteada vai para o `calibracao.json` e a posição de cada rodada
para o `summary.json` dela, então a varredura continua reproduzível e
auditável.

A saída vai para `results/calibracao-<timestamp>/calibracao.json`, com o
procedimento, os níveis, os limites apurados e o ambiente.

### Critério de saturação

Um nível é **sustentado** quando **todas** as repetições passam nos três
critérios: atraso médio de agendamento menor que o intervalo entre chegadas,
vazão de ao menos 99% do alvo, e zero falhas de transporte.

Exigir unanimidade é deliberado — um teto de aparato deve ser conservador. E o
teto declarado é a maior taxa sustentada **antes da primeira saturação**: um
nível alto que volta a passar depois de um nível saturado é coincidência, não
capacidade.

## Validação da capacidade de detecção

Um instrumento que não encontra padrão não distingue "não há padrão" de "o
instrumento não detecta padrão". A hipótese do trabalho afirma que o injetor
permite **identificar padrões de erro**, o que exige exibi-lo detectando um
padrão conhecido.

A flag `--decline-bias` injeta uma verdade fundamental no alvo: a taxa de
recusa de um atributo declarado passa a ser o valor informado, em vez da taxa
base. A regra é estática, declarada e determinística — não é lógica de negócio,
não guarda estado e não muda com a carga.

```sh
# controle negativo: recusa uniforme
go run ./cmd/authorizer -approval-rate 0.85 -seed 42

# controle positivo: MCC 5967 com taxa 0,40 sobre base 0,15
go run ./cmd/authorizer -approval-rate 0.85 -seed 42 -decline-bias "mcc=5967:0.40"

# análise
go run ./analysis/qui2 -raw results/<rodada>/raw.csv -atributo mcc
```

### Resultado

Duas rodadas de 100 TPS por 5 min, 27.000 requisições medidas cada, idênticas
em tudo exceto o viés:

| Condição | χ² (6 gl) | p-valor | Independência |
|----------|-----------|---------|---------------|
| controle | 4,86 | 0,562 | **não** rejeitada |
| injetada | 1513,19 | < 10⁻¹² | **rejeitada** |

Na condição injetada, a taxa do MCC 5967 foi **0,4091 com IC 95% de [0,3936,
0,4248]** — contém o valor injetado de 0,40 e exclui a base de 0,15. A taxa foi
recuperada.

Os seis MCCs não viesados têm contagem de recusa **byte a byte idêntica** entre
as condições. Isso decorre do desenho determinístico: a decisão é função pura
de (semente, STAN), e o viés altera apenas o limiar das transações que casam
com ele. A única diferença entre as condições é o sinal injetado, sem
confundimento possível.

O detalhamento está em [docs/experimento.md](docs/experimento.md), seção 9.

## Resultados dos experimentos

Executados em 28/09/2026: 10, 100, 500 e 1000 TPS, 5 repetições de 2 min cada,
ordem sorteada. Alvo com latência base de 20 ms e jitter lognormal de média
10 ms, aprovação de 85%.

### O aparato recupera a distribuição configurada

| Quantil | Previsto | 100 TPS | 500 TPS | 1000 TPS |
|---------|----------|---------|---------|----------|
| p50 | 26,8 ms | 26,7 ms | 27,2 ms | 27,3 ms |
| p95 | 52,1 ms | 51,7 ms | 52,8 ms | 52,5 ms |
| p99 | 82,8 ms | 81,0 ms | 83,6 ms | 83,4 ms |

Concordância dentro de 2% em três quantis e três níveis de carga. A vazão ficou
em 100% do alvo em todos os níveis, sem uma falha de transporte.

### A latência de resposta revela o que a de serviço esconde

A latência de serviço é praticamente idêntica nos quatro níveis — o alvo não
degradou. A de resposta depende criticamente da utilização do pool, e o
experimento foi executado em duas sessões que caíram em regimes diferentes:

| Sessão | Média de serviço | Utilização a 1000 TPS | p99 resposta − serviço |
|--------|------------------|----------------------|------------------------|
| 28/09 | 31,20 ms | **97,5%** | **55,1 ms** |
| 29/09 | 30,36 ms | **94,9%** | **1,1 ms** |

Uma diferença de 0,84 ms na média de serviço — 2,7%, dentro da variação entre
sessões — moveu a utilização em 2,6 pontos e o enfileiramento colapsou de 55 ms
para 1 ms.

A utilização se calcula com o tempo **médio**, não com a mediana. A distribuição
do alvo foi configurada, então a média é analítica: 20 ms de base mais lognormal
de média 10 ms dá **30,0 ms**, contra 26,1 ms de mediana.

**O que fica estabelecido:** entre 94,9% e 97,5% de utilização, o p99 observado
pelo cliente passa de indistinguível da latência de serviço para 67% pior que
ela. A transição é abrupta.

> **M/M/c não modela este sistema.** Na utilização de cada sessão o modelo prevê
> 78,4 ms (observado 1,1) e 172,8 ms (observado 55,1) — superestimação de uma a
> duas ordens de grandeza. Esperado: o serviço tem CV de 0,44 e as chegadas são
> quase determinísticas, e ambas empurram o joelho para perto de ρ = 1. Serve
> como limite superior grosseiro, não como modelo. Calculado por
> [`analysis/fila`](analysis/fila).

Em ambas as sessões o autorizador está saudável. **Uma medição que reportasse
apenas latência de serviço declararia o gargalo inexistente nas duas.**

Regra de dimensionamento, com o tempo **médio**:

> conexões ≥ TPS × E[S] ÷ utilização alvo

Para 1000 TPS, E[S] de 31 ms e utilização alvo de 50%: **62 conexões**, não as
32 usadas.

### Limitações declaradas

**O nível de 10 TPS é ruidoso** — 900 requisições por repetição, contra 90.000
a 1000 TPS. Seu p99 não é comparável ao dos demais níveis e seu p99,9 não é
estimável.

**Repetições precisam de sementes independentes.** Com as sementes fixas, as
cinco repetições de um nível consomem as mesmas transações e recebem as mesmas
decisões — são réplicas apenas para latência. O experimento foi **reexecutado**
com `-vary-seed`, e os resultados de desfecho do artigo vêm dessa segunda
execução.

**O falso positivo era artefato da pseudorreplicação.** Com sementes fixas, o
teste a 1000 TPS dava p = 0,048 num alvo sem viés, repetido idêntico nas cinco
repetições — aparência de achado sistemático. Com réplicas independentes,
**zero de cinco rejeitam**, com p entre 0,17 e 0,99.

**Significância não basta como critério de detecção.** O ruído a N = 90.000 fica
entre 2% e 8% de diferença relativa; o sinal verdadeiro do controle positivo é
de 189%. O critério adotado exige **p < 0,05 E diferença relativa acima de
50%**, na lacuna entre os dois.

## Limites conhecidos do aparato

Apurados pela **segunda calibração**, de 28/09/2026, sob ordem sorteada e plano
de energia Alto desempenho. Registro em
[`results/calibracao-20260928T104552/calibracao.json`](results/calibracao-20260928T104552/calibracao.json).

| Limite | Valor |
|--------|-------|
| **Teto de injeção (critério da mediana)** | **2000 TPS** |
| **Maior nível com todas as repetições aprovadas** | **1000 TPS** |
| Primeira taxa saturada | 3000 TPS |
| Piso de atraso de agendamento | 440 µs |
| Piso de serviço (ida e volta em loopback) | 190 µs |
| Ruído na cauda, p99 | 1,2 a 7,8 ms |

Os experimentos param em **1000 TPS**: é o maior nível em que as cinco
repetições foram aprovadas. A 1500 e 2000 TPS duas de cinco reprovam
individualmente, e rodar exatamente no teto convidaria a crítica de que os
dados do nível mais alto vêm de onde o aparato já falha em 40% das tentativas.

> **O teto não é constante da máquina.** A primeira calibração, em 22/09,
> apurou 500 TPS; a segunda, 2000 TPS. Entre elas mudaram a ordem das rodadas,
> o plano de energia, o repouso e a forma de execução — mas a evidência aponta
> para variabilidade entre sessões como causa dominante, não para nenhuma
> dessas mudanças. **Recalibre imediatamente antes de cada lote de
> experimentos** e reporte o valor da sessão que produziu os dados.

A **vazão permanece em 100% em todos os níveis**, inclusive nos saturados: o
injetor entrega o número correto de requisições e recebe todas as respostas até
5000 TPS. O que ele não consegue é entregá-las *nos instantes pretendidos* —
acima do teto, a carga deixa de ser um fluxo uniforme e passa a chegar em
rajadas. Uma calibração que olhasse só para a vazão concluiria, erradamente,
que o aparato sustenta 5000 TPS.

### A cauda não é reprodutível entre sessões

O p99 da latência de serviço a 100 TPS, na mesma configuração e no mesmo plano
de energia, mediu 9,1 ms, 34,9 ms e 1,3 ms em três sessões diferentes. A taxa
não explica a diferença, e um teste pareado em 28/09 descartou o plano de
energia como causa (seção 6.9 do `docs/experimento.md`).

A consequência prática: **o p99 de uma rodada isolada não é comparável ao de
outra sessão.** Comparações de cauda exigem rodadas pareadas na mesma sessão.
A mediana, em contraste, é estável — variou apenas entre 235 e 250 µs nos
mesmos testes.

### De onde vem o teto

Do temporizador do sistema operacional. O `internal/clock` corrigiu a
**medição**, não o **agendamento**: o `time.Timer` do Go desvia do prazo pedido
de ~900 µs *antes* a dezenas de milissegundos *depois*, com mediana na casa do
milissegundo, independentemente do prazo. Um intervalo entre chegadas menor que
isso não é realizável.

> Medições exploratórias anteriores à correção do relógio sugeriram teto
> próximo de 2000 TPS. Aquele número era um artefato: agenda e medição usavam o
> mesmo relógio de passo grosseiro, e o atraso do temporizador era invisível
> para a própria medição. O teto real é quatro vezes menor.

### Fidelidade da latência do mock

O `time.Sleep` do mock está preso à mesma granularidade. A latência entregue
excede a configurada em 0,5 a 0,75 ms, aproximadamente constante:

| `--latency-base` | mediana medida | erro relativo |
|------------------|----------------|---------------|
| 1 ms | 1718 µs | +72% |
| 5 ms | 5743 µs | +15% |
| 20 ms | 20479 µs | +2,4% |

Latências configuradas abaixo de ~10 ms não são fiéis ao valor declarado. Como
autorizadores reais operam na faixa de dezenas de milissegundos, a restrição
não atrapalha o experimento pretendido — mas o valor a reportar no artigo é o
**medido**, não o configurado.

Os três limites estão detalhados em [docs/experimento.md](docs/experimento.md),
seções 4.4, 6.7 e 6.8.

## Verificação manual

O injetor acima já exercita o caminho completo, mas usa o mesmo código de
montagem e enquadramento do autorizador — se ambos estiverem errados da mesma
forma, o teste passa. O procedimento abaixo envia bytes crus, sem depender de
nenhum código deste repositório, e por isso verifica o formato de fio de forma
independente.

Com o autorizador em execução em outro terminal, envie uma `0100` canônica e
leia a `0110`.

A requisição abaixo tem 120 bytes de payload. O PAN `9999990000000014` é
sintético, válido por Luhn, e começa por 9 — faixa que o ISO/IEC 7812 reserva
para atribuição nacional e que não é alocada a nenhum esquema internacional de
cartões.

### PowerShell (Windows)

```powershell
$req = "0100723844010880800016999999000000001400000000000001000009051430000000011430000905541102106000001000000000001TERM0001986"
$payload = [Text.Encoding]::ASCII.GetBytes($req)
$prefixo = [BitConverter]::GetBytes([uint16]$payload.Length)
[Array]::Reverse($prefixo)                       # prefixo de 2 bytes big-endian
$frame = [byte[]]($prefixo + $payload)

$cliente = New-Object Net.Sockets.TcpClient('127.0.0.1', 8583)
$fluxo = $cliente.GetStream()
$fluxo.Write($frame, 0, $frame.Length)
$fluxo.Flush()

$cab = New-Object byte[] 2
$null = $fluxo.Read($cab, 0, 2)
[Array]::Reverse($cab)
$n = [BitConverter]::ToUInt16($cab, 0)
$buf = New-Object byte[] $n
$lidos = 0
while ($lidos -lt $n) { $lidos += $fluxo.Read($buf, $lidos, $n - $lidos) }
$cliente.Close()

Write-Output "enviado  ($($payload.Length) bytes): $req"
Write-Output "recebido ($n bytes): $([Text.Encoding]::ASCII.GetString($buf))"
```

### Bash (Linux/macOS, com `xxd` e `nc`)

```sh
REQ='0100723844010880800016999999000000001400000000000001000009051430000000011430000905541102106000001000000000001TERM0001986'
{ printf '\x00\x78'; printf '%s' "$REQ"; } | nc 127.0.0.1 8583 | xxd
```

O prefixo `\x00\x78` é o tamanho 120 em big-endian.

### Saída esperada

```
enviado  (120 bytes): 0100723844010880800016999999000000001400000000000001000009051430000000011430000905541102106000001000000000001TERM0001986
recebido (115 bytes): 0110723800010A808000169999990000000014000000000000010000090514300000000114300009050600000100000000000100TERM0001986
```

Leitura da resposta, campo a campo:

| Trecho | DE | Significado |
|--------|----|-----------|
| `0110` | — | MTI de resposta de autorização |
| `723800010A808000` | — | bitmap primário: DEs 2, 3, 4, 7, 11, 12, 13, 32, 37, 39, 41 e 49 |
| `16` `9999990000000014` | 2 | PAN, LLVAR com tamanho 16 |
| `000000` | 3 | processing code |
| `000000010000` | 4 | valor: R$ 100,00 em centavos |
| `0905143000` | 7 | data/hora de transmissão |
| `000001` | 11 | STAN — correlaciona a resposta à requisição |
| `143000` | 12 | hora local |
| `0905` | 13 | data local |
| `06` `000001` | 32 | instituição adquirente, LLVAR com tamanho 6 |
| `000000000001` | 37 | RRN |
| `00` | 39 | **código de resposta: aprovado** |
| `TERM0001` | 41 | terminal |
| `986` | 49 | moeda |

A resposta tem 115 bytes contra os 120 da requisição: o DE 39 acrescenta 2
bytes, enquanto os DEs 18 (MCC, 4 bytes) e 22 (POS entry mode, 3 bytes) não são
ecoados. O critério está em [docs/experimento.md](docs/experimento.md).

## Estrutura

```
cmd/authorizer/      sistema sob teste — autorizador mock
cmd/injector/        gerador de carga
cmd/calibrate/       calibração dos limites do aparato
cmd/massa/           gerador da massa sintética
internal/iso8583/    spec, montagem, parse e enquadramento das mensagens
internal/ratelimit/  controle de taxa em modelo aberto
internal/clock/      relógio monotônico de alta resolução
internal/massa/      leitura e validação da massa
internal/metrics/    coleta de latência e consolidação
internal/massa/      leitura dos CSVs de entrada (pendente)
data/massa.csv       massa sintética de entrada (versionada)
results/             saída bruta, uma pasta por rodada
analysis/qui2/       teste de independência recusa × atributo
analysis/fila/       utilização e espera prevista do pool (M/M/c)
docs/experimento.md  ambiente, decisões de projeto e procedimento
```

## Licença

MIT. Ver [LICENSE](LICENSE).
