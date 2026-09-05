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

Implementado até o **passo 2** da ordem de execução: o caminho ponta a ponta
está fechado — o injetor envia uma `0100` e lê a `0110`. Uma requisição por
execução, sem controle de taxa, sem coleta de latência e sem flags.

| Passo | Componente | Estado |
|-------|-----------|--------|
| 1 | Autorizador responde a uma `0100` | **concluído** |
| 2 | Injetor envia `0100` e lê a resposta | **concluído** |
| 3 | Controle de taxa em modelo aberto | pendente |
| 4 | Coleta de latência, `raw.csv` e `summary.json` | pendente |
| 5 | Flags de configuração do mock | pendente |
| 6 | Baseline de calibração | pendente |
| 7 | Execução dos experimentos | pendente |

## Requisitos

- Go 1.25.3 ou superior (a versão está travada em `go.mod`).

A única dependência direta é `github.com/moov-io/iso8583`.

## Compilação e testes

```sh
go build ./...
go test ./...
```

## Executando o autorizador

```sh
go run ./cmd/authorizer
```

Ele escuta em `127.0.0.1:8583` e registra em *stderr* cada conexão aceita e
encerrada:

```
autorizador escutando em 127.0.0.1:8583
```

Encerre com `Ctrl+C`.

## Executando o injetor

Com o autorizador em execução em outro terminal:

```sh
go run ./cmd/injector
```

Ele envia uma única `0100`, lê a `0110` e confere a correlação pelo STAN:

```
conectado a 127.0.0.1:8583
resposta   : 0110723800010A808000169999990000000014...00TERM0001986
MTI        : 0110
DE 11 STAN : 000001
DE 39      : 00
decorrido  : 535.2µs
```

Saída em `stdout`, log em `stderr`. O processo termina com status diferente de
zero se a resposta não vier, se o MTI não for `0110` ou se o STAN divergir do
enviado.

> **O tempo em `decorrido` não é um resultado.** É apenas um sinal de vida. A
> medição que sustenta o artigo depende do modelo aberto e da correção da
> omissão coordenada, ambos ainda não implementados (passos 3 e 4). Até lá,
> nenhum número produzido por este binário deve ser tratado como dado.

Os DEs 7, 12 e 13 vêm do relógio, então variam a cada execução: o DE 7 é a
data/hora de transmissão em UTC e os DEs 12 e 13 são hora e data locais do
ponto de captura. Em fuso UTC-3 os dois diferem em três horas, o que é visível
na saída.

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
internal/ratelimit/  controle de taxa em modelo aberto (pendente)
internal/metrics/    coleta de latência e consolidação (pendente)
internal/massa/      leitura dos CSVs de entrada (pendente)
data/                massa sintética
results/             saída bruta, uma pasta por rodada
analysis/            scripts de estatística e gráficos
docs/experimento.md  ambiente, decisões de projeto e procedimento
```

## Licença

MIT. Ver [LICENSE](LICENSE).
