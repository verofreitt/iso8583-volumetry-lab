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

Implementado até o **passo 5** da ordem de execução: o autorizador mock é
configurável e determinístico, e o injetor mede a latência de cada requisição
com correção de omissão coordenada.

| Passo | Componente | Estado |
|-------|-----------|--------|
| 1 | Autorizador responde a uma `0100` | **concluído** |
| 2 | Injetor envia `0100` e lê a resposta | **concluído** |
| 3 | Controle de taxa em modelo aberto | **concluído** |
| 4 | Coleta de latência, `raw.csv` e `summary.json` | **concluído** |
| 5 | Flags de configuração do mock | **concluído** |
| 6 | Baseline de calibração | pendente |
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
| `-results` | `results` | raiz onde a pasta da rodada é criada |
| `-sut-config` | — | arquivo gravado pelo autorizador com `--config-out` |

> A flag `-seed` é registrada no `summary.json` mas ainda **não tem efeito**:
> a massa sintética (`internal/massa`) não existe, e todas as requisições usam
> os mesmos valores. O campo já está no esquema para que ele não mude quando a
> massa chegar.

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

## Limites conhecidos do aparato

Dois pisos desta máquina, apurados de forma exploratória e a serem
quantificados com rigor no passo 6:

**Teto de injeção, ~2000 TPS.** O atraso médio de agendamento se estabiliza em
500–660 µs independentemente da taxa pedida — é o piso de granularidade do
temporizador do sistema operacional. Acima desse ponto o injetor entrega o
número correto de chegadas, mas não nos instantes pretendidos.

**Piso de ruído no p99, milissegundos.** Com o autorizador em `--echo-only`, o
p99 da latência de serviço fica em alguns milissegundos e não cresce com a
carga, o que descarta enfileiramento: é ruído ambiente da máquina. O p99 de uma
rodada só diz algo sobre o autorizador se a latência configurada estiver bem
acima desse piso.

**Fidelidade da latência do mock, excesso de ~0,7 ms.** O `time.Sleep` do mock
está preso à mesma granularidade de temporizador. A latência entregue excede a
configurada em 0,5 a 0,75 ms, aproximadamente constante:

| `--latency-base` | mediana medida | erro relativo |
|------------------|----------------|---------------|
| 1 ms | 1718 µs | +72% |
| 5 ms | 5743 µs | +15% |
| 20 ms | 20479 µs | +2,4% |

Latências configuradas abaixo de ~10 ms não são fiéis ao valor declarado. Como
autorizadores reais operam na faixa de dezenas de milissegundos, a restrição
não atrapalha o experimento pretendido — mas o valor a reportar no artigo é o
**medido**, não o configurado.

Ambos estão detalhados em [docs/experimento.md](docs/experimento.md).

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
internal/iso8583/    spec, montagem, parse e enquadramento das mensagens
internal/ratelimit/  controle de taxa em modelo aberto
internal/clock/      relógio monotônico de alta resolução
internal/metrics/    coleta de latência e consolidação
internal/massa/      leitura dos CSVs de entrada (pendente)
data/                massa sintética
results/             saída bruta, uma pasta por rodada
analysis/            scripts de estatística e gráficos
docs/experimento.md  ambiente, decisões de projeto e procedimento
```

## Licença

MIT. Ver [LICENSE](LICENSE).
