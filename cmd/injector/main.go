// Command injector e o gerador de carga do experimento.
//
// Este e o passo 4 da ordem de execucao descrita em CLAUDE.md: coleta de
// latencia e escrita de raw.csv e summary.json. As chegadas sao agendadas em
// modelo aberto, cada envio ocorre em sua propria goroutine, e a latencia e
// registrada a partir do instante de chegada pretendido.
//
// Ainda nao implementado: leitura da massa sintetica dos CSVs de entrada
// (internal/massa) e as flags de configuracao do autorizador, que e o passo 5.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	moov "github.com/moov-io/iso8583"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/clock"
	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/massa"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/metrics"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/ratelimit"
)

const (
	// endereco do autorizador. Mesma maquina, via loopback.
	endereco = "127.0.0.1:8583"

	// prazo maximo de uma troca individual. Sem prazo, uma requisicao sem
	// resposta seguraria uma conexao do pool ate o fim da rodada.
	prazo = 5 * time.Second
)

type opcoes struct {
	tps        float64
	duracao    time.Duration
	warmup     time.Duration
	conexoes   int
	repeticao  int
	semente    int64
	resultados string
	sutConfig  string
	massa      string
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	var o opcoes
	flag.Float64Var(&o.tps, "tps", 10, "taxa de chegada pretendida, em transacoes por segundo")
	flag.DurationVar(&o.duracao, "duration", 10*time.Second, "duracao da rodada, incluindo o warm-up")
	flag.DurationVar(&o.warmup, "warmup", 0, "periodo inicial descartado da analise")
	flag.IntVar(&o.conexoes, "conns", 8, "conexoes persistentes mantidas com o autorizador")
	flag.IntVar(&o.repeticao, "rep", 1, "numero da repeticao, usado no nome da pasta de saida")
	flag.Int64Var(&o.semente, "seed", 1, "semente da ordem de consumo da massa")
	flag.StringVar(&o.massa, "massa", filepath.Join("data", "massa.csv"), "CSV da massa sintetica de entrada")
	flag.StringVar(&o.resultados, "results", "results", "raiz onde a pasta da rodada e criada")
	flag.StringVar(&o.sutConfig, "sut-config", "", "arquivo gravado pelo autorizador com --config-out, embutido no summary.json")
	flag.Parse()

	if err := executar(o); err != nil {
		log.Fatalf("%v", err)
	}
}

func executar(o opcoes) error {
	if o.warmup < 0 {
		return fmt.Errorf("warmup nao pode ser negativo, recebido %v", o.warmup)
	}
	if o.warmup >= o.duracao {
		return fmt.Errorf("warmup (%v) deve ser menor que a duracao (%v): nao sobraria nada para medir", o.warmup, o.duracao)
	}

	m, err := massa.Ler(o.massa)
	if err != nil {
		return err
	}
	m.Embaralhar(o.semente)
	log.Printf("massa: %d transacoes de %s, ordem pela semente %d", m.Tamanho(), o.massa, o.semente)

	agendador, err := ratelimit.NovoAberto(o.tps, o.duracao)
	if err != nil {
		return fmt.Errorf("configurando o agendador: %w", err)
	}

	chegadas := agendador.Chegadas()
	descartadas := chegadasDoWarmup(o.warmup, o.tps, chegadas)
	coletor := metrics.NovoColetor(chegadas, descartadas)

	p, err := novoPool(endereco, o.conexoes, prazo)
	if err != nil {
		return fmt.Errorf("abrindo o pool: %w", err)
	}
	defer p.fechar()

	log.Printf("%d conexoes abertas com %s", o.conexoes, endereco)
	log.Printf("alvo de %g TPS por %v (%d chegadas, %d descartadas no warm-up de %v)",
		o.tps, o.duracao, chegadas, descartadas, o.warmup)

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt)
	defer parar()

	res := agendador.Executar(ctx, func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(ctx, p, ch, m))
	})

	resumo, err := coletor.Resumir(
		metrics.Rodada{
			Inicio:             res.Inicio,
			Fim:                res.Fim,
			TPSAlvo:            o.tps,
			Duracao:            o.duracao.String(),
			Warmup:             o.warmup.String(),
			ChegadasDescartada: descartadas,
			ChegadasTotais:     res.Chegadas,
			Conexoes:           o.conexoes,
			Repeticao:          o.repeticao,
			Semente:            o.semente,
			MassaArquivo:       o.massa,
			MassaTransacoes:    m.Tamanho(),
		},
		agendamento(res, o.tps),
		metrics.CapturarAmbiente(flagsInformadas(), configDoAutorizador(o.sutConfig)),
	)
	if err != nil {
		return fmt.Errorf("consolidando a rodada: %w", err)
	}

	pasta, err := pastaDaRodada(o.resultados, res.Inicio, o.tps, o.repeticao)
	if err != nil {
		return err
	}
	if err := escreverResultados(pasta, coletor, resumo); err != nil {
		return err
	}

	relatar(os.Stdout, resumo, p.perdidasTotal())
	fmt.Fprintf(os.Stdout, "\nresultados em %s\n", pasta)
	return nil
}

// chegadasDoWarmup devolve quantas chegadas iniciais pertencem ao warm-up.
//
// A chegada de indice i ocorre no instante i/TPS, entao pertencem ao warm-up
// exatamente as de indice menor que warmup*TPS.
func chegadasDoWarmup(warmup time.Duration, tps float64, chegadas int) int {
	if warmup <= 0 || tps <= 0 {
		return 0
	}
	n := int(math.Ceil(warmup.Seconds() * tps))
	if n > chegadas {
		n = chegadas
	}
	return n
}

// agendamento converte o resultado do agendador e decide se o injetor saturou.
//
// O criterio usa o atraso medio, nao o maximo: um unico sobressalto do
// temporizador do sistema operacional ou uma pausa do coletor de lixo eleva o
// maximo sem que o injetor tenha deixado de sustentar a taxa.
func agendamento(res ratelimit.Resultado, tps float64) metrics.Agendamento {
	intervalo := time.Duration(float64(time.Second) / tps)
	return metrics.Agendamento{
		AtrasoMedioUS:   res.AtrasoMedio.Microseconds(),
		AtrasoMaximoUS:  res.AtrasoMaximo.Microseconds(),
		InjetorSaturado: res.AtrasoMedio > intervalo,
	}
}

// configDoAutorizador le o arquivo gravado pelo autorizador com --config-out.
//
// A leitura falha em silencio de proposito: a ausencia do arquivo e registrada
// no summary.json como tal, e abortar a rodada por causa dela custaria a
// medicao inteira. A analise consegue distinguir uma rodada com a configuracao
// do sistema sob teste de uma sem.
func configDoAutorizador(caminho string) json.RawMessage {
	if caminho == "" {
		return nil
	}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		log.Printf("aviso: nao foi possivel ler %s: %v", caminho, err)
		return nil
	}
	if !json.Valid(dados) {
		log.Printf("aviso: %s nao contem JSON valido", caminho)
		return nil
	}

	return json.RawMessage(dados)
}

// flagsInformadas devolve todas as flags do injetor com seus valores efetivos,
// para o bloco de ambiente do summary.json.
func flagsInformadas() map[string]string {
	valores := map[string]string{}
	flag.VisitAll(func(f *flag.Flag) {
		valores[f.Name] = f.Value.String()
	})
	return valores
}

// requisitar executa uma troca completa e devolve o registro da medicao.
//
// Os instantes sao tomados em volta da troca. O de envio e capturado depois da
// aquisicao da conexao, de modo que a espera pelo pool aparece na latencia de
// resposta, que parte do instante agendado, mas nao na latencia de servico.
// A diferenca entre as duas colunas e justamente o que o artigo discute.
func requisitar(ctx context.Context, p *pool, ch ratelimit.Chegada, m *massa.Massa) metrics.Registro {
	t := m.Em(ch.Indice)
	req := requisicao(ch, t)
	reg := metrics.Registro{
		Indice:   ch.Indice,
		STAN:     req.STAN,
		MassaID:  t.ID,
		Agendado: ch.Agendado,
	}

	conn, err := p.adquirir(ctx)
	if err != nil {
		reg.Envio = clock.Agora()
		reg.Erro = fmt.Sprintf("adquirindo conexao: %v", err)
		return reg
	}

	reg.Envio = clock.Agora()
	resp, err := trocar(conn, req)
	if err != nil {
		reg.Erro = err.Error()
		reg.Timeout = ehTimeout(err)
		// o fluxo pode ter ficado dessincronizado: a conexao nao volta ao pool
		p.descartar(conn)
		return reg
	}
	reg.Resposta = clock.Agora()
	p.devolver(conn)

	de39, err := resp.GetString(39)
	if err != nil {
		reg.Erro = fmt.Sprintf("lendo DE 39: %v", err)
		return reg
	}
	reg.DE39 = de39

	return reg
}

// ehTimeout distingue o esgotamento do prazo das demais falhas de transporte.
// Um timeout e falha de desempenho; um erro de transporte e falha de
// infraestrutura. O summary.json conta os dois separadamente.
func ehTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// requisicao monta a 0100 de uma chegada a partir de uma transacao da massa.
//
// A massa fornece os dados de negocio: PAN, valor, MCC, terminal e os demais
// campos que descrevem a transacao. O que e gerado aqui sao os campos que
// pertencem a requisicao e nao a transacao:
//
//   - STAN (DE 11), derivado do indice da chegada. Precisa ser unico dentro da
//     rodada porque e a chave de correlacao entre requisicao e resposta. O
//     campo tem seis digitos, entao a numeracao reinicia a cada milhao de
//     requisicoes; o limite esta registrado em docs/experimento.md.
//   - RRN (DE 37), tambem derivado do indice, como referencia de rastreamento.
//   - os campos temporais (DE 7, 12 e 13), derivados do instante de chegada
//     pretendido e nao do relogio no momento do envio.
func requisicao(ch ratelimit.Chegada, t massa.Transacao) iso.Requisicao {
	return iso.Requisicao{
		PAN:                   t.PAN,
		ProcessingCode:        t.ProcessingCode,
		Valor:                 t.Valor,
		STAN:                  fmt.Sprintf("%06d", ch.Indice%1000000),
		MCC:                   t.MCC,
		POSEntryMode:          t.POSEntryMode,
		InstituicaoAdquirente: t.Adquirente,
		RRN:                   fmt.Sprintf("%012d", ch.Indice),
		TerminalID:            t.TerminalID,
		Moeda:                 t.Moeda,
		Instante:              ch.Agendado,
	}
}

// trocar envia uma 0100 e devolve a 0110 recebida, conferindo a correlacao
// pelo STAN.
func trocar(conn net.Conn, req iso.Requisicao) (*moov.Message, error) {
	if err := conn.SetDeadline(time.Now().Add(prazo)); err != nil { //nolint:forbidigo // prazo de socket usa o relogio do sistema
		return nil, fmt.Errorf("definindo prazo: %w", err)
	}

	empacotada, err := req.Pack()
	if err != nil {
		return nil, err
	}

	if err := iso.WriteFrame(conn, empacotada); err != nil {
		return nil, err
	}

	bruta, err := iso.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("lendo a resposta: %w", err)
	}

	resp, err := iso.Parse(bruta)
	if err != nil {
		return nil, err
	}

	stan, err := resp.GetString(11)
	if err != nil {
		return nil, fmt.Errorf("lendo DE 11: %w", err)
	}
	if stan != req.STAN {
		return nil, fmt.Errorf("STAN da resposta %q difere do enviado %q", stan, req.STAN)
	}

	return resp, nil
}

// relatar escreve o resumo da rodada em formato legivel. O conteudo
// integral vai para o summary.json.
func relatar(w io.Writer, r metrics.Resumo, perdidas int) {
	fmt.Fprintf(w, "alvo          : %g TPS por %v, %d conexoes\n",
		r.Rodada.TPSAlvo, r.Rodada.Duracao, r.Rodada.Conexoes)
	fmt.Fprintf(w, "chegadas      : %d medidas de %d (warm-up de %v descartou %d)\n",
		r.Vazao.ChegadasMedidas, r.Rodada.ChegadasTotais, r.Rodada.Warmup, r.Rodada.ChegadasDescartada)
	fmt.Fprintf(w, "respondidas   : %d (aprovadas %d, recusadas %d)\n",
		r.Vazao.Respondidas, r.Desfechos.Aprovadas, r.Desfechos.Recusadas)
	fmt.Fprintf(w, "falhas        : %d erros de transporte, %d timeouts\n",
		r.Desfechos.ErrosTransporte, r.Desfechos.Timeouts)
	if perdidas > 0 {
		fmt.Fprintf(w, "conexoes perdidas: %d\n", perdidas)
	}
	fmt.Fprintf(w, "vazao         : %.2f TPS (%.1f%% do alvo)\n",
		r.Vazao.AlcancadoTPS, r.Vazao.PercentualDoAlvo)

	if len(r.Desfechos.DistribuicaoDE39) > 0 {
		fmt.Fprintf(w, "DE 39         :")
		for _, c := range r.Desfechos.CodigosDE39() {
			fmt.Fprintf(w, " %s=%d", c, r.Desfechos.DistribuicaoDE39[c])
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "\n%-16s %10s %10s\n", "latencia (us)", "servico", "resposta")
	linhas := []struct {
		rotulo string
		s, r   any
	}{
		{"media", r.LatenciaServico.MediaUS, r.LatenciaResposta.MediaUS},
		{"mediana", r.LatenciaServico.MedianaUS, r.LatenciaResposta.MedianaUS},
		{"p95", r.LatenciaServico.P95US, r.LatenciaResposta.P95US},
		{"p99", r.LatenciaServico.P99US, r.LatenciaResposta.P99US},
		{"p99.9", r.LatenciaServico.P999US, r.LatenciaResposta.P999US},
		{"maximo", r.LatenciaServico.MaximoUS, r.LatenciaResposta.MaximoUS},
		{"desvio-padrao", r.LatenciaServico.DesvioPadr, r.LatenciaResposta.DesvioPadr},
	}
	for _, l := range linhas {
		fmt.Fprintf(w, "%-16s %10s %10s\n", l.rotulo, numero(l.s), numero(l.r))
	}

	fmt.Fprintf(w, "\natraso de agendamento: medio %d us, maximo %d us\n",
		r.Agendamento.AtrasoMedioUS, r.Agendamento.AtrasoMaximoUS)

	if r.Agendamento.InjetorSaturado {
		fmt.Fprintf(w, "\nAVISO: o atraso medio de agendamento excedeu o intervalo entre chegadas.\n")
		fmt.Fprintf(w, "O injetor nao sustentou a taxa pretendida; esta rodada mede o injetor,\n")
		fmt.Fprintf(w, "nao o autorizador.\n")
	}
}

func numero(v any) string {
	switch n := v.(type) {
	case int64:
		return fmt.Sprintf("%d", n)
	case float64:
		return fmt.Sprintf("%.1f", n)
	default:
		return fmt.Sprintf("%v", v)
	}
}
