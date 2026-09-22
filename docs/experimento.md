# Procedimento experimental

Documento de registro do aparato. Cada decisão que afeta a interpretação dos
números medidos é registrada aqui, com a justificativa.

Estado atual: **passo 5 da ordem de execução** (flags de configuração do
autorizador mock). As seções de ambiente, procedimento de execução e
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

Não há lógica de negócio, cache nem qualquer adaptação dinâmica ao volume.
Qualquer não-linearidade no resultado precisa vir de contenção real de
recursos, e não de esperteza do mock.

### 4.1 Parâmetros

| Flag | Padrão | Efeito |
|------|--------|--------|
| `--latency-base` | `0s` | latência de serviço base |
| `--latency-jitter` | `0s` | média da dispersão somada à base |
| `--latency-dist` | `exponencial` | forma da dispersão: `exponencial` ou `lognormal` |
| `--approval-rate` | `1` | proporção de respostas `00` |
| `--decline-dist` | `51:40,05:30,14:20,91:10` | pesos dos códigos de recusa |
| `--max-conns` | `0` | teto de requisições simultâneas; 0 remove o teto |
| `--seed` | `1` | semente das decisões |
| `--echo-only` | `false` | responde imediatamente, sem latência nem sorteio |
| `--config-out` | — | arquivo onde a configuração e o ambiente são gravados |
| `--quiet` | `false` | suprime o log por conexão |

### 4.2 Determinismo: função pura de (semente, STAN)

Cada decisão — a latência a aplicar e o código do DE 39 — é uma **função pura
da semente e do STAN da requisição**. Não há estado compartilhado, não há trava,
e o resultado independe da ordem de chegada ou de qual goroutine atende.

A alternativa óbvia, um gerador pseudoaleatório compartilhado protegido por
mutex, produziria uma *sequência* determinada pela semente, mas o **mapeamento**
entre valores sorteados e requisições dependeria do escalonador. A distribuição
agregada seria estável, e ainda assim duas execuções idênticas dariam respostas
diferentes para a mesma transação. Isso contraria a exigência de
reprodutibilidade bit a bit, que a seção 1 do CLAUDE.md lista como prioridade
alta.

A derivação usa FNV-1a sobre o STAN, misturado com a semente, e splitmix64 para
os sorteios subsequentes. O DE 11 é a chave porque já é único dentro da rodada
e já é a chave de correlação entre requisição e resposta.

Consequência prática: repetições da mesma rodada com a mesma semente recebem as
mesmas respostas, porque os STANs se repetem. Para obter sorteios diferentes
entre repetições, mude a semente do autorizador.

Verificado em `TestAutorizadorMesmaSementeMesmasRespostas` e, ponta a ponta,
comparando a coluna `de39` do `raw.csv` de duas rodadas independentes.

### 4.3 Distribuições da dispersão

Nas duas distribuições, **a média da dispersão é o valor de
`--latency-jitter`**, e o valor sorteado é sempre positivo. A latência de
serviço é `--latency-base` mais a dispersão.

| Distribuição | Parametrização | Característica |
|--------------|----------------|----------------|
| `exponencial` | média = jitter | sem memória, cauda moderada |
| `lognormal` | média = jitter, σ = 1, logo μ = ln(jitter) − ½ | cauda mais pesada para a mesma média |

O σ da lognormal fica **fixo em 1** para que a distribuição seja descrita por um
único parâmetro e para que o artigo declare a parametrização sem ambiguidade.

A exponencial é a escolha natural para tempo de serviço sem memória. A
lognormal existe para investigar percentis altos sem elevar a média, que é o
regime em que a discussão de cauda do artigo se situa.

Não há teto para o valor sorteado. Um teto truncaria justamente a cauda que o
experimento investiga.

### 4.4 Fidelidade da latência configurada

O mock aplica a latência com `time.Sleep`, que está sujeito à granularidade do
temporizador do sistema operacional — a mesma que limita o agendamento do
injetor. A latência entregue tem, portanto, um excesso sistemático.

Medido nesta máquina, a 100 TPS, com jitter zero:

| `--latency-base` | mediana medida | excesso | erro relativo |
|------------------|----------------|---------|---------------|
| 500 µs | 1215 µs | +715 µs | **+143%** |
| 1 ms | 1718 µs | +718 µs | +72% |
| 2 ms | 2725 µs | +725 µs | +36% |
| 5 ms | 5743 µs | +743 µs | +15% |
| 20 ms | 20479 µs | +479 µs | **+2,4%** |

O excesso é aproximadamente **constante em 0,5 a 0,75 ms**, e reúne a
granularidade do `time.Sleep` com o próprio tempo de ida e volta em loopback
(mediana de 177 µs em modo eco). O erro *relativo* só se torna desprezível
acima de cerca de 20 ms.

> **Consequência para o desenho dos experimentos:** latências de serviço
> configuradas abaixo de ~10 ms não são fiéis ao valor declarado. Como
> autorizadores reais operam na faixa de dezenas de milissegundos, a restrição
> não atrapalha o experimento pretendido — mas precisa ser declarada, e o valor
> a reportar no artigo é o **medido**, não o configurado.

### 4.5 `--max-conns`: teto por requisição, não por conexão

O teto é aplicado a **requisições atendidas simultaneamente**, ainda que a flag
se chame `--max-conns`.

Aplicado por conexão, como o nome sugere, o parâmetro seria uma função degrau
contra um cliente que mantém conexões persistentes: abaixo do tamanho do pool
do injetor não teria efeito algum, e acima dele as conexões excedentes ficariam
paradas para sempre e todas as suas requisições expirariam. Não produziria
curva de saturação, que é o propósito declarado do parâmetro na seção 4 do
CLAUDE.md.

Aplicado por requisição, o teto enfileira de verdade, e a espera aparece na
latência medida. É o modelo de um servidor com concorrência limitada.

Coberto por `TestAutorizadorRespeitaTetoDeSimultaneas` e seu contraponto sem
teto.

### 4.6 `--echo-only`

Responde imediatamente, sem latência e sem sorteio, sempre com `00`. É o alvo
trivial exigido pela seção 7.2 do CLAUDE.md para descobrir em que TPS o
*injetor* satura sozinho, e ignora todos os demais parâmetros.

O caminho é o mais curto possível: nem o gerador pseudoaleatório é consultado.

### 4.7 Registro da configuração dos dois processos

A seção 5.4 do CLAUDE.md exige as flags dos **dois** processos no
`summary.json`. Transcrevê-las à mão seria a parte mais frágil da cadeia de
auditoria, então o próprio autorizador as grava:

```sh
authorizer --latency-base 20ms --config-out autorizador.json
injector   --sut-config autorizador.json
```

O arquivo traz a configuração e o ambiente do autorizador — incluindo o
`GOMAXPROCS` dele, que a seção 7.1 exige poder fixar por processo. O injetor o
embute no `summary.json` como JSON aninhado, e não como texto, para que a
análise leia os parâmetros do sistema sob teste diretamente.

A troca passa por arquivo, e não pela rede: consultar o autorizador durante a
rodada acrescentaria um caminho de código ao sistema sob teste, e o experimento
depende de esse caminho ser o mais simples possível.

Se o arquivo não for informado ou não puder ser lido, a rodada **não é
abortada** — abortar custaria a medição inteira. A ausência fica registrada no
`summary.json` como tal, e a análise distingue uma rodada com a configuração do
sistema sob teste de uma sem.

---

## 5. Injetor

**Estado atual (passo 4):** aplica uma taxa de chegada fixa em modelo aberto,
coleta a latência de cada requisição com correção de omissão coordenada, e
grava `raw.csv` e `summary.json`.

Pendente: a leitura da massa sintética dos CSVs de entrada (`internal/massa`).
Enquanto ela não existe, todas as requisições usam os mesmos valores, e a flag
`-seed` é registrada no `summary.json` sem ter efeito observável. O campo já
existe no esquema para que ele não mude quando a massa chegar.

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

### 5.5 Limite da correlação por STAN

O STAN deriva do índice da chegada, o que garante unicidade dentro da rodada e
permite conferir a correspondência entre requisição e resposta — a conferência
é feita em toda troca, e uma divergência é tratada como erro de transporte.

O DE 11 tem seis dígitos, então a numeração reinicia a cada **1.000.000** de
requisições. Uma rodada mais longa que isso precisaria de outra chave de
correlação. A 2000 TPS, o limite corresponde a cerca de 8 minutos de rodada
contínua.

### 5.6 Limite do teste automatizado

O teste ponta a ponta do injetor sobe um servidor que reproduz o comportamento
do autorizador usando o **mesmo caminho de código** de montagem da resposta —
o binário do autorizador vive em outro `package main` e não pode ser importado.
Um erro comum às duas pontas, portanto, passaria despercebido por ele.

Essa lacuna é coberta pela verificação manual documentada no README, que envia
bytes crus sem depender de nenhum código deste repositório e confere o formato
de fio de forma independente.

### 5.7 Coleta de latência

A coleta não usa travas. O número de chegadas é conhecido antes do início da
rodada, então cada requisição escreve em uma posição própria de um vetor
pré-alocado, indexada pelo índice da chegada. Posições distintas de um slice
são memória independente, e não há corrida.

A alternativa usual — um mutex em volta do histograma no caminho de cada
requisição — introduziria contenção exatamente no ponto que o experimento
pretende medir. O histograma é montado ao final, a partir dos registros brutos,
que são também o conteúdo do `raw.csv`.

Os percentis vêm de um HdrHistogram sobre os valores individuais, com faixa de
1 µs a 60 s e três dígitos significativos. Nunca são calculados sobre média
móvel ou amostragem: as duas práticas destroem a cauda, que é o objeto do
experimento.

### 5.8 O relógio: por que o `time.Now` não serve no Windows

Esta seção registra um defeito encontrado e corrigido **antes** de qualquer
experimento, e cuja omissão teria invalidado toda a medição de baixa latência.

No Windows, o `time.Now` do Go não usa o *QueryPerformanceCounter*: lê o tempo
de interrupção do sistema, cuja granularidade é a do tique do temporizador.
Medido nesta máquina:

| Grandeza | Valor |
|----------|-------|
| menor passo não-nulo entre leituras sucessivas de `time.Now` | 331 µs |
| passo mediano | 534 µs |
| granularidade do relógio de parede | ~30 ms |
| resolução do `QueryPerformanceCounter` | **100 ns** |

Uma troca de mensagens em loopback leva cerca de 125 a 300 µs — abaixo do passo
do relógio do runtime. O efeito foi observado numa rodada de 200 TPS antes da
correção: os campos `ts_envio` e `ts_resposta` do `raw.csv` saíam **idênticos
até o último dígito**, e a mediana da latência de serviço saía em zero.

```
stan,ts_agendado,ts_envio,ts_resposta,latencia_servico_us,...
001000,...12.7175509-03:00,...12.7180881-03:00,...12.7180881-03:00,0,537,00,
```

O `internal/clock` corrige isso ancorando **uma única** leitura de parede a um
contador monotônico de alta resolução. Todos os instantes seguintes são a
âncora mais o deslocamento monotônico, de modo que a diferença entre dois
instantes tem a precisão do contador, e não a do tique do sistema. A exatidão
absoluta em relação ao horário civil continua sendo a da âncora, o que é
irrelevante para medir durações.

Depois da correção, a mesma rodada de 200 TPS:

```
stan,ts_agendado,ts_envio,ts_resposta,latencia_servico_us,...
001000,...50.9711895-03:00,...50.9716797-03:00,...50.9718641-03:00,184,675,00,
```

Mediana da latência de serviço: **270 µs**, com mínimo de 125 µs. Os percentis
calculados diretamente do CSV conferem com os do histograma, o que valida a
consolidação.

A fonte e a resolução do relógio entram no bloco de ambiente do
`summary.json`. Uma latência da ordem da resolução do relógio não é mensurável,
e o artigo precisa declarar esse piso em vez de apresentar números abaixo dele.

> **A correção é da medição, não do agendamento.** A espera entre chegadas
> continua sujeita à granularidade do temporizador do sistema operacional, da
> ordem de centenas de microssegundos. É dela que vem o teto de injeção
> discutido na seção 6, e é por isso que o atraso de agendamento é medido e
> reportado separadamente.

### 5.9 Arquivos de saída

Uma pasta por rodada, em `results/<timestamp>-<tps>-<rep>/`.

**`raw.csv`** — uma linha por requisição medida, nas colunas definidas na seção
5.4 do CLAUDE.md:

```
stan, ts_agendado, ts_envio, ts_resposta,
latencia_servico_us, latencia_resposta_us, de39, erro_transporte
```

As duas latências ocupam colunas separadas para que a análise possa compará-las
e o artigo discutir a diferença. Linhas com falha de transporte têm as duas em
branco: zero seria indistinguível de uma resposta instantânea. As chegadas do
warm-up não são escritas — mantê-las no arquivo bruto convidaria a incluí-las
na análise por engano.

A conversão para microssegundos arredonda, e não trunca: truncar levaria toda
latência submicrossegundo a zero, e um zero no arquivo bruto seria lido como
medição instantânea.

**`summary.json`** — o resumo consolidado mais o bloco de ambiente completo:
versão do Go, `GOMAXPROCS`, número de CPUs, sistema operacional, arquitetura,
`GOGC`, fonte e resolução do relógio, todas as flags do injetor, a
configuração do autorizador e a linha de comando exata.

O bloco do autorizador é hoje preenchido pela flag `-sut`, que registra a linha
de comando informada por quem executa. Quando o autorizador ganhar suas
próprias flags, no passo 5, o mecanismo passa a ser automático.

### 5.10 Warm-up

O warm-up é delimitado por **índice de chegada**, não por relógio: a chegada de
índice *i* ocorre no instante *i*/TPS, então as chegadas do warm-up são
exatamente as de índice menor que *warmup*×TPS. Delimitar por índice torna o
recorte idêntico entre rodadas, sem depender do instante em que o processo
começou.

Uma rodada com warm-up maior ou igual à duração é recusada: não sobraria nada
para medir.

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

### 6.1 Piso de ruído da máquina

Medição exploratória com o autorizador respondendo sem latência artificial,
8 conexões, rodadas de 10 s com 2 s de warm-up:

| Alvo | Mediana serviço | p95 serviço | p99 serviço | máximo serviço |
|------|-----------------|-------------|-------------|----------------|
| 20 TPS | 979 µs | 4,2 ms | **25,7 ms** | 36,6 ms |
| 200 TPS | 794 µs | 6,2 ms | **11,6 ms** | 22,2 ms |

A cauda **não cresce com a carga** — a 20 TPS o p99 é pior que a 200 TPS. Isso
descarta enfileiramento como explicação: o que se vê é ruído ambiente da
máquina (escalonamento do Windows, gerenciamento de energia, processos de
fundo, pausas do coletor de lixo dos dois processos).

A consequência para o experimento é direta: **o p99 de uma rodada só diz algo
sobre o autorizador se a latência de serviço configurada estiver bem acima
desse piso.** Abaixo dele, a cauda medida é a da máquina, não a do sistema sob
teste.

Quantificar esse piso com rigor, e não por amostragem exploratória, é o
propósito do passo 6. O valor apurado vai para o artigo como limite declarado
do aparato, ao lado do teto de injeção.

---

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
