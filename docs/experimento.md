# Procedimento experimental

Documento de registro do aparato. Cada decisão que afeta a interpretação dos
números medidos é registrada aqui, com a justificativa.

Estado atual: **capacidade de detecção validada** com controle negativo e
positivo (seção 9). Os limites declarados do aparato estão na seção 6.7; as
decisões revistas durante a execução, na seção 8. As seções de ambiente, procedimento de execução e
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

Toda a massa é sintética. Nenhum dado real é utilizado.

### 3.1 Um arquivo de entrada, não três

Os resultados preliminares descreviam **três** CSVs: um para a massa de
mensagens de entrada, outro para registro de tempo de resposta e um terceiro
para classificação das transações como aprovadas ou negadas com seus códigos de
resposta.

Dos três, apenas o primeiro é de entrada. Os outros dois eram de saída, e foram
substituídos por `raw.csv` e `summary.json`. A substituição não é cosmética:

**No `raw.csv`, a latência e o DE 39 da mesma transação ficam na mesma linha,
correlacionáveis pelo STAN.** Mantê-los em arquivos separados perderia
exatamente a correlação necessária para cruzar código de recusa com latência —
que é o cruzamento central da hipótese do trabalho.

A metodologia do artigo será atualizada para refletir isso. Ver a seção 8,
sobre decisões revisadas durante a execução.

### 3.2 Por que a entrada não é normalizada

A massa é um único CSV, uma linha por transação, com todas as colunas. Não há
tabelas separadas de cartões, terminais e transações. Duas razões:

1. **Desempenho.** Normalizar exigiria junção em tempo de execução, pondo
   trabalho extra no caminho crítico do injetor — justamente onde o aparato já
   está no limite por temporização (seção 6.8).
2. **Variedade.** A variedade combinatória se obtém gerando N linhas distintas.
   Não são precisas três tabelas para isso.

Um CSV por perfil de carga — varejo, saque, comércio eletrônico — teria valor
experimental real, porque o mix de transações é uma segunda dimensão legítima.
Fica registrado como trabalho futuro: multiplicaria o número de rodadas, e o
prazo não comporta.

### 3.3 Esquema

```
id,pan,processing_code,amount,mcc,pos_entry_mode,acquirer_id,terminal_id,currency
```

| Coluna | DE | Origem |
|--------|----|--------|
| `id` | — | sequencial; chave de ligação com o `raw.csv` |
| `pan` | 2 | 16 dígitos, válido por Luhn, prefixo 9 |
| `processing_code` | 3 | `000000` (compra) ou `010000` (saque) |
| `amount` | 4 | lognormal truncada, em centavos |
| `mcc` | 18 | 6 valores da ISO 18245 |
| `pos_entry_mode` | 22 | 5 valores: digitado, tarja, chip, aproximação, e-commerce |
| `acquirer_id` | 32 | 5 instituições sintéticas |
| `terminal_id` | 41 | `TERMnnnn`, 500 terminais |
| `currency` | 49 | `986` (real, ISO 4217) |

**STAN, RRN e os campos de data e hora não fazem parte da massa.** São gerados
por requisição no instante do envio: o STAN precisa ser único dentro da rodada
para correlacionar requisição e resposta, e os campos temporais precisam
refletir o instante de chegada pretendido, não o do relógio no envio.

### 3.4 PAN

Os PANs são válidos por Luhn e começam pelo dígito **9**. O ISO/IEC 7812 reserva
o *Major Industry Identifier* 9 para atribuição nacional — faixa não alocada a
nenhum esquema internacional de cartões. Isso garante que nenhum BIN real em uso
seja emitido.

A validade por Luhn é conferida na **carga** de cada rodada, não apenas na
geração, e uma massa que a viole aborta a rodada antes de qualquer medição.

### 3.5 Distribuição do valor

Lognormal, a escolha usual para valor de transação: positiva por construção e
assimétrica à direita, com muitas compras pequenas e poucas grandes.

| Parâmetro | Valor |
|-----------|-------|
| mediana | R$ 50,00 (μ = ln 5000, em centavos) |
| σ | 1,2 |
| truncamento | R$ 1,00 a R$ 10.000,00 |

O truncamento representa os limites práticos de uma autorização de varejo e
**corta a cauda da lognormal** — precisa constar do artigo. Valores fora da
faixa são re-sorteados, e não saturados nos extremos: saturar criaria picos
artificiais no mínimo e no máximo, visíveis na análise como artefato.

Massa gerada com semente 1, 50.000 linhas:

| Estatística | Valor |
|-------------|-------|
| PANs distintos | 50.000 |
| mínimo | R$ 1,00 |
| mediana | R$ 49,83 |
| p95 | R$ 356,94 |
| máximo | R$ 7.009,20 |
| média | R$ 101,65 |

50.000 linhas cobrem uma rodada de 500 TPS por 100 s sem repetir. Rodadas mais
longas reaproveitam a massa na mesma ordem; o reuso é normal e fica declarado.

### 3.6 Geração e consumo: duas sementes distintas

| Semente | Governa | Onde |
|---------|---------|------|
| `cmd/massa -seed` | a geração da massa | executada uma vez; o CSV é versionado |
| `cmd/injector -seed` | a ordem de consumo | por rodada |

O **CSV commitado é a fonte de verdade** dos experimentos; o gerador no
repositório documenta o método. É a prática padrão em pesquisa reproduzível:
publica-se o gerador e o artefato gerado.

Há um motivo técnico forte para não depender apenas da semente. A
reprodutibilidade a partir de semente quebra em silêncio quando o Go muda de
versão: a 1.20 passou a semear as funções globais automaticamente e depreciou
`rand.Seed`, a 1.22 adotou o ChaCha8 como gerador padrão dessas funções, e o
`math/rand/v2` removeu o gerador da Go 1 por inteiro — preservar o fluxo do
`Source` não bastaria, porque mudanças em `Intn` e `Float64` alteram os valores
derivados.

Daí três regras:

1. `rand.New(rand.NewSource(semente))` explícito, **nunca** as funções globais
   do `math/rand`;
2. versão do Go travada no `go.mod`;
3. **CSV commitado**, que torna as duas anteriores irrelevantes para quem
   replicar: pega os mesmos bytes.

### 3.7 A coluna `massa_id` no `raw.csv`

O `raw.csv` ganhou uma coluna `massa_id`, que não constava da seção 5.4 do
CLAUDE.md.

Sem ela, o arquivo bruto traz STAN, latências e DE 39, mas **nenhuma ligação com
a transação que os originou**. Não haveria como cruzar MCC, valor ou forma de
captura com código de recusa ou latência — e a hipótese do trabalho fala em
identificar *padrões* de erro, o que exige exatamente esse cruzamento.

A coluna liga cada linha do `raw.csv` à linha correspondente de
`data/massa.csv`, completando a cadeia:

```
raw.csv.massa_id  ->  massa.csv.id  ->  pan, mcc, amount, pos_entry_mode, ...
raw.csv.stan      ->  correlação requisição/resposta dentro da rodada
```

Exemplo de cruzamento sobre uma rodada de 200 TPS:

| pos_entry_mode | n | aprovação |
|----------------|---|-----------|
| 010 (digitado) | 601 | 83,4% |
| 020 (tarja) | 638 | 83,7% |
| 051 (chip) | 550 | 85,5% |
| 071 (aproximação) | 623 | 84,8% |
| 810 (e-commerce) | 588 | 85,2% |

> **A uniformidade é o resultado esperado contra um alvo sem viés.** O
> autorizador mock decide o DE 39 em função de (semente, STAN) apenas, e nessa
> configuração nenhum padrão por atributo pode emergir.
>
> Isso é o **controle negativo**. Sozinho, ele não sustenta a hipótese: um
> instrumento que não encontra padrão não distingue "não há padrão" de "o
> instrumento não detecta padrão". O controle positivo, com um viés conhecido
> injetado no alvo pela flag `--decline-bias`, está na seção 9.
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

## 6. Calibração do aparato

Injetor e autorizador rodam no mesmo host e disputam CPU. Isso é uma ameaça à
validade real, e o código precisa dar meios de **medi-la, não de escondê-la**.

A calibração é conduzida **antes de qualquer experimento**. Se o injetor satura
em 800 TPS, o experimento de 1000 TPS mede o injetor e não o autorizador, e
descobrir isso depois de rodar tudo é o pior cenário possível.

```sh
go run ./cmd/calibrate -levels 100,250,500,1000,1500,2000,3000,5000 \
  -reps 5 -duration 15s -warmup 5s -conns 32
```

O comando grava `results/calibracao-<timestamp>/calibracao.json` com o
procedimento, os níveis medidos, os limites apurados e o ambiente.

### 6.1 Procedimento

O alvo é o autorizador em `--echo-only`: responde imediatamente, sem latência
artificial e sem sorteio. O que sobra de latência e de atraso é do **aparato** —
injetor, pilha de rede local, escalonador do sistema operacional e coletor de
lixo dos dois processos.

Cada rodada usa **processos novos**, iniciados e encerrados pelo próprio
programa de calibração, para que nada seja herdado da rodada anterior. Os dois
binários são compilados a partir do código corrente no início da varredura:
usar binários deixados por uma compilação anterior abriria a chance de calibrar
uma versão diferente da que será usada nos experimentos.

### 6.2 Critério de saturação

Um nível é considerado **sustentado** quando os consolidados passam nos
critérios:

| Critério | Limiar |
|----------|--------|
| atraso médio de agendamento (mediana entre repetições) | menor que o intervalo entre chegadas |
| vazão alcançada (mediana entre repetições) | ao menos 99% do alvo |
| repetições reprovadas individualmente | não mais que a metade |

O **teto de injeção declarado** é a maior taxa sustentada *antes da primeira
saturação*. Um nível alto que volta a passar depois de um nível saturado é
coincidência, não capacidade, e não eleva o teto.

#### Por que mediana e não unanimidade

A primeira versão deste critério exigia que **todas** as repetições passassem.
O argumento parecia sólido — um teto de aparato deve ser conservador — e estava
errado. A primeira calibração desta máquina produziu:

```
     100 TPS  atraso 766us  vazao 100%  sustentado
     250 TPS  atraso 642us  vazao 100%  SATURADO
     500 TPS  atraso 766us  vazao 100%  sustentado
    2000 TPS  atraso 359us  vazao 100%  sustentado
```

Teto declarado: **100 TPS**, quando o injetor sustentava 2000 TPS com folga. A
causa foi uma única repetição a 250 TPS com atraso médio de 18410 µs contra
642 µs das outras duas — um engasgo transitório da máquina, não incapacidade de
sustentar a taxa. Combinada com a regra de parar na primeira saturação, a
unanimidade deixou o teto refém de um evento externo.

A mediana entre repetições distingue **incapacidade sistemática**, que é o que
o teto deve medir, de **ruído transitório**. As repetições reprovadas
individualmente continuam registradas no `calibracao.json` e aparecem no
relatório, para que uma decisão apertada não passe despercebida.

O caso está coberto por `TestUmaRepeticaoRuimNaoDerrubaONivel` e seu
contraponto `TestMaioriaRuimDerrubaONivel`.

### 6.3 Os pisos vêm do conjunto dos níveis

A primeira versão tirava os pisos do nível mais baixo, supondo que a menor taxa
seria a menos ruidosa. Não é o caso nesta classe de máquina: na primeira
calibração o p99 a 100 TPS foi 9063 µs, contra 771 µs a 1500 TPS.

O relatório passa a apurar:

- **piso de atraso**: o mínimo entre os níveis
- **piso de serviço**: a mediana, entre os níveis, da mediana de ida e volta —
  é a grandeza mais estável da calibração
- **ruído na cauda**: o p99 em **três** valores, mínimo, mediana e máximo

O p99 é reportado como faixa porque a dispersão é o próprio achado: ele não
cresce com a carga e varia por uma ordem de grandeza entre níveis, conforme o
ruído ambiente durante a rodada. Um número único daria a impressão de um piso
bem determinado que não existe.

### 6.4 Isolamento de recursos

O programa de calibração permite fixar `GOMAXPROCS` e `GOGC` de cada processo
separadamente, como exige a seção 7 do CLAUDE.md:

```sh
go run ./cmd/calibrate -gomaxprocs-injector 2 -gomaxprocs-authorizer 2 -gogc 400
```

Os valores são impostos como variáveis de ambiente aos processos filhos e ficam
registrados no `calibracao.json`. Para execuções manuais, as mesmas variáveis
valem diretamente:

```sh
GOMAXPROCS=2 go run ./cmd/authorizer --echo-only
GOMAXPROCS=2 go run ./cmd/injector -tps 500 -duration 30s
```

Em Linux, `taskset` prende cada binário a conjuntos disjuntos de núcleos, o que
é mais forte que limitar `GOMAXPROCS` porque impede também a migração entre
núcleos:

```sh
taskset -c 0,1 ./authorizer --echo-only &
taskset -c 2,3 ./injector -tps 500 -duration 30s
```

O equivalente no Windows é a afinidade de processador, ajustável por
`Start-Process -Affinity` ou pelo Gerenciador de Tarefas. As medições deste
documento foram feitas **sem** fixar afinidade, e essa é uma limitação
declarada: parte da dispersão observada vem da migração de núcleos decidida
pelo escalonador do sistema.

### 6.5 Higiene do ambiente de medição

A dispersão observada é grande o bastante para que processos de fundo importem.
Antes de uma rodada que vá para o artigo:

- encerre sincronizadores de arquivo. Este repositório fica dentro de uma pasta
  do OneDrive; o serviço **não estava em execução** durante as medições
  registradas aqui, mas com ele ativo a varredura deve ser feita a partir de
  uma pasta fora da sincronização;
- evite compilar, indexar ou navegar durante a varredura;
- fixe o plano de energia e **registre qual**. O teste da seção 6.9 mostrou que
  o plano afeta a mediana em cerca de 6% mas **não** explica a cauda; fixá-lo
  remove uma variável, não resolve a dispersão.

Nenhuma dessas medidas elimina o ruído. Elas reduzem a chance de uma repetição
isolada contaminar o resultado — e o critério pela mediana existe justamente
porque a redução nunca é completa.

### 6.6 GOGC

As pausas do coletor de lixo do Go afetam os dois processos e contaminam a
cauda da distribuição de latência — é o fenômeno que Dean e Barroso (2013)
descrevem. O valor efetivo de `GOGC` é registrado no `summary.json` de cada
rodada e no `calibracao.json`, mesmo quando não foi escolhido explicitamente,
porque o padrão do runtime é 100 e isso precisa constar do resultado.

Fixar `GOGC` em um valor alto reduz a frequência das pausas ao custo de mais
memória. A decisão pertence ao desenho de cada experimento, e o que o aparato
garante é que a escolha fique registrada.

### 6.7 Resultado da calibração

Varredura de 22/09/2026, 8 níveis, 5 repetições de 15 s com 5 s de warm-up,
32 conexões, alvo em `--echo-only`. Relatório completo em
[`results/calibracao-20260922T190806/calibracao.json`](../results/calibracao-20260922T190806/calibracao.json).

| Alvo | Intervalo | Atraso médio | Vazão | p50 serviço | p99 serviço | p99 resposta | Situação |
|------|-----------|--------------|-------|-------------|-------------|--------------|----------|
| 100 TPS | 10000 µs | 2180 µs | 100,0% | 357 µs | 34,9 ms | 43,9 ms | sustentado |
| 250 TPS | 4000 µs | 1313 µs | 100,0% | 278 µs | 6,3 ms | 10,1 ms | sustentado |
| 500 TPS | 2000 µs | 1166 µs | 100,0% | 258 µs | 4,5 ms | 10,0 ms | sustentado |
| 1000 TPS | 1000 µs | 1408 µs | 100,0% | 240 µs | 23,7 ms | 94,6 ms | **saturado** |
| 1500 TPS | 666 µs | 1247 µs | 100,0% | 244 µs | 7,4 ms | 23,4 ms | **saturado** |
| 2000 TPS | 500 µs | 1282 µs | 100,0% | 259 µs | 10,1 ms | 105,3 ms | **saturado** |
| 3000 TPS | 333 µs | 2308 µs | 100,0% | 330 µs | 30,1 ms | 201,9 ms | **saturado** |
| 5000 TPS | 200 µs | 3988 µs | 100,0% | 1144 µs | 17,7 ms | 470,5 ms | **saturado** |

Nos níveis saturados, **5 de 5 repetições** reprovaram individualmente: é falha
sistemática, não ruído.

#### Limites declarados do aparato

| Limite | Valor |
|--------|-------|
| **Teto de injeção** | **500 TPS** |
| Primeira taxa saturada | 1000 TPS |
| Piso de atraso de agendamento | 1166 µs |
| Piso de serviço (mediana, ida e volta em loopback) | 278 µs |
| Ruído na cauda, p99 | 4,5 ms mínimo, 17,7 ms mediana, 34,9 ms máximo |

**Nenhum experimento deve ser executado acima de 500 TPS.** Acima disso, o
atraso médio de agendamento supera o intervalo entre chegadas e o resultado
mede o injetor, não o autorizador.

Note que a **vazão permanece em 100% em todos os níveis**, inclusive nos
saturados. O injetor entrega o número correto de requisições e recebe todas as
respostas até 5000 TPS — o que ele não consegue é entregá-las *nos instantes
pretendidos*. Acima do teto, a carga deixa de ser um fluxo uniforme e passa a
chegar em rajadas. Uma calibração que olhasse apenas para a vazão concluiria,
erradamente, que o aparato sustenta 5000 TPS.

### 6.8 De onde vem o teto: o temporizador, não o relógio

O teto é imposto pelo temporizador do sistema operacional, e a distinção em
relação ao relógio de medição (seção 5.8) é essencial: **o `internal/clock`
corrigiu a medição, não o agendamento.**

Desvio do `time.Timer` do Go em relação ao prazo pedido, medido contra o
`QueryPerformanceCounter` (`TestGranularidadeDoTemporizador`):

| Prazo pedido | Desvio mínimo | Desvio p50 | Desvio p90 | Desvio máximo |
|--------------|---------------|------------|------------|---------------|
| 100 µs | −98 µs | 1,35 ms | 14,7 ms | 62,3 ms |
| 500 µs | −465 µs | 593 µs | 14,0 ms | 94,3 ms |
| 1 ms | −886 µs | 523 µs | 4,7 ms | 54,3 ms |
| 2 ms | −342 µs | 263 µs | 4,1 ms | 14,0 ms |
| 10 ms | −807 µs | 3,05 ms | 15,8 ms | 61,9 ms |

O desvio ocorre nos **dois sentidos**: o temporizador dispara até ~900 µs
**antes** do prazo. É o que se espera de um temporizador governado por um
relógio de passo grosseiro — quando ele julga que 1 ms passou, o tempo real
decorrido está em qualquer ponto de uma janela da largura do tique.

A mediana do desvio fica na casa do milissegundo, independentemente do prazo
pedido. Um intervalo entre chegadas menor que isso não é realizável, o que
situa o teto entre 500 e 1000 TPS — exatamente onde a varredura o encontrou.

> **Atenção ao comparar com medições anteriores a esta correção.** Antes do
> `internal/clock`, agenda e medição usavam o mesmo `time.Now` de passo
> grosseiro, e o atraso do temporizador era **invisível para a própria
> medição**: o injetor reportava atraso médio de 500 a 660 µs em qualquer taxa,
> e uma varredura exploratória sugeriu teto próximo de 2000 TPS. Aquele número
> não estava errado por acaso — a medição era cega ao próprio erro. O teto real,
> medido com relógio adequado, é quatro vezes menor.

O atraso de agendamento registrado pelo injetor conta apenas o desvio
**positivo**: uma chegada disparada antes do instante pretendido não entra na
média. A escolha é adequada ao critério de saturação, que pergunta se o injetor
ficou para trás, mas subestima a dispersão total do processo de chegadas. A
caracterização completa do jitter está na tabela acima.

### 6.9 A variação do p99 em carga baixa: hipótese testada e não confirmada

Durante a calibração observou-se que o p99 da latência de serviço **não cresce
com a carga** e chega a piorar em taxas baixas:

| Varredura | 100 TPS | 250–500 TPS | 1500 TPS |
|-----------|---------|-------------|----------|
| 22/09, 3 repetições | 9063 µs | 6331 / 4499 µs | 771 µs |
| 22/09, 5 repetições | 34.879 µs | 6331 / 4499 µs | 7359 µs |

A explicação candidata era **gerenciamento de energia**: em taxa baixa o
processo fica ocioso entre requisições, o núcleo entra em estado de economia e
reduz frequência, e o despertar a partir do ocioso custa caro. Em taxa alta o
processo permanece quente e escalonado. Dean & Barroso (2013) listam
gerenciamento de energia entre as fontes de variabilidade de latência.

A hipótese foi testada em 28/09/2026. **Não se confirmou.**

#### Desenho do teste

Comparar contra a varredura de 22/09 seria inválido: o estado da máquina
derivou entre as sessões, e a mesma configuração produziu 9063 µs numa
varredura e 34.879 µs na outra. O teste foi, portanto, um **A/B pareado na
mesma sessão**: linha de base no plano vigente, troca de plano, nova medição,
tudo em sequência.

A diferença entre os planos está no estado mínimo do processador, na tomada:

| Plano | Estado mínimo do processador |
|-------|------------------------------|
| Equilibrado | **5%** |
| Alto desempenho | **100%** |

Cinco repetições de 15 s a 100 TPS em cada plano, contra `--echo-only`,
idênticas às da calibração.

#### Resultado

| Plano | p99 serviço por repetição (µs) | mediana das repetições |
|-------|-------------------------------|------------------------|
| Equilibrado | 1108, 1145, **1294**, 6011, 7063 | 1294 µs |
| Alto desempenho | 1171, 1416, **1465**, 4053, 9207 | 1465 µs |

**As faixas se sobrepõem quase inteiramente**, e o alto desempenho ficou
nominalmente *pior*. As duas condições são bimodais da mesma forma: três
repetições em torno de 1,2 ms e duas entre 4 e 9 ms. O plano de energia não
explica a cauda.

#### O que o teste mostrou, além da refutação

Dois achados secundários, ambos relevantes.

**1. A mediana melhora, de forma pequena e consistente.**

| Plano | p50 serviço por repetição (µs) |
|-------|-------------------------------|
| Equilibrado | 249, 249, 249, 249, 250 |
| Alto desempenho | 233, 234, 235, 237, 237 |

Todas as cinco repetições do alto desempenho ficam abaixo de todas as cinco do
equilibrado. A separação é perfeita, o que com n = 5 + 5 corresponde a um
p exato bicaudal de 2/252 ≈ **0,008**. A magnitude é de cerca de 14 µs, ou
5,6% da mediana.

O efeito existe e é consistente com o mecanismo — o processador não precisa
subir de frequência a cada requisição — mas é **pequeno demais para explicar
uma variação de p99 de uma ordem de grandeza**.

**2. O fenômeno investigado não reproduziu.**

A linha de base de 28/09 no plano Equilibrado deu p99 de 1294 µs, contra 9063
e 34.879 µs das varreduras de 22/09 na mesma configuração e no mesmo plano. A
pior repetição de 28/09 foi 9207 µs, comparável ao 9063 da primeira varredura;
os 34.879 µs da segunda não foram sequer aproximados.

A conclusão é que **a cauda em carga baixa é estado episódico da máquina, não
função sistemática da taxa nem do plano de energia.** O que muda entre sessões
não foi identificado, e as candidatas — processos de fundo, estado térmico,
atividade de disco — não foram isoladas.

#### Consequências para o artigo

1. A "inversão do p99" descrita antes como achado **não se sustenta como
   fenômeno dependente da carga**. O que se sustenta é a variabilidade
   episódica: o p99 do aparato em carga baixa varia de 1,1 ms a 34,9 ms entre
   sessões, sem que a taxa explique a diferença.
2. Essa variabilidade é, por si, um limite declarado do aparato e mais honesta
   que a explicação anterior: **o p99 de uma rodada isolada não é reprodutível
   entre sessões** e comparações de cauda exigem rodadas pareadas na mesma
   sessão, como o teste acima.
3. O plano de energia passa a ser item do bloco de ambiente por causa do efeito
   na mediana, ainda que pequeno.
4. O desenho pareado usado aqui é o procedimento a adotar sempre que duas
   condições forem comparadas na cauda.

> O resultado negativo fica registrado com o mesmo peso que teria um positivo.
> Uma hipótese com mecanismo plausível, testada e refutada, é informação;
> descartá-la em silêncio e manter a explicação bonita no texto não seria.

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
---

## 8. Decisões revisadas durante a execução

Três decisões de projeto foram revistas **depois** de ver dados. Ajuste
retrospectivo é aceitável em pesquisa de engenharia, mas só se declarado como
tal — do contrário o critério parece ter sido escolhido de antemão, e o leitor
não tem como julgar se ele foi moldado pelo resultado.

Cada revisão está registrada abaixo no formato: critério inicial, o que a
execução revelou, critério revisado, justificativa.

### 8.1 Métrica de validação do aparato: vazão → aderência ao agendamento

**Critério inicial.** A vazão alcançada versus a pretendida seria "a métrica
mais importante" do aparato.

**O que a execução revelou.** Na calibração de 22/09/2026, a vazão ficou em
**100% em todos os oito níveis, de 100 a 5000 TPS** — inclusive naqueles em que
o processo de chegada já havia colapsado. O injetor entrega o número correto de
requisições e recebe todas as respostas mesmo a 5000 TPS; o que ele não
consegue é entregá-las nos instantes pretendidos. Uma calibração guiada pela
vazão concluiria que o aparato sustenta 5000 TPS.

**Critério revisado.** A validação do aparato passa a usar a **aderência ao
agendamento**: a distribuição de (instante real de envio − instante pretendido),
com os dois sinais preservados. A vazão permanece como métrica de saturação do
*sistema sob teste*.

**Justificativa.** As duas métricas respondem perguntas diferentes, e a segunda
só tem sentido se a primeira passar:

| Métrica | Pergunta |
|---------|----------|
| aderência ao agendamento | o injetor aplicou a carga que prometeu? |
| vazão alcançada | o sistema sob teste deu conta da carga aplicada? |

É a armadilha que Jiang & Hassan tratam na fase de execução do teste de carga.

### 8.2 Critério de saturação: unanimidade → mediana entre repetições

**Critério inicial.** Um nível de carga seria considerado sustentado apenas se
**todas** as repetições passassem, sob o argumento de que um teto de aparato
deve ser conservador.

**O que a execução revelou.** A primeira varredura, com 3 repetições, produziu:

```
     100 TPS  atraso 766us  vazao 100%  sustentado
     250 TPS  atraso 642us  vazao 100%  SATURADO
     500 TPS  atraso 766us  vazao 100%  sustentado
    2000 TPS  atraso 359us  vazao 100%  sustentado
```

Teto declarado: **100 TPS**, quando o injetor sustentava 2000 TPS com folga. A
causa foi uma repetição isolada a 250 TPS com atraso médio de **18.410 µs**
contra 642 µs das outras duas. Combinada com a regra de parar na primeira
saturação, a unanimidade deixou o teto refém de um evento externo.

**Critério revisado.** A decisão passa a usar a **mediana entre repetições**, e
o número de repetições subiu de 3 para 5. As repetições reprovadas
individualmente continuam contadas e registradas no `calibracao.json`.

**Justificativa.** A mediana distingue incapacidade sistemática de sustentar a
taxa, que é o que o teto deve medir, de um engasgo transitório da máquina. Com
3 repetições a mediana é decidida por 2 votos, o que é frágil; 5 repetições dão
margem. Kalibera & Jones é a âncora para justificar o número de repetições.

**O outlier não deve ser escondido.** A repetição de 18,4 ms a 250 TPS é
provavelmente o mesmo fenômeno descrito na seção 6.9 — despertar a partir de
estado ocioso em taxa baixa. A mediana a remove da decisão, e ela permanece
visível no relatório e no texto, com a explicação. Uma dispersão de uma ordem
de grandeza é achado, não sujeira a varrer.

### 8.3 Arquivos de dados: três CSVs → um de entrada mais duas saídas

**Decisão inicial.** Os resultados preliminares descreviam três CSVs: massa de
mensagens de entrada, registro de tempo de resposta, e classificação das
transações como aprovadas ou negadas com seus códigos.

**O que a execução revelou.** Apenas o primeiro é de entrada. Separar tempo de
resposta e código de resposta em dois arquivos **perderia a correlação entre
eles**, que é o cruzamento central da hipótese: para relacionar código de recusa
com latência, os dois precisam estar na mesma linha.

**Decisão revisada.** Um CSV de entrada (`data/massa.csv`) e duas saídas por
rodada: `raw.csv`, com uma linha por requisição contendo latências e DE 39
correlacionados por STAN, e `summary.json`, com o resumo consolidado e o bloco
de ambiente.

**Justificativa.** Além da correlação, o `raw.csv` ganhou a coluna `massa_id`,
que liga cada requisição à transação de entrada que a originou. A cadeia
completa — atributos da transação, latência e código de resposta — fica
disponível para a análise sem nenhuma junção em tempo de execução. Ver seção
3.7.
---

## 9. Validação da capacidade de detecção

Um instrumento que não encontra padrão não distingue "não há padrão" de "o
instrumento não detecta padrão". A hipótese do trabalho afirma que o injetor
**permite identificar padrões de erro**; sustentar isso exige exibir o
instrumento detectando um padrão conhecido.

O experimento tem, portanto, dois lados:

| Condição | Alvo | Papel |
|----------|------|-------|
| controle | recusa uniforme | controle **negativo** |
| injetada | `--decline-bias mcc=5967:0.40` | controle **positivo** |

### 9.1 Determinismo e ausência de correlação são exigências distintas

A seção 4 do CLAUDE.md proíbe três coisas no autorizador mock: **lógica de
negócio**, **estado oculto** e **adaptação dinâmica ao volume**. Uma regra
estática e declarada não viola nenhuma delas.

A confusão entre "o mock é determinístico" e "o mock não pode correlacionar
recusa com atributo" foi um erro de leitura da restrição, corrigido antes do
experimento. `--decline-bias` é verdade fundamental injetada deliberadamente no
alvo: a taxa de recusa de um atributo declarado passa a ser o valor informado,
em vez da taxa base. A regra é fixa, conhecida de antemão, não guarda estado
entre requisições e não muda com a carga.

A decisão continua sendo função pura de (semente, STAN, atributos da
requisição). O sorteio vem do STAN; os atributos apenas escolhem **qual limiar**
se aplica.

### 9.2 Desenho

Duas rodadas, idênticas em tudo exceto o viés:

| Parâmetro | Valor |
|-----------|-------|
| taxa | 100 TPS |
| duração | 5 min, com 30 s de warm-up descartado |
| requisições medidas | 27.000 por condição |
| conexões | 16 |
| massa | `data/massa.csv`, semente de consumo 42 |
| latência do alvo | 20 ms, sem jitter |
| taxa de recusa base | 0,15 (`--approval-rate 0.85`) |
| semente do alvo | 42 |

A categoria alvo é o **MCC 5967**, marketing direto e teleserviços de entrada.
A escolha não é arbitrária: é uma categoria de risco mais alto, o que torna a
hipótese de uma taxa de recusa distinta plausível no texto. O MCC foi
acrescentado ao conjunto declarado da massa para que existam transações a
viesar — 3.808 das 27.000 requisições medidas, 14,1%.

### 9.3 Resultados

**Controle negativo** — recusa uniforme:

| MCC | n | recusas | taxa | IC 95% |
|-----|---|---------|------|--------|
| 4111 | 3861 | 551 | 0,1427 | [0,1320, 0,1541] |
| 5411 | 3886 | 566 | 0,1457 | [0,1349, 0,1571] |
| 5541 | 3924 | 599 | 0,1527 | [0,1417, 0,1642] |
| 5812 | 3808 | 540 | 0,1418 | [0,1311, 0,1532] |
| 5912 | 3804 | 556 | 0,1462 | [0,1353, 0,1577] |
| 5967 | 3808 | 594 | 0,1560 | [0,1448, 0,1679] |
| 5999 | 3909 | 578 | 0,1479 | [0,1371, 0,1593] |

χ² = 4,857 com 6 graus de liberdade, **p = 0,562**. Independência **não
rejeitada**: não há evidência de que a recusa dependa do MCC. Todos os
intervalos contêm a taxa base de 0,15.

**Controle positivo** — `mcc=5967:0.40`:

| MCC | n | recusas | taxa | IC 95% |
|-----|---|---------|------|--------|
| 4111 | 3861 | 551 | 0,1427 | [0,1320, 0,1541] |
| 5411 | 3886 | 566 | 0,1457 | [0,1349, 0,1571] |
| 5541 | 3924 | 599 | 0,1527 | [0,1417, 0,1642] |
| 5812 | 3808 | 540 | 0,1418 | [0,1311, 0,1532] |
| 5912 | 3804 | 556 | 0,1462 | [0,1353, 0,1577] |
| **5967** | 3808 | **1558** | **0,4091** | **[0,3936, 0,4248]** |
| 5999 | 3909 | 578 | 0,1479 | [0,1371, 0,1593] |

χ² = 1513,19 com 6 graus de liberdade, **p < 10⁻¹²**. Independência
**rejeitada**.

**A taxa injetada foi recuperada.** O intervalo de confiança de 95% do MCC 5967,
[0,3936, 0,4248], contém o valor injetado de **0,40** e não contém a taxa base
de 0,15.

### 9.4 A comparação é perfeitamente controlada

Os seis MCCs não viesados têm contagem de recusa **byte a byte idêntica** entre
as duas condições:

| MCC | controle | injetada | idêntico |
|-----|----------|----------|----------|
| 4111 | 551 | 551 | sim |
| 5411 | 566 | 566 | sim |
| 5541 | 599 | 599 | sim |
| 5812 | 540 | 540 | sim |
| 5912 | 556 | 556 | sim |
| 5967 | 594 | **1558** | não |
| 5999 | 578 | 578 | sim |

Não é coincidência, e é consequência direta do desenho determinístico descrito
na seção 4.2: a decisão é função pura de (semente, STAN), e o viés altera
apenas o limiar das transações que casam com ele. Nenhuma outra transação muda
de desfecho.

A consequência metodológica é forte: **a única diferença entre as duas
condições é o sinal injetado**. Não há confundimento possível — nem por ordem
de consumo da massa, nem por escalonamento, nem por estado acumulado. Um
experimento com gerador pseudoaleatório compartilhado não teria essa
propriedade, porque o mapeamento entre valores sorteados e requisições
dependeria do escalonador.

### 9.5 Conclusão

O aparato detecta um padrão de erro por atributo quando ele existe, e não o
reporta quando não existe. As duas afirmações são necessárias, e nenhuma delas
sozinha sustentaria a hipótese.

O que o experimento **não** demonstra é capacidade de descobrir padrões
desconhecidos: o atributo testado foi escolhido de antemão. Testar muitos
atributos em busca de significância exigiria correção para comparações
múltiplas, e isso fica como limitação declarada.

### 9.6 Reprodução

```sh
go run ./cmd/authorizer -latency-base 20ms -approval-rate 0.85 -seed 42 \
  -config-out aut.json &
go run ./cmd/injector -tps 100 -duration 5m -warmup 30s -conns 16 -seed 42 \
  -sut-config aut.json -results results/controle-positivo -rep 1

go run ./cmd/authorizer -latency-base 20ms -approval-rate 0.85 -seed 42 \
  -decline-bias "mcc=5967:0.40" -config-out aut.json &
go run ./cmd/injector -tps 100 -duration 5m -warmup 30s -conns 16 -seed 42 \
  -sut-config aut.json -results results/controle-positivo -rep 2

go run ./analysis/qui2 -raw results/controle-positivo/<rodada>/raw.csv -atributo mcc
```

A análise usa apenas a biblioteca padrão. O qui-quadrado e o intervalo de
Wilson estão implementados em `analysis/qui2/estatistica.go` e ancorados em
percentis tabelados por `TestQui2ContraValoresConhecidos` — uma implementação
errada da função gama incompleta produziria p-valores plausíveis e falsos, que
é o pior modo de falha possível para uma análise que vai ao artigo.
