# Procedimento experimental

Documento de registro do aparato. Cada decisão que afeta a interpretação dos
números medidos é registrada aqui, com a justificativa.

Estado atual: **passo 3 da ordem de execução** (controle de taxa em modelo
aberto). As seções de ambiente, procedimento de execução e
resultados são preenchidas conforme os passos seguintes forem concluídos.

---

## 1. Perfil de mensagem

- **Norma:** ISO 8583, perfil 1987.
- **Codificação:** ASCII.
- **Bitmap:** primário apenas, 8 bytes representados em 16 dígitos
  hexadecimais ASCII.
- **Biblioteca:** `github.com/moov-io/iso8583 v0.26.1` para marshal e unmarshal.

Nenhuma especificação de bandeira é implementada, referenciada ou aproximada. O
layout empregado é o de domínio público da norma ISO 8583:1987.

### 1.1 Critério de seleção do subconjunto de data elements

O subconjunto abaixo é o **mínimo necessário para compor uma requisição de
autorização interoperável**: identificação do portador e do valor, coordenadas
temporais, identificação do ponto de captura e chaves de correlação entre
requisição e resposta. Nenhum elemento fora desse mínimo foi incluído, e em
particular nenhum subelemento privado de bandeira, que é justamente onde as
especificações proprietárias se diferenciam.

| DE | Conteúdo | Formato | Função no experimento |
|----|----------|---------|----------------------|
| 2  | PAN | LLVAR n..19 | Identificação do portador; varia a massa |
| 3  | Processing code | n6 | Natureza da transação |
| 4  | Valor da transação | n12 | Valor em centavos |
| 7  | Data/hora de transmissão | n10 (MMDDhhmmss) | Coordenada temporal da requisição |
| 11 | STAN | n6 | **Chave de correlação requisição/resposta** |
| 12 | Hora local | n6 | Coordenada temporal do ponto de captura |
| 13 | Data local | n4 | Coordenada temporal do ponto de captura |
| 18 | MCC | n4 | Categoria do estabelecimento |
| 22 | POS entry mode | n3 | Forma de captura |
| 32 | ID da instituição adquirente | LLVAR n..11 | Origem da transação |
| 37 | RRN | an12 | Referência de rastreamento |
| 39 | Código de resposta | an2 | **Resultado da autorização (só na resposta)** |
| 41 | Terminal ID | ans8 | Identificação do terminal |
| 49 | Moeda | n3 | Moeda do valor em DE 4 |

O **DE 11 (STAN)** merece destaque: é ele que correlaciona cada `0110` à `0100`
que a originou. Sem essa correlação não há medição de latência por transação, e
sim apenas contagem agregada. Por isso o eco íntegro do DE 11 é objeto de teste
automatizado.

### 1.2 Data elements ecoados na resposta

A `0110` ecoa os DEs **2, 3, 4, 7, 11, 12, 13, 32, 37, 41 e 49** presentes na
`0100`, e acrescenta o **DE 39**, gerado pelo autorizador.

Ficam deliberadamente de fora:

- **DE 18 (MCC)** e **DE 22 (POS entry mode)** — descrevem a natureza do
  estabelecimento e a forma de captura. São informação do lado da requisição e
  não têm função na resposta.
- **DE 39** não é ecoado porque não existe na requisição: é o resultado
  produzido pelo autorizador.

Um DE ausente na requisição não é inventado na resposta.

### 1.3 Ausência de padding nos campos

Nenhum campo do spec declara `padding`. A decisão não é estética e afeta a
auditabilidade dos dados brutos.

Com `Pad` definido, a biblioteca preenche o valor no *pack* e o **remove** no
*unpack*. O efeito colateral é que uma data local `0905` seria lida de volta
como `905`, e um POS entry mode `021` como `21`: o valor em memória deixaria de
corresponder ao valor que trafegou na rede.

Sem padding, o prefixador de tamanho fixo da biblioteca rejeita qualquer valor
de largura incorreta **no momento do pack**. O resultado é falha ruidosa em vez
de corrupção silenciosa do fluxo, e o valor lido após o *unpack* é byte a byte
idêntico ao que trafegou. O gerador de massa fica responsável por emitir todos
os campos fixos já na largura exata.

A garantia é objeto de teste (`TestPackRejeitaLarguraIncorreta`).

> Nota sobre a biblioteca: o `Spec87ASCII` distribuído pelo `moov-io/iso8583`
> declara o bitmap com `Length: 16`. Esse campo é contado em **bytes crus**, de
> modo que o valor 16 produz bitmap primário mais secundário em toda mensagem.
> O spec deste experimento usa `Length: 8`, que corresponde ao bitmap primário
> exigido pelo perfil adotado.

---

## 2. Transporte

TCP puro sobre `127.0.0.1`, com **prefixo de tamanho de 2 bytes big-endian**
precedendo cada mensagem. É a convenção mais comum em autorizadores e é trivial
de documentar.

Frame:

```
+--------+--------+------------------------------+
| tam_hi | tam_lo |  payload ISO 8583 (ASCII)    |
+--------+--------+------------------------------+
   1 byte  1 byte    'tam' bytes, máx. 65535
```

Duas decisões de implementação com efeito sobre a medição:

1. **A escrita do prefixo e do payload ocorre em uma única chamada a `Write`.**
   Duas chamadas separadas poderiam ser entregues em segmentos TCP distintos,
   somando um atraso de rede à latência medida sem que ele pertença ao sistema
   sob teste.
2. **A leitura usa `io.ReadFull`, nunca `Read` direto.** Uma chamada a `Read`
   pode devolver menos bytes que o solicitado sem que isso seja erro — situação
   esperada em socket TCP sob carga. Tratar o retorno curto como frame completo
   dessincronizaria o fluxo de forma silenciosa e contaminaria todas as medições
   subsequentes daquela conexão. O comportamento é objeto de teste com um
   leitor que devolve um byte por chamada (`TestReadFrameLeituraFragmentada`).

O encerramento limpo da conexão, que ocorre entre frames, é distinguido de um
frame truncado: o primeiro devolve `io.EOF`, o segundo devolve erro. Erros de
transporte precisam ser contabilizados separadamente das recusas de negócio.

---

## 3. Massa de dados

Toda a massa é sintética e gerada com semente fixa. Nenhum dado real é
utilizado.

Os PANs são válidos por Luhn e começam pelo dígito **9**. O ISO/IEC 7812 reserva
o *Major Industry Identifier* 9 para atribuição nacional — faixa não alocada a
nenhum esquema internacional de cartões. Isso garante que nenhum BIN real em uso
seja emitido pelo gerador.

PAN canônico usado nos testes e na verificação manual: `9999990000000014`.

---

## 4. Autorizador mock

O sistema sob teste precisa ser **previsível, não realista**. Se o comportamento
dele for opaco, não há como atribuir uma variação de latência ao injetor ou ao
autorizador.

**Estado atual:** servidor TCP em `127.0.0.1:8583`, sem flags e sem métricas.
O DE 39 é sempre `00`. Cada conexão aceita múltiplos pares `0100`/`0110`, e as
conexões são atendidas **concorrentemente**, cada uma em sua própria goroutine.

O atendimento em série do passo 1 foi substituído no passo 3 por necessidade, e
não por antecipação: com um pool de conexões no injetor, apenas a primeira
seria atendida e as demais ficariam paradas na fila de *accept* do sistema
operacional. O injetor mediria essa espera como latência do autorizador — um
artefato de aparato que invalidaria o experimento. O teto de conexões
simultâneas (`--max-conns`), que serve ao propósito oposto de provocar
saturação de forma controlada, continua pendente para o passo 5.

Ainda não implementado, nos passos seguintes: latência de serviço configurável e
sua distribuição, taxa de aprovação, distribuição dos códigos de recusa, teto de
conexões simultâneas e semente.

---

## 5. Injetor

**Estado atual (passo 3):** aplica uma taxa de chegada fixa em modelo aberto,
por uma duração configurável. Sem coleta de latência com histograma, sem
warm-up, sem semente e sem arquivos de saída — esses são o passo 4.

### 5.1 Origem dos campos temporais

Os DEs 7, 12 e 13 derivam de um único instante, e não de três leituras
independentes do relógio — três leituras poderiam cair em segundos diferentes e
produzir uma mensagem internamente inconsistente.

| DE | Fuso | Formato |
|----|------|---------|
| 7  | UTC | `MMDDhhmmss` |
| 12 | local | `hhmmss` |
| 13 | local | `MMDD` |

O DE 7 é a data/hora de **transmissão** e segue a convenção da norma de ser
expresso em UTC. Os DEs 12 e 13 são hora e data **locais do ponto de captura**.
Em fuso UTC-3 os dois diferem em três horas, e a distinção é verificada em
`TestRequisicaoDerivaCamposTemporais` com um instante em fuso deslocado — se
todos os campos usassem o mesmo fuso, o teste não distinguiria os dois casos.

### 5.2 Modelo aberto de chegadas

As chegadas são independentes das conclusões: o instante em que cada requisição
deve partir é determinado apenas pelo relógio, nunca pelo término da requisição
anterior. Cada envio ocorre em sua própria goroutine.

O contraste é com o modelo fechado, em que uma resposta lenta atrasa a próxima
requisição, o sistema nunca é submetido à taxa pretendida e a cauda da
distribuição desaparece da medição — Schroeder, Wierman e Harchol-Balter (2006).

Duas decisões de implementação:

**O instante de cada chegada é calculado a partir do índice**, como
`round(i / TPS)`, e nunca pela acumulação de um intervalo. A 3 TPS o intervalo
é 333,333 ms com dízima, não representável em nanossegundos; somar
repetidamente o valor truncado faria a taxa efetiva derivar ao longo da rodada,
e a taxa declarada no artigo deixaria de corresponder à taxa aplicada. Coberto
por `TestDeslocamentoNaoDeriva`.

**Não há teto para o número de requisições simultâneas em voo.** Um teto
transformaria o modelo aberto em fechado assim que fosse atingido, escondendo
justamente a saturação que o experimento pretende medir. A contrapartida é que,
se o autorizador parar de responder, o consumo de memória do injetor cresce com
a taxa. É um comportamento aceito e declarado, não um descuido.

O pool de conexões é o único ponto de espera, e a espera é legítima: é o que um
cliente real com pool limitado observa, e ela é medida a partir do instante de
chegada pretendido. As conexões são todas abertas **antes** do início da
rodada — o custo de estabelecer uma conexão TCP, mesmo em loopback, é da mesma
ordem de grandeza do tempo de serviço do mock e dominaria a latência se fosse
pago dentro da requisição.

Uma conexão que falha no meio de uma troca **não volta ao pool**: o fluxo pode
ter ficado dessincronizado, e a próxima requisição a usá-la leria a resposta
errada, quebrando a correlação por STAN em silêncio. Ela é fechada e
substituída, para que o pool não encolha ao longo da rodada.

### 5.3 Vazão alcançada: o denominador correto

A vazão alcançada é calculada sobre a **janela de chegadas**, não sobre o tempo
decorrido até a última resposta.

As N chegadas de uma rodada ocupam os instantes 0, 1/TPS, 2/TPS, até
(N−1)/TPS — uma janela que termina um intervalo antes do fim da rodada.
Dividir o número de respostas pelo tempo decorrido produz vazão **acima de 100%
do alvo**, o que é impossível em modelo aberto: nenhuma resposta pode ser
contada antes de sua chegada ter sido agendada. O erro foi observado na prática
(uma rodada de 10 TPS por 5 s reportou 10,20 TPS, ou 102% do alvo) antes de ser
corrigido.

Com a janela de chegadas como denominador, a vazão só fica abaixo do alvo
quando alguma requisição deixou de ser respondida — que é exatamente o sinal de
saturação que a métrica deve capturar.

### 5.4 Atraso de agendamento: médio, não máximo

O injetor registra duas medidas do próprio desvio em relação ao plano:

| Medida | O que indica |
|--------|--------------|
| atraso **médio** | atraso sistemático — diagnóstico de saturação do injetor |
| atraso **máximo** | pior caso — sensível a pausas do GC e ao temporizador do SO |

O critério de saturação usa o **médio**. Uma versão anterior usava o máximo e
disparava o aviso em rodadas de 500 TPS em que todas as 2500 chegadas foram
despachadas e respondidas dentro da janela: um único sobressalto de 8 ms basta
para elevar o máximo sem que o injetor tenha deixado de sustentar a taxa. Com o
critério pelo máximo, o aviso viraria ruído em qualquer rodada de taxa alta e
perderia utilidade. Coberto por `TestRelatarNaoAvisaPorPicoIsolado`.

### 5.5 O que ainda não é medição

Nenhum número produzido pelo injetor até aqui é resultado de experimento. Não
há warm-up, semente, histograma de latência nem registro do ambiente de
execução. A latência por transação, com correção de omissão coordenada e
escrita de `raw.csv` e `summary.json`, é o passo 4.

### 5.6 Limite da correlação por STAN

O STAN deriva do índice da chegada, o que garante unicidade dentro da rodada e
permite conferir a correspondência entre requisição e resposta — a conferência
é feita em toda troca, e uma divergência é tratada como erro de transporte.

O DE 11 tem seis dígitos, então a numeração reinicia a cada **1.000.000** de
requisições. Uma rodada mais longa que isso precisaria de outra chave de
correlação. A 2000 TPS, o limite corresponde a cerca de 8 minutos de rodada
contínua.

### 5.7 Limite do teste automatizado

O teste ponta a ponta do injetor sobe um servidor que reproduz o comportamento
do autorizador usando o **mesmo caminho de código** de montagem da resposta —
o binário do autorizador vive em outro `package main` e não pode ser importado.
Um erro comum às duas pontas, portanto, passaria despercebido por ele.

Essa lacuna é coberta pela verificação manual documentada no README, que envia
bytes crus sem depender de nenhum código deste repositório e confere o formato
de fio de forma independente.

---

## 6. Teto preliminar do injetor

Medições exploratórias nesta máquina, com o autorizador respondendo sem
latência artificial e 32 conexões, rodadas de 5 s:

| Alvo | Chegadas | Respondidas | Atraso médio | Atraso máximo |
|------|----------|-------------|--------------|---------------|
| 10 TPS | 50 | 50 | 659 µs | 3,56 ms |
| 500 TPS | 2500 | 2500 | 562 µs | 4,67 ms |
| 5000 TPS | 25000 | 25000 | 511 µs | 19,22 ms |

O atraso médio se estabiliza em torno de **500 a 660 µs, independentemente da
taxa pedida**. Isso não é contenção do injetor: é o piso de granularidade do
temporizador do sistema operacional. O comportamento implica um teto de injeção
próximo de **2000 TPS**, taxa em que o intervalo entre chegadas (500 µs) cruza
esse piso.

Acima desse ponto o injetor continua entregando o número correto de chegadas,
mas não nos instantes pretendidos: o espaçamento deixa de ser uniforme e a
carga passa a chegar em rajadas.

> Estes números são **preliminares**. A calibração formal é o passo 6, e o
> valor apurado lá vai para o artigo como limite declarado do aparato. Nenhum
> experimento deve ser executado em taxa acima do teto de calibração sem que
> isso seja explicitamente discutido: naquele nível de carga o resultado mede o
> injetor, não o autorizador.

## 7. Ambiente de execução

*A ser preenchido quando os experimentos forem executados.* O bloco de ambiente
completo — versão do Go, `GOMAXPROCS`, número de CPUs, sistema operacional,
`GOGC`, todas as flags dos dois processos, semente e linha de comando exata —
será gravado em cada `summary.json`.

Versão do Go usada no desenvolvimento até aqui: **go1.25.3 windows/amd64**.
Compilador C para o detector de corrida: **gcc 16.1.0** (MinGW-W64
x86_64-ucrt-posix-seh, WinLibs).

### 7.1 Verificação de concorrência

Injetor e autorizador são concorrentes, e o detector de corrida integra a
verificação. Estado na conclusão do passo 3:

| Verificação | Resultado |
|-------------|-----------|
| `go test -race -count=3 ./...` | sem corridas nos quatro pacotes |
| binários instrumentados, 1000 TPS por 8 s, 64 conexões | 8000/8000 respondidas, sem corridas |

A segunda linha importa porque os testes automatizados usam um autorizador de
mentira: o caminho de atendimento concorrente do binário real (`go handleConn`)
só é exercitado executando os dois processos sob carga, com `GORACE` em
`halt_on_error=1`.
