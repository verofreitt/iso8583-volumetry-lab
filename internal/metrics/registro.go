// Package metrics coleta a latencia de cada requisicao e consolida o resultado
// de uma rodada.
//
// A coleta nao usa travas. O numero de chegadas de uma rodada e conhecido antes
// do inicio, entao cada requisicao escreve em uma posicao propria de um vetor
// pre-alocado, indexada pelo indice da chegada. Posicoes distintas de um slice
// sao memoria independente, e nao ha corrida.
//
// A alternativa usual, um mutex em volta do histograma no caminho de cada
// requisicao, introduziria contencao exatamente no ponto que o experimento
// pretende medir. O histograma e montado ao final, a partir dos registros
// brutos, que sao tambem o conteudo do raw.csv.
package metrics

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"
)

// Registro e o desfecho de uma requisicao.
type Registro struct {
	// Indice e a ordem da chegada na rodada.
	Indice int

	// STAN e a chave de correlacao entre requisicao e resposta.
	STAN string

	// MassaID e o id da linha da massa que originou a requisicao.
	//
	// Sem ela o raw.csv nao se liga a transacao que o gerou, e nao ha como
	// cruzar atributos como MCC, valor ou forma de captura com o codigo de
	// resposta e a latencia. A hipotese do trabalho fala em identificar
	// padroes de erro, e padrao exige esse cruzamento.
	MassaID string

	// Agendado e o instante de chegada pretendido.
	Agendado time.Time

	// Envio e o instante em que a requisicao de fato partiu.
	Envio time.Time

	// Resposta e o instante em que a resposta foi recebida. Fica zerado
	// quando a requisicao falhou.
	Resposta time.Time

	// DE39 e o codigo de resposta. Fica vazio quando a requisicao falhou.
	DE39 string

	// Erro descreve a falha de transporte, se houve.
	Erro string

	// Timeout distingue o esgotamento do prazo das demais falhas de
	// transporte. Um timeout e falha de desempenho; os dois sao contados
	// separadamente, e ambos separados das recusas de negocio.
	Timeout bool

	// preenchido distingue uma posicao efetivamente usada de uma chegada que
	// nunca ocorreu, no caso de a rodada ser interrompida.
	preenchido bool
}

// Sucesso informa se a requisicao obteve resposta.
func (r Registro) Sucesso() bool {
	return r.Erro == "" && !r.Resposta.IsZero()
}

// LatenciaServico e o tempo entre o envio efetivo e a resposta.
//
// E a latencia que o autorizador exibe. Nao corrige omissao coordenada: se o
// injetor atrasou para enviar, esse atraso nao aparece aqui.
func (r Registro) LatenciaServico() time.Duration {
	return r.Resposta.Sub(r.Envio)
}

// LatenciaResposta e o tempo entre o instante de chegada pretendido e a
// resposta.
//
// E a latencia que o cliente observa, e a unica defensavel para caracterizar o
// sistema. Se o injetor atrasou por contencao interna, o atraso pertence a
// experiencia do cliente e precisa aparecer no numero. Ver secao 5.2 do
// CLAUDE.md e a discussao de omissao coordenada em docs/experimento.md.
func (r Registro) LatenciaResposta() time.Duration {
	return r.Resposta.Sub(r.Agendado)
}

// Coletor acumula os registros de uma rodada.
type Coletor struct {
	registros []Registro

	// descartadas e a quantidade de chegadas iniciais que pertencem ao
	// warm-up e nao entram nem no raw.csv nem no resumo.
	descartadas int
}

// NovoColetor prepara a coleta de uma rodada com o numero de chegadas
// informado, descartando as primeiras.
//
// O warm-up e delimitado por indice de chegada, e nao por relogio: a chegada de
// indice i ocorre no instante i/TPS, entao as chegadas do warm-up sao
// exatamente as de indice menor que warmup*TPS. Delimitar por indice torna o
// recorte identico entre rodadas, sem depender do instante em que o processo
// comecou.
func NovoColetor(chegadas, descartadas int) *Coletor {
	if chegadas < 0 {
		chegadas = 0
	}
	if descartadas < 0 {
		descartadas = 0
	}
	if descartadas > chegadas {
		descartadas = chegadas
	}
	return &Coletor{
		registros:   make([]Registro, chegadas),
		descartadas: descartadas,
	}
}

// ChegadasDescartadas devolve quantas chegadas iniciais pertencem ao warm-up.
func (c *Coletor) ChegadasDescartadas() int {
	return c.descartadas
}

// Registrar grava o desfecho de uma requisicao.
//
// Cada indice e escrito por uma unica goroutine, entao nao ha sincronizacao.
// Um indice fora da faixa e ignorado em silencio apenas se o coletor estiver
// vazio; caso contrario e um erro de programacao e o metodo entra em panico,
// porque gravar no lugar errado corromperia a medicao.
func (c *Coletor) Registrar(r Registro) {
	if len(c.registros) == 0 {
		return
	}
	if r.Indice < 0 || r.Indice >= len(c.registros) {
		panic(fmt.Sprintf("metrics: indice %d fora da faixa [0,%d)", r.Indice, len(c.registros)))
	}
	r.preenchido = true
	c.registros[r.Indice] = r
}

// Medidos devolve os registros que entram na analise: preenchidos e fora do
// warm-up.
func (c *Coletor) Medidos() []Registro {
	if c.descartadas >= len(c.registros) {
		return nil
	}

	medidos := make([]Registro, 0, len(c.registros)-c.descartadas)
	for _, r := range c.registros[c.descartadas:] {
		if r.preenchido {
			medidos = append(medidos, r)
		}
	}
	return medidos
}

// colunas e o cabecalho do raw.csv, na ordem definida na secao 5.4 do
// CLAUDE.md.
var colunas = []string{
	"stan",
	"massa_id",
	"ts_agendado",
	"ts_envio",
	"ts_resposta",
	"latencia_servico_us",
	"latencia_resposta_us",
	"de39",
	"erro_transporte",
}

// EscreverCSV grava uma linha por requisicao medida.
//
// As chegadas do warm-up nao sao escritas: o warm-up e descartado, e mante-lo
// no arquivo bruto convidaria a inclui-lo na analise por engano.
//
// A coluna massa_id liga cada linha a transacao de entrada que a originou, em
// data/massa.csv. E ela que permite cruzar atributos da transacao com codigo
// de resposta e latencia. A secao 5.4 do CLAUDE.md nao a previa; o acrescimo
// esta justificado em docs/experimento.md.
//
// Os instantes usam RFC 3339 com nanossegundos. As duas latencias ocupam
// colunas separadas, para que a analise possa comparar a latencia de servico
// com a latencia corrigida de omissao coordenada e o artigo possa discutir a
// diferenca. Linhas com falha de transporte tem as duas latencias em branco:
// zero seria indistinguivel de uma resposta instantanea.
func (c *Coletor) EscreverCSV(w io.Writer) error {
	escritor := csv.NewWriter(w)

	if err := escritor.Write(colunas); err != nil {
		return fmt.Errorf("escrevendo cabecalho: %w", err)
	}

	for _, r := range c.Medidos() {
		linha := []string{
			r.STAN,
			r.MassaID,
			r.Agendado.Format(time.RFC3339Nano),
			r.Envio.Format(time.RFC3339Nano),
			"", // ts_resposta
			"", // latencia_servico_us
			"", // latencia_resposta_us
			r.DE39,
			r.Erro,
		}

		if r.Sucesso() {
			linha[4] = r.Resposta.Format(time.RFC3339Nano)
			linha[5] = strconv.FormatInt(microssegundos(r.LatenciaServico()), 10)
			linha[6] = strconv.FormatInt(microssegundos(r.LatenciaResposta()), 10)
		}

		if err := escritor.Write(linha); err != nil {
			return fmt.Errorf("escrevendo registro do STAN %s: %w", r.STAN, err)
		}
	}

	escritor.Flush()
	return escritor.Error()
}

// microssegundos converte uma duracao para microssegundos, arredondando.
//
// O arredondamento e deliberado: truncar levaria toda latencia submicrossegundo
// a zero, e um zero no arquivo bruto seria lido como medicao instantanea.
func microssegundos(d time.Duration) int64 {
	return int64(math.Round(float64(d) / float64(time.Microsecond)))
}
