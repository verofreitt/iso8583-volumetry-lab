# Procedimento experimental

Documento de registro do aparato. Cada decisão que afeta a interpretação dos
números medidos é registrada aqui, com a justificativa.

Estado atual: **passo 2 da ordem de execução** (caminho ponta a ponta fechado
entre injetor e autorizador). As seções de ambiente, procedimento de execução e
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

**Estado atual:** servidor TCP em `127.0.0.1:8583`, sem flags, sem métricas e
sem concorrência. As conexões são atendidas em série e o DE 39 é sempre `00`.
Cada conexão aceita múltiplos pares `0100`/`0110`.

Ainda não implementado, nos passos seguintes: latência de serviço configurável e
sua distribuição, taxa de aprovação, distribuição dos códigos de recusa, teto de
conexões simultâneas e semente.

---

## 5. Injetor

**Estado atual (passo 2):** envia uma única `0100` por execução e lê a `0110`
correspondente. Sem controle de taxa, sem coleta de latência, sem flags.

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

### 5.2 O que ainda não é medição

O injetor imprime o tempo decorrido da troca, mas **esse número não é um
resultado do experimento**. Ele é medido em modelo fechado, sobre uma única
requisição, e não corrige omissão coordenada. Serve como sinal de vida do
caminho ponta a ponta e nada além disso.

A medição válida depende de dois requisitos ainda não implementados: o modelo
aberto de chegadas (passo 3) e o registro da latência a partir do instante de
chegada pretendido (passo 4).

### 5.3 Limite do teste automatizado

O teste ponta a ponta do injetor sobe um servidor que reproduz o comportamento
do autorizador usando o **mesmo caminho de código** de montagem da resposta —
o binário do autorizador vive em outro `package main` e não pode ser importado.
Um erro comum às duas pontas, portanto, passaria despercebido por ele.

Essa lacuna é coberta pela verificação manual documentada no README, que envia
bytes crus sem depender de nenhum código deste repositório e confere o formato
de fio de forma independente.

---

## 6. Ambiente de execução

*A ser preenchido quando os experimentos forem executados.* O bloco de ambiente
completo — versão do Go, `GOMAXPROCS`, número de CPUs, sistema operacional,
`GOGC`, todas as flags dos dois processos, semente e linha de comando exata —
será gravado em cada `summary.json`.

Versão do Go usada no desenvolvimento até aqui: **go1.25.3 windows/amd64**.
