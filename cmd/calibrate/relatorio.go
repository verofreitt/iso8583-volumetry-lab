package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/clock"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/metrics"
)

// Procedimento registra como a calibracao foi conduzida.
type Procedimento struct {
	Niveis      []float64 `json:"niveis_tps"`
	Repeticoes  int       `json:"repeticoes"`
	Duracao     string    `json:"duracao"`
	Warmup      string    `json:"warmup"`
	Conexoes    int       `json:"conexoes"`
	LimiarVazao float64   `json:"limiar_vazao_percentual"`
	ModoAlvo    string    `json:"modo_do_alvo"`
	Inicio      time.Time `json:"inicio"`
	Fim         time.Time `json:"fim"`
}

// Repeticao e o extrato de uma rodada.
type Repeticao struct {
	AtrasoMedioUS     int64   `json:"atraso_medio_us"`
	AtrasoMaximoUS    int64   `json:"atraso_maximo_us"`
	PercentualDoAlvo  float64 `json:"percentual_do_alvo"`
	InjetorSaturado   bool    `json:"injetor_saturado"`
	MedianaServicoUS  int64   `json:"mediana_servico_us"`
	P99ServicoUS      int64   `json:"p99_servico_us"`
	P999ServicoUS     int64   `json:"p99_9_servico_us"`
	MedianaRespostaUS int64   `json:"mediana_resposta_us"`
	P99RespostaUS     int64   `json:"p99_resposta_us"`
	ErrosTransporte   int64   `json:"erros_transporte"`
	Timeouts          int64   `json:"timeouts"`
}

func resumir(r metrics.Resumo) Repeticao {
	return Repeticao{
		AtrasoMedioUS:     r.Agendamento.AtrasoMedioUS,
		AtrasoMaximoUS:    r.Agendamento.AtrasoMaximoUS,
		PercentualDoAlvo:  r.Vazao.PercentualDoAlvo,
		InjetorSaturado:   r.Agendamento.InjetorSaturado,
		MedianaServicoUS:  r.LatenciaServico.MedianaUS,
		P99ServicoUS:      r.LatenciaServico.P99US,
		P999ServicoUS:     r.LatenciaServico.P999US,
		MedianaRespostaUS: r.LatenciaResposta.MedianaUS,
		P99RespostaUS:     r.LatenciaResposta.P99US,
		ErrosTransporte:   r.Desfechos.ErrosTransporte,
		Timeouts:          r.Desfechos.Timeouts,
	}
}

// Nivel consolida as repeticoes de uma taxa.
type Nivel struct {
	TPS         float64     `json:"tps"`
	Repeticoes  []Repeticao `json:"repeticoes"`
	IntervaloUS int64       `json:"intervalo_entre_chegadas_us"`

	// os consolidados usam a mediana entre repeticoes, que resiste melhor a
	// uma repeticao contaminada por ruido externo que a media
	AtrasoMedioUS     int64   `json:"atraso_medio_us"`
	AtrasoMaximoUS    int64   `json:"atraso_maximo_us"`
	PercentualDoAlvo  float64 `json:"percentual_do_alvo"`
	MedianaServicoUS  int64   `json:"mediana_servico_us"`
	P99ServicoUS      int64   `json:"p99_servico_us"`
	P999ServicoUS     int64   `json:"p99_9_servico_us"`
	MedianaRespostaUS int64   `json:"mediana_resposta_us"`
	P99RespostaUS     int64   `json:"p99_resposta_us"`
	Falhas            int64   `json:"falhas"`

	// RepeticoesFalhas conta quantas repeticoes reprovaram individualmente.
	// Fica visivel para que uma decisao apertada nao passe despercebida.
	RepeticoesFalhas int `json:"repeticoes_com_falha"`

	// Sustentado decide pela MEDIANA entre repeticoes, nao por unanimidade.
	//
	// Exigir unanimidade parece conservador, mas deixa o teto refem de uma
	// unica repeticao contaminada por ruido externo. Na primeira calibracao
	// desta maquina, uma repeticao isolada a 250 TPS registrou atraso medio de
	// 18410 us contra 642 us das outras duas, e o teto declarado despencou de
	// 2000 para 100 TPS. O numero resultante nao descrevia o aparato.
	//
	// A mediana distingue incapacidade sistematica de sustentar a taxa, que e
	// o que o teto deve medir, de um engasgo transitorio da maquina. O numero
	// de repeticoes reprovadas continua registrado.
	Sustentado bool `json:"sustentado"`

	MotivoSaturacao string `json:"motivo_saturacao,omitempty"`
}

func (n *Nivel) consolidar(limiarVazao float64) {
	n.IntervaloUS = int64(1e6 / n.TPS)

	n.AtrasoMedioUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.AtrasoMedioUS }))
	n.AtrasoMaximoUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.AtrasoMaximoUS }))
	n.MedianaServicoUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.MedianaServicoUS }))
	n.P99ServicoUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.P99ServicoUS }))
	n.P999ServicoUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.P999ServicoUS }))
	n.MedianaRespostaUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.MedianaRespostaUS }))
	n.P99RespostaUS = medianaInt(colunaInt(n.Repeticoes, func(r Repeticao) int64 { return r.P99RespostaUS }))
	n.PercentualDoAlvo = medianaFloat(colunaFloat(n.Repeticoes, func(r Repeticao) float64 { return r.PercentualDoAlvo }))

	var reprovadas []string
	for i, r := range n.Repeticoes {
		n.Falhas += r.ErrosTransporte + r.Timeouts

		var razoes []string
		if r.InjetorSaturado {
			razoes = append(razoes, fmt.Sprintf("atraso medio de %d us", r.AtrasoMedioUS))
		}
		if r.PercentualDoAlvo < limiarVazao {
			razoes = append(razoes, fmt.Sprintf("vazao de %.1f%%", r.PercentualDoAlvo))
		}
		if r.ErrosTransporte+r.Timeouts > 0 {
			razoes = append(razoes, fmt.Sprintf("%d falhas de transporte", r.ErrosTransporte+r.Timeouts))
		}
		if len(razoes) > 0 {
			n.RepeticoesFalhas++
			reprovadas = append(reprovadas, fmt.Sprintf("repeticao %d (%s)", i+1, strings.Join(razoes, ", ")))
		}
	}

	// a decisao usa os consolidados por mediana, ja calculados acima
	var motivos []string
	if n.AtrasoMedioUS > n.IntervaloUS {
		motivos = append(motivos, fmt.Sprintf("atraso medio mediano de %d us excede o intervalo de %d us",
			n.AtrasoMedioUS, n.IntervaloUS))
	}
	if n.PercentualDoAlvo < limiarVazao {
		motivos = append(motivos, fmt.Sprintf("vazao mediana de %.1f%% abaixo do limiar de %.1f%%",
			n.PercentualDoAlvo, limiarVazao))
	}
	if n.RepeticoesFalhas > len(n.Repeticoes)/2 {
		motivos = append(motivos, fmt.Sprintf("%d de %d repeticoes reprovaram individualmente",
			n.RepeticoesFalhas, len(n.Repeticoes)))
	}

	n.Sustentado = len(motivos) == 0
	if len(motivos) > 0 {
		n.MotivoSaturacao = strings.Join(motivos, "; ")
	} else if len(reprovadas) > 0 {
		// sustentado apesar de repeticoes isoladas: registra para inspecao
		n.MotivoSaturacao = "sustentado pela mediana, com " + strings.Join(reprovadas, "; ")
	}
}

// Ambiente do processo que conduziu a calibracao.
type Ambiente struct {
	VersaoGo              string `json:"versao_go"`
	NumCPU                int    `json:"num_cpu"`
	GOOS                  string `json:"goos"`
	GOARCH                string `json:"goarch"`
	GOGCImposto           string `json:"gogc_imposto"`
	GOMAXPROCSInjetor     int    `json:"gomaxprocs_injetor"`
	GOMAXPROCSAutorizador int    `json:"gomaxprocs_autorizador"`
	RelogioFonte          string `json:"relogio_fonte"`
	RelogioResolucaoNS    int64  `json:"relogio_resolucao_ns"`
	LinhaDeComando        string `json:"linha_de_comando"`
}

func ambiente(o opcoes) Ambiente {
	gogc := o.gogc
	if gogc == "" {
		gogc = "nao imposto (herda o ambiente)"
	}
	return Ambiente{
		VersaoGo:              runtime.Version(),
		NumCPU:                runtime.NumCPU(),
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		GOGCImposto:           gogc,
		GOMAXPROCSInjetor:     o.gomaxprocsInjetor,
		GOMAXPROCSAutorizador: o.gomaxprocsAutorizador,
		RelogioFonte:          clock.Fonte(),
		RelogioResolucaoNS:    clock.Resolucao().Nanoseconds(),
		LinhaDeComando:        strings.Join(os.Args, " "),
	}
}

// Limites sao os numeros que vao para o artigo como limites declarados do
// aparato.
type Limites struct {
	// TetoInjecaoTPS e a maior taxa sustentada por todas as repeticoes.
	// Acima dela, o resultado mede o injetor e nao o autorizador.
	TetoInjecaoTPS float64 `json:"teto_injecao_tps"`

	// PrimeiraTaxaSaturadaTPS e a menor taxa em que alguma repeticao falhou.
	// O teto real esta entre as duas.
	PrimeiraTaxaSaturadaTPS float64 `json:"primeira_taxa_saturada_tps,omitempty"`

	// PisoAtrasoUS e o menor atraso de agendamento observado entre os niveis.
	// Nao diminui em taxas baixas: e a granularidade do temporizador do
	// sistema operacional, nao contencao do injetor.
	PisoAtrasoUS int64 `json:"piso_atraso_agendamento_us"`

	// PisoServicoMedianaUS e a mediana, entre os niveis, do tempo de ida e
	// volta em loopback. E a grandeza mais estavel da calibracao.
	PisoServicoMedianaUS int64 `json:"piso_servico_mediana_us"`

	// O p99 contra um alvo que responde imediatamente e reportado em tres
	// valores, e nao em um.
	//
	// A dispersao e o proprio achado: o p99 nao cresce com a carga e varia por
	// uma ordem de grandeza entre niveis, conforme o ruido ambiente da maquina
	// durante a rodada. Um numero unico esconderia isso e daria a impressao de
	// um piso bem determinado.
	RuidoP99MinimoUS  int64 `json:"ruido_p99_minimo_us"`
	RuidoP99MedianaUS int64 `json:"ruido_p99_mediana_us"`
	RuidoP99MaximoUS  int64 `json:"ruido_p99_maximo_us"`

	Observacoes []string `json:"observacoes"`
}

// Relatorio e o conteudo do calibracao.json.
type Relatorio struct {
	Procedimento Procedimento `json:"procedimento"`
	Limites      Limites      `json:"limites"`
	Niveis       []Nivel      `json:"niveis"`
	Ambiente     Ambiente     `json:"ambiente"`
}

func (r *Relatorio) concluir() {
	if len(r.Niveis) == 0 {
		return
	}

	// o teto e a maior taxa sustentada que nao tem nenhuma taxa saturada
	// abaixo dela: uma taxa alta que volta a passar depois de uma falha seria
	// coincidencia, nao capacidade
	for _, n := range r.Niveis {
		if !n.Sustentado {
			if r.Limites.PrimeiraTaxaSaturadaTPS == 0 {
				r.Limites.PrimeiraTaxaSaturadaTPS = n.TPS
			}
			continue
		}
		if r.Limites.PrimeiraTaxaSaturadaTPS == 0 {
			r.Limites.TetoInjecaoTPS = n.TPS
		}
	}

	// os pisos vem do conjunto dos niveis, e nao do mais baixo: nesta classe de
	// maquina o nivel menos carregado nao e o menos ruidoso
	var atrasos, p99s, medianas []int64
	for _, n := range r.Niveis {
		atrasos = append(atrasos, n.AtrasoMedioUS)
		p99s = append(p99s, n.P99ServicoUS)
		medianas = append(medianas, n.MedianaServicoUS)
	}
	sort.Slice(p99s, func(i, j int) bool { return p99s[i] < p99s[j] })

	r.Limites.PisoAtrasoUS = minimoInt(atrasos)
	r.Limites.PisoServicoMedianaUS = medianaInt(medianas)
	r.Limites.RuidoP99MinimoUS = p99s[0]
	r.Limites.RuidoP99MedianaUS = p99s[len(p99s)/2]
	r.Limites.RuidoP99MaximoUS = p99s[len(p99s)-1]

	if r.Limites.TetoInjecaoTPS == 0 {
		r.Limites.Observacoes = append(r.Limites.Observacoes,
			"nenhum nivel foi sustentado: a faixa varrida comeca acima do teto do aparato")
	}
	if r.Limites.PrimeiraTaxaSaturadaTPS == 0 {
		r.Limites.Observacoes = append(r.Limites.Observacoes,
			"nenhum nivel saturou: o teto esta acima da faixa varrida e nao foi determinado")
	}
	r.Limites.Observacoes = append(r.Limites.Observacoes,
		"o piso de atraso e a granularidade do temporizador do sistema operacional, nao contencao do injetor: ele nao diminui em taxas baixas",
		"o p99 foi medido contra --echo-only e nao cresce com a carga; a variacao entre niveis e ruido ambiente da maquina, nao enfileiramento",
		"uma latencia de servico configurada abaixo do p99 maximo nao e distinguivel do aparato na cauda")
}

func (r Relatorio) imprimir(w io.Writer) {
	fmt.Fprintf(w, "\n=== calibracao do aparato ===\n\n")
	fmt.Fprintf(w, "alvo em %s, %d repeticoes de %v por nivel, %d conexoes\n\n",
		r.Procedimento.ModoAlvo, r.Procedimento.Repeticoes, r.Procedimento.Duracao, r.Procedimento.Conexoes)

	fmt.Fprintf(w, "%8s %10s %10s %8s %10s %10s %10s  %s\n",
		"TPS", "intervalo", "atraso", "vazao", "p50 serv", "p99 serv", "p99 resp", "situacao")
	fmt.Fprintf(w, "%8s %10s %10s %8s %10s %10s %10s  %s\n",
		"", "(us)", "medio(us)", "(%)", "(us)", "(us)", "(us)", "")

	for _, n := range r.Niveis {
		fmt.Fprintf(w, "%8g %10d %10d %8.1f %10d %10d %10d  %s\n",
			n.TPS, n.IntervaloUS, n.AtrasoMedioUS, n.PercentualDoAlvo,
			n.MedianaServicoUS, n.P99ServicoUS, n.P99RespostaUS, situacao(n.Sustentado))
	}

	fmt.Fprintf(w, "\n--- limites declarados do aparato ---\n\n")
	if r.Limites.TetoInjecaoTPS > 0 {
		fmt.Fprintf(w, "teto de injecao          : %g TPS\n", r.Limites.TetoInjecaoTPS)
	} else {
		fmt.Fprintf(w, "teto de injecao          : nao determinado\n")
	}
	if r.Limites.PrimeiraTaxaSaturadaTPS > 0 {
		fmt.Fprintf(w, "primeira taxa saturada   : %g TPS\n", r.Limites.PrimeiraTaxaSaturadaTPS)
	}
	fmt.Fprintf(w, "piso de atraso           : %d us (granularidade do temporizador)\n", r.Limites.PisoAtrasoUS)
	fmt.Fprintf(w, "piso de servico, mediana : %d us (ida e volta em loopback)\n", r.Limites.PisoServicoMedianaUS)
	fmt.Fprintf(w, "ruido na cauda, p99      : %d us minimo, %d us mediana, %d us maximo\n",
		r.Limites.RuidoP99MinimoUS, r.Limites.RuidoP99MedianaUS, r.Limites.RuidoP99MaximoUS)

	fmt.Fprintf(w, "\n")
	for _, o := range r.Limites.Observacoes {
		fmt.Fprintf(w, "- %s\n", o)
	}

	for _, n := range r.Niveis {
		if n.MotivoSaturacao != "" {
			fmt.Fprintf(w, "\n%g TPS saturou: %s\n", n.TPS, n.MotivoSaturacao)
		}
	}
}

// --- auxiliares ---

func colunaInt(reps []Repeticao, f func(Repeticao) int64) []int64 {
	v := make([]int64, 0, len(reps))
	for _, r := range reps {
		v = append(v, f(r))
	}
	return v
}

func colunaFloat(reps []Repeticao, f func(Repeticao) float64) []float64 {
	v := make([]float64, 0, len(reps))
	for _, r := range reps {
		v = append(v, f(r))
	}
	return v
}

func minimoInt(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func medianaInt(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[len(c)/2]
}

func medianaFloat(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}
