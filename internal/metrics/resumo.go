package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/clock"
)

const (
	// faixa do histograma, em microssegundos: de 1 us a 60 s.
	menorLatenciaUS = 1
	maiorLatenciaUS = 60 * 1000 * 1000

	// tres digitos significativos mantem o erro de quantizacao abaixo de 0,1%
	// em qualquer ponto da faixa, o que e suficiente para reportar ate p99,9.
	digitosSignificativos = 3
)

// Estatisticas resume uma distribuicao de latencia.
//
// Os percentis vem de um HdrHistogram sobre os valores individuais. Nunca sao
// calculados sobre media movel ou amostragem: as duas praticas destroem a
// cauda, que e justamente o que o experimento investiga.
type Estatisticas struct {
	Amostras   int64   `json:"amostras"`
	MediaUS    float64 `json:"media_us"`
	MedianaUS  int64   `json:"mediana_us"`
	P95US      int64   `json:"p95_us"`
	P99US      int64   `json:"p99_us"`
	P999US     int64   `json:"p99_9_us"`
	MinimoUS   int64   `json:"minimo_us"`
	MaximoUS   int64   `json:"maximo_us"`
	DesvioPadr float64 `json:"desvio_padrao_us"`
}

// estatisticas monta o resumo de uma serie de latencias.
func estatisticas(valores []time.Duration) (Estatisticas, error) {
	if len(valores) == 0 {
		return Estatisticas{}, nil
	}

	h := hdrhistogram.New(menorLatenciaUS, maiorLatenciaUS, digitosSignificativos)
	for _, v := range valores {
		us := microssegundos(v)
		if us < menorLatenciaUS {
			us = menorLatenciaUS
		}
		if err := h.RecordValue(us); err != nil {
			return Estatisticas{}, fmt.Errorf("registrando latencia de %d us no histograma: %w", us, err)
		}
	}

	return Estatisticas{
		Amostras:   h.TotalCount(),
		MediaUS:    h.Mean(),
		MedianaUS:  h.ValueAtQuantile(50),
		P95US:      h.ValueAtQuantile(95),
		P99US:      h.ValueAtQuantile(99),
		P999US:     h.ValueAtQuantile(99.9),
		MinimoUS:   h.Min(),
		MaximoUS:   h.Max(),
		DesvioPadr: h.StdDev(),
	}, nil
}

// Desfechos separa resultado de negocio de falha de desempenho.
//
// Uma recusa e resposta valida do autorizador; um timeout e falha de
// desempenho; um erro de transporte e falha de infraestrutura. Somar os tres
// invalidaria a analise, entao cada um tem seu proprio contador.
type Desfechos struct {
	Aprovadas        int64            `json:"aprovadas"`
	Recusadas        int64            `json:"recusadas"`
	ErrosTransporte  int64            `json:"erros_transporte"`
	Timeouts         int64            `json:"timeouts"`
	TaxaAprovacao    float64          `json:"taxa_aprovacao"`
	DistribuicaoDE39 map[string]int64 `json:"distribuicao_de39"`
}

// Vazao compara a taxa pretendida com a alcancada.
//
// E a metrica mais importante da secao 6 do CLAUDE.md: se a alcancada ficar
// abaixo da alvo, aquele nivel de carga mede a saturacao de algum componente, e
// e preciso saber qual.
type Vazao struct {
	AlvoTPS          float64 `json:"alvo_tps"`
	AlcancadoTPS     float64 `json:"alcancado_tps"`
	PercentualDoAlvo float64 `json:"percentual_do_alvo"`
	JanelaSegundos   float64 `json:"janela_s"`
	ChegadasMedidas  int     `json:"chegadas_medidas"`
	Respondidas      int64   `json:"respondidas"`
}

// Agendamento registra o desvio do injetor em relacao ao plano de chegadas.
type Agendamento struct {
	AtrasoMedioUS  int64 `json:"atraso_medio_us"`
	AtrasoMaximoUS int64 `json:"atraso_maximo_us"`

	// InjetorSaturado indica que o atraso medio superou o intervalo entre
	// chegadas. Quando verdadeiro, a rodada mede o injetor e nao o autorizador.
	InjetorSaturado bool `json:"injetor_saturado"`
}

// Rodada descreve os parametros da execucao.
type Rodada struct {
	Inicio             time.Time `json:"inicio"`
	Fim                time.Time `json:"fim"`
	TPSAlvo            float64   `json:"tps_alvo"`
	Duracao            string    `json:"duracao"`
	Warmup             string    `json:"warmup"`
	ChegadasDescartada int       `json:"chegadas_descartadas_warmup"`
	ChegadasTotais     int       `json:"chegadas_totais"`
	Conexoes           int       `json:"conexoes"`
	Repeticao          int       `json:"repeticao"`

	// OrdemEmbaralhada e SementeOrdem descrevem como a ordem de execucao do
	// experimento foi definida.
	//
	// Sem elas, o summary.json de uma rodada isolada nao permite saber se o
	// nivel de carga estava confundido com a posicao na varredura. Ver secao
	// 6.10 de docs/experimento.md.
	OrdemEmbaralhada bool  `json:"ordem_embaralhada"`
	SementeOrdem     int64 `json:"semente_da_ordem"`

	// Sequencia e a posicao desta rodada na ordem de execucao do experimento.
	//
	// Quando a ordem dos niveis de carga e sorteada, o nivel deixa de estar
	// confundido com a posicao na varredura — mas so e possivel verificar isso
	// depois se cada rodada souber onde ficou na sequencia. Zero indica rodada
	// avulsa, fora de uma varredura.
	Sequencia int   `json:"sequencia_na_execucao"`
	Semente   int64 `json:"semente"`

	// MassaArquivo e MassaTransacoes identificam a massa de entrada usada.
	// Sem elas nao ha como afirmar que duas rodadas consumiram os mesmos
	// dados.
	MassaArquivo    string `json:"massa_arquivo"`
	MassaTransacoes int    `json:"massa_transacoes"`
}

// Ambiente registra o contexto de execucao.
//
// Sem este bloco a rodada e inutil para o artigo: nao ha como afirmar que dois
// resultados sao comparaveis, nem reproduzir a medicao.
type Ambiente struct {
	VersaoGo           string            `json:"versao_go"`
	GOMAXPROCS         int               `json:"gomaxprocs"`
	NumCPU             int               `json:"num_cpu"`
	GOOS               string            `json:"goos"`
	GOARCH             string            `json:"goarch"`
	GOGC               string            `json:"gogc"`
	RelogioFonte       string            `json:"relogio_fonte"`
	RelogioResolucaoNS int64             `json:"relogio_resolucao_ns"`
	LinhaDeComando     string            `json:"linha_de_comando"`
	FlagsInjetor       map[string]string `json:"flags_injetor"`

	// ConfigAutorizador e o conteudo do arquivo gravado pelo autorizador com
	// --config-out. Fica aninhado como JSON, e nao como texto, para que a
	// analise possa ler os parametros do sistema sob teste diretamente.
	//
	// A secao 5.4 do CLAUDE.md exige as flags dos dois processos no resultado.
	// Transcrever as do autorizador a mao seria a parte mais fragil da cadeia
	// de auditoria, entao o proprio autorizador as grava.
	ConfigAutorizador json.RawMessage `json:"config_autorizador"`
}

// CapturarAmbiente le o ambiente do processo corrente.
//
// GOGC e lido da variavel de ambiente. Quando nao definida, o valor efetivo e
// o padrao 100 do runtime, e e isso que fica registrado: as pausas do coletor
// de lixo afetam a cauda da latencia, entao o valor precisa constar do
// resultado mesmo quando nao foi escolhido explicitamente.
//
// A fonte e a resolucao do relogio tambem entram no bloco. Uma latencia da
// ordem da resolucao do relogio nao e mensuravel, e o artigo precisa declarar
// esse piso em vez de apresentar numeros abaixo dele.
func CapturarAmbiente(flags map[string]string, configAutorizador json.RawMessage) Ambiente {
	gogc := os.Getenv("GOGC")
	if gogc == "" {
		gogc = "100 (padrao, nao definido no ambiente)"
	}

	if len(configAutorizador) == 0 {
		configAutorizador = json.RawMessage(`"nao informado: execute o autorizador com --config-out"`)
	}

	return Ambiente{
		VersaoGo:           runtime.Version(),
		GOMAXPROCS:         runtime.GOMAXPROCS(0),
		NumCPU:             runtime.NumCPU(),
		GOOS:               runtime.GOOS,
		GOARCH:             runtime.GOARCH,
		GOGC:               gogc,
		RelogioFonte:       clock.Fonte(),
		RelogioResolucaoNS: clock.Resolucao().Nanoseconds(),
		LinhaDeComando:     strings.Join(os.Args, " "),
		FlagsInjetor:       flags,
		ConfigAutorizador:  configAutorizador,
	}
}

// Resumo e o conteudo do summary.json.
type Resumo struct {
	Rodada           Rodada       `json:"rodada"`
	Vazao            Vazao        `json:"vazao"`
	Agendamento      Agendamento  `json:"agendamento"`
	LatenciaServico  Estatisticas `json:"latencia_servico"`
	LatenciaResposta Estatisticas `json:"latencia_resposta"`
	Desfechos        Desfechos    `json:"desfechos"`
	Ambiente         Ambiente     `json:"ambiente"`
}

// Resumir consolida os registros medidos de uma rodada.
//
// A vazao alcancada e calculada sobre a janela de chegadas medidas, e nao sobre
// o tempo decorrido ate a ultima resposta. As N chegadas ocupam os instantes
// 0, 1/TPS, ..., (N-1)/TPS, uma janela que termina um intervalo antes do fim da
// rodada; usar o tempo decorrido produziria vazao acima de 100% do alvo, o que
// e impossivel em modelo aberto.
func (c *Coletor) Resumir(r Rodada, a Agendamento, amb Ambiente) (Resumo, error) {
	medidos := c.Medidos()

	var (
		servico  []time.Duration
		resposta []time.Duration
		d        = Desfechos{DistribuicaoDE39: map[string]int64{}}
	)

	for _, reg := range medidos {
		switch {
		case reg.Timeout:
			d.Timeouts++
		case reg.Erro != "":
			d.ErrosTransporte++
		case reg.Sucesso():
			servico = append(servico, reg.LatenciaServico())
			resposta = append(resposta, reg.LatenciaResposta())
			d.DistribuicaoDE39[reg.DE39]++
			if reg.DE39 == "00" {
				d.Aprovadas++
			} else {
				d.Recusadas++
			}
		default:
			d.ErrosTransporte++
		}
	}

	respondidas := d.Aprovadas + d.Recusadas
	if respondidas > 0 {
		d.TaxaAprovacao = float64(d.Aprovadas) / float64(respondidas)
	}

	estServico, err := estatisticas(servico)
	if err != nil {
		return Resumo{}, fmt.Errorf("latencia de servico: %w", err)
	}
	estResposta, err := estatisticas(resposta)
	if err != nil {
		return Resumo{}, fmt.Errorf("latencia de resposta: %w", err)
	}

	chegadasMedidas := r.ChegadasTotais - r.ChegadasDescartada
	if chegadasMedidas < 0 {
		chegadasMedidas = 0
	}

	v := Vazao{
		AlvoTPS:         r.TPSAlvo,
		ChegadasMedidas: chegadasMedidas,
		Respondidas:     respondidas,
	}
	if r.TPSAlvo > 0 {
		v.JanelaSegundos = float64(chegadasMedidas) / r.TPSAlvo
	}
	if v.JanelaSegundos > 0 {
		v.AlcancadoTPS = float64(respondidas) / v.JanelaSegundos
		v.PercentualDoAlvo = 100 * v.AlcancadoTPS / r.TPSAlvo
	}

	return Resumo{
		Rodada:           r,
		Vazao:            v,
		Agendamento:      a,
		LatenciaServico:  estServico,
		LatenciaResposta: estResposta,
		Desfechos:        d,
		Ambiente:         amb,
	}, nil
}

// EscreverJSON grava o resumo com indentacao, para que o arquivo seja legivel
// e comparavel entre rodadas com um diff.
func (r Resumo) EscreverJSON(w io.Writer) error {
	codificador := json.NewEncoder(w)
	codificador.SetIndent("", "  ")
	if err := codificador.Encode(r); err != nil {
		return fmt.Errorf("codificando summary.json: %w", err)
	}
	return nil
}

// CodigosDE39 devolve os codigos de resposta observados, em ordem, para
// apresentacao estavel.
func (d Desfechos) CodigosDE39() []string {
	codigos := make([]string, 0, len(d.DistribuicaoDE39))
	for c := range d.DistribuicaoDE39 {
		codigos = append(codigos, c)
	}
	sort.Strings(codigos)
	return codigos
}
