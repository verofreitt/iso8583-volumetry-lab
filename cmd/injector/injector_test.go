package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/metrics"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/ratelimit"
)

// autorizadorDeTeste sobe um servidor que reproduz o comportamento do
// cmd/authorizer, atendendo conexoes concorrentes.
//
// O binario do autorizador vive em outro package main e nao pode ser importado
// aqui, entao este servidor usa o mesmo caminho de codigo de montagem da
// resposta. A verificacao entre os dois binarios reais e manual e esta
// documentada no README.
func autorizadorDeTeste(t *testing.T, de39 string, servico time.Duration) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go atenderConexao(conn, de39, servico)
		}
	}()

	return ln.Addr().String()
}

func atenderConexao(conn net.Conn, de39 string, servico time.Duration) {
	defer conn.Close()

	for {
		bruta, err := iso.ReadFrame(conn)
		if err != nil {
			return
		}
		req, err := iso.Parse(bruta)
		if err != nil {
			return
		}
		if servico > 0 {
			time.Sleep(servico)
		}
		resp, err := iso.BuildResponse(req, de39)
		if err != nil {
			return
		}
		empacotada, err := resp.Pack()
		if err != nil {
			return
		}
		if err := iso.WriteFrame(conn, empacotada); err != nil {
			return
		}
	}
}

// --- construcao da requisicao ---

func TestRequisicaoSTANDerivaDoIndice(t *testing.T) {
	casos := map[int]string{
		0:       "000000",
		1:       "000001",
		42:      "000042",
		999999:  "999999",
		1000000: "000000", // a numeracao reinicia: DE 11 tem seis digitos
		1000001: "000001",
	}
	for indice, esperado := range casos {
		req := requisicao(ratelimit.Chegada{Indice: indice, Agendado: time.Now()})
		if req.STAN != esperado {
			t.Errorf("indice %d: STAN = %q, esperado %q", indice, req.STAN, esperado)
		}
	}
}

// TestRequisicaoUsaInstanteAgendado confirma que os campos temporais da
// mensagem derivam do instante de chegada pretendido, e nao do relogio no
// momento do envio.
func TestRequisicaoUsaInstanteAgendado(t *testing.T) {
	agendado := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	req := requisicao(ratelimit.Chegada{Indice: 7, Agendado: agendado})

	if !req.Instante.Equal(agendado) {
		t.Errorf("Instante = %v, esperado %v", req.Instante, agendado)
	}
}

func TestRequisicaoEhValida(t *testing.T) {
	empacotada, err := requisicao(ratelimit.Chegada{Indice: 1, Agendado: time.Now()}).Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(empacotada) != 120 {
		t.Errorf("0100 com %d bytes, esperado 120", len(empacotada))
	}
	if !bytes.HasPrefix(empacotada, []byte("0100")) {
		t.Errorf("MTI inesperado: %s", empacotada[:4])
	}
}

// --- pool de conexoes ---

func TestPoolAbreConexoesAntecipadamente(t *testing.T) {
	endereco := autorizadorDeTeste(t, "00", 0)

	p, err := novoPool(endereco, 4, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	if got := len(p.livres); got != 4 {
		t.Errorf("%d conexoes livres, esperado 4", got)
	}
}

func TestPoolRejeitaTamanhoInvalido(t *testing.T) {
	if _, err := novoPool("127.0.0.1:1", 0, time.Second); err == nil {
		t.Error("esperado erro para pool de tamanho zero, obtido nil")
	}
}

func TestPoolFalhaSemServidor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	endereco := ln.Addr().String()
	ln.Close()

	if _, err := novoPool(endereco, 2, 200*time.Millisecond); err == nil {
		t.Error("esperado erro ao abrir pool sem autorizador, obtido nil")
	}
}

// TestPoolAdquirirBloqueiaQuandoOcupado confirma que o pool limita o
// paralelismo, e que a espera respeita o cancelamento do contexto.
func TestPoolAdquirirBloqueiaQuandoOcupado(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "00", 0), 1, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	conn, err := p.adquirir(context.Background())
	if err != nil {
		t.Fatalf("adquirir: %v", err)
	}

	ctx, cancelar := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelar()

	if _, err := p.adquirir(ctx); err == nil {
		t.Error("esperado erro ao esgotar o pool, obtido nil")
	}

	p.devolver(conn)
	if _, err := p.adquirir(context.Background()); err != nil {
		t.Errorf("adquirir apos devolver: %v", err)
	}
}

// TestPoolDescartarSubstituiConexao verifica que uma conexao defeituosa nao
// volta ao pool e que o pool nao encolhe.
//
// Devolver uma conexao que falhou no meio de uma troca deixaria o fluxo
// dessincronizado, e a proxima requisicao a usa-la leria a resposta errada:
// a correlacao por STAN quebraria em silencio.
func TestPoolDescartarSubstituiConexao(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "00", 0), 2, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	conn, err := p.adquirir(context.Background())
	if err != nil {
		t.Fatalf("adquirir: %v", err)
	}
	p.descartar(conn)

	if got := p.perdidasTotal(); got != 0 {
		t.Errorf("perdidas = %d, esperado 0: a conexao deveria ter sido substituida", got)
	}

	// as duas conexoes continuam utilizaveis
	for i := 0; i < 2; i++ {
		c, err := p.adquirir(context.Background())
		if err != nil {
			t.Fatalf("adquirir %d: %v", i, err)
		}
		if _, err := trocar(c, requisicao(ratelimit.Chegada{Indice: i, Agendado: time.Now()})); err != nil {
			t.Errorf("troca %d apos descarte: %v", i, err)
		}
		p.devolver(c)
	}
}

func TestPoolDevolverAposFechar(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "00", 0), 1, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}

	conn, err := p.adquirir(context.Background())
	if err != nil {
		t.Fatalf("adquirir: %v", err)
	}

	p.fechar()
	p.devolver(conn) // nao deve entrar em panico
	p.fechar()       // idempotente
}

// --- troca ---

func TestTrocarPontaAPonta(t *testing.T) {
	conn, err := net.Dial("tcp", autorizadorDeTeste(t, "00", 0))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	req := requisicao(ratelimit.Chegada{Indice: 3, Agendado: time.Now()})
	resp, err := trocar(conn, req)
	if err != nil {
		t.Fatalf("trocar: %v", err)
	}

	mti, err := resp.GetMTI()
	if err != nil {
		t.Fatalf("GetMTI: %v", err)
	}
	if mti != iso.MTIAuthResponse {
		t.Errorf("MTI = %q, esperado %q", mti, iso.MTIAuthResponse)
	}
}

func TestTrocarFalhaSemResposta(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	aceita, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	aceita.Close()

	if _, err := trocar(conn, requisicao(ratelimit.Chegada{Indice: 0, Agendado: time.Now()})); err == nil {
		t.Error("esperado erro quando o autorizador fecha sem responder, obtido nil")
	}
}

// --- warm-up ---

func TestChegadasDoWarmup(t *testing.T) {
	casos := []struct {
		warmup   time.Duration
		tps      float64
		chegadas int
		esperado int
	}{
		{0, 10, 100, 0},
		{2 * time.Second, 10, 100, 20},
		{time.Second, 2.5, 25, 3},   // 2,5 chegadas no warm-up: arredonda para cima
		{time.Minute, 10, 100, 100}, // warm-up maior que a rodada
	}
	for _, c := range casos {
		if got := chegadasDoWarmup(c.warmup, c.tps, c.chegadas); got != c.esperado {
			t.Errorf("chegadasDoWarmup(%v, %v, %d) = %d, esperado %d",
				c.warmup, c.tps, c.chegadas, got, c.esperado)
		}
	}
}

// TestExecutarRejeitaWarmupInvalido protege contra a rodada que nao mede nada.
func TestExecutarRejeitaWarmupInvalido(t *testing.T) {
	casos := map[string]opcoes{
		"warmup negativo":        {tps: 10, duracao: time.Second, warmup: -time.Second, conexoes: 1},
		"warmup igual a duracao": {tps: 10, duracao: time.Second, warmup: time.Second, conexoes: 1},
		"warmup maior":           {tps: 10, duracao: time.Second, warmup: 2 * time.Second, conexoes: 1},
	}
	for nome, o := range casos {
		t.Run(nome, func(t *testing.T) {
			if err := executar(o); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

// --- rodada completa ---

// TestRodadaModeloAberto e o teste central do controle de taxa.
//
// O tempo de servico do autorizador e maior que o intervalo entre chegadas.
// Em modelo fechado a vazao desabaria para o inverso do tempo de servico; em
// modelo aberto todas as chegadas ocorrem no prazo e sao atendidas em paralelo.
func TestRodadaModeloAberto(t *testing.T) {
	const (
		tps     = 50.0
		duracao = 400 * time.Millisecond
		servico = 60 * time.Millisecond // 3x o intervalo entre chegadas
	)

	p, err := novoPool(autorizadorDeTeste(t, "00", servico), 16, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	agendador, err := ratelimit.NovoAberto(tps, duracao)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	coletor := metrics.NovoColetor(agendador.Chegadas(), 0)
	res := agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(context.Background(), p, ch))
	})

	const esperado = 20 // 50 TPS por 0,4 s
	if res.Chegadas != esperado {
		t.Errorf("Chegadas = %d, esperado %d", res.Chegadas, esperado)
	}

	medidos := coletor.Medidos()
	if len(medidos) != esperado {
		t.Fatalf("%d registros medidos, esperado %d", len(medidos), esperado)
	}
	for _, r := range medidos {
		if !r.Sucesso() {
			t.Errorf("STAN %s falhou: %s", r.STAN, r.Erro)
		}
	}

	// em modelo fechado, 20 requisicoes de 60ms levariam 1,2s
	if decorrido := res.Fim.Sub(res.Inicio); decorrido > time.Second {
		t.Errorf("rodada levou %v; em modelo aberto deveria terminar perto de %v", decorrido, duracao+servico)
	}
}

// TestRodadaSTANsUnicos confirma que cada resposta corresponde a sua propria
// requisicao mesmo com varias trocas simultaneas em conexoes distintas.
func TestRodadaSTANsUnicos(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "00", 5*time.Millisecond), 8, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	agendador, err := ratelimit.NovoAberto(200, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	coletor := metrics.NovoColetor(agendador.Chegadas(), 0)
	agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(context.Background(), p, ch))
	})

	vistos := map[string]bool{}
	for _, r := range coletor.Medidos() {
		if !r.Sucesso() {
			t.Errorf("STAN %s falhou: %s", r.STAN, r.Erro)
			continue
		}
		if vistos[r.STAN] {
			t.Errorf("STAN %s emitido duas vezes", r.STAN)
		}
		vistos[r.STAN] = true
	}

	if len(vistos) != 60 { // 200 TPS por 0,3 s
		t.Errorf("%d STANs distintos, esperado 60", len(vistos))
	}
}

func TestRodadaContaRecusasSeparadamente(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "51", 0), 4, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	agendador, err := ratelimit.NovoAberto(100, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	coletor := metrics.NovoColetor(agendador.Chegadas(), 0)
	res := agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(context.Background(), p, ch))
	})

	resumo, err := coletor.Resumir(
		metrics.Rodada{TPSAlvo: 100, ChegadasTotais: res.Chegadas},
		agendamento(res, 100),
		metrics.CapturarAmbiente(nil, nil),
	)
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	const esperado = 20
	if resumo.Desfechos.Recusadas != esperado {
		t.Errorf("Recusadas = %d, esperado %d", resumo.Desfechos.Recusadas, esperado)
	}
	if resumo.Desfechos.Aprovadas != 0 {
		t.Errorf("Aprovadas = %d, esperado 0", resumo.Desfechos.Aprovadas)
	}
	if resumo.Desfechos.ErrosTransporte != 0 || resumo.Desfechos.Timeouts != 0 {
		t.Errorf("recusa de negocio nao e falha de transporte: erros=%d timeouts=%d",
			resumo.Desfechos.ErrosTransporte, resumo.Desfechos.Timeouts)
	}
	if resumo.Desfechos.TaxaAprovacao != 0 {
		t.Errorf("TaxaAprovacao = %v, esperado 0", resumo.Desfechos.TaxaAprovacao)
	}
}

// TestRodadaRegistraOmissaoCoordenada confirma que a latencia de resposta
// parte do instante agendado.
//
// O autorizador demora mais que o intervalo entre chegadas e o pool tem uma
// unica conexao, entao as requisicoes enfileiram. A espera pela conexao nao
// aparece na latencia de servico, mas precisa aparecer na de resposta: e ela
// que descreve o que o cliente observou.
func TestRodadaRegistraOmissaoCoordenada(t *testing.T) {
	p, err := novoPool(autorizadorDeTeste(t, "00", 30*time.Millisecond), 1, 2*time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	agendador, err := ratelimit.NovoAberto(100, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	coletor := metrics.NovoColetor(agendador.Chegadas(), 0)
	agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(context.Background(), p, ch))
	})

	medidos := coletor.Medidos()
	if len(medidos) == 0 {
		t.Fatal("nenhum registro medido")
	}

	// a ultima requisicao enfileirou atras de todas as anteriores
	ultima := medidos[len(medidos)-1]
	if !ultima.Sucesso() {
		t.Fatalf("ultima requisicao falhou: %s", ultima.Erro)
	}
	if ultima.LatenciaResposta() <= ultima.LatenciaServico() {
		t.Errorf("latencia de resposta (%v) deveria superar a de servico (%v): a espera pela conexao sumiu da medicao",
			ultima.LatenciaResposta(), ultima.LatenciaServico())
	}

	// a espera cresce ao longo da rodada, porque as chegadas continuam no
	// ritmo do relogio enquanto o atendimento nao acompanha
	primeira := medidos[0]
	if primeira.Sucesso() && ultima.LatenciaResposta() <= primeira.LatenciaResposta() {
		t.Errorf("a latencia de resposta deveria crescer com o enfileiramento: primeira %v, ultima %v",
			primeira.LatenciaResposta(), ultima.LatenciaResposta())
	}
}

// TestRodadaEscreveArquivos exercita a saida completa do passo 4.
func TestRodadaEscreveArquivos(t *testing.T) {
	destino := t.TempDir()

	p, err := novoPool(autorizadorDeTeste(t, "00", 0), 4, time.Second)
	if err != nil {
		t.Fatalf("novoPool: %v", err)
	}
	defer p.fechar()

	agendador, err := ratelimit.NovoAberto(100, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	coletor := metrics.NovoColetor(agendador.Chegadas(), 10)
	res := agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		coletor.Registrar(requisitar(context.Background(), p, ch))
	})

	resumo, err := coletor.Resumir(
		metrics.Rodada{
			Inicio: res.Inicio, Fim: res.Fim,
			TPSAlvo: 100, Duracao: "300ms", Warmup: "100ms",
			ChegadasDescartada: 10, ChegadasTotais: res.Chegadas,
			Conexoes: 4, Repeticao: 2, Semente: 7,
		},
		agendamento(res, 100),
		metrics.CapturarAmbiente(map[string]string{"tps": "100"},
			json.RawMessage(`{"config":{"latency-base":"0s"}}`)),
	)
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	pasta, err := pastaDaRodada(destino, res.Inicio, 100, 2)
	if err != nil {
		t.Fatalf("pastaDaRodada: %v", err)
	}
	if !strings.HasSuffix(pasta, "-100tps-2") {
		t.Errorf("nome da pasta = %q, esperado sufixo -100tps-2", filepath.Base(pasta))
	}

	if err := escreverResultados(pasta, coletor, resumo); err != nil {
		t.Fatalf("escreverResultados: %v", err)
	}

	// raw.csv: cabecalho + uma linha por chegada medida
	bruto, err := os.ReadFile(filepath.Join(pasta, "raw.csv"))
	if err != nil {
		t.Fatalf("lendo raw.csv: %v", err)
	}
	linhas, err := csv.NewReader(bytes.NewReader(bruto)).ReadAll()
	if err != nil {
		t.Fatalf("relendo raw.csv: %v", err)
	}
	if len(linhas) != 21 { // 30 chegadas - 10 de warm-up, + cabecalho
		t.Errorf("%d linhas em raw.csv, esperado 21", len(linhas))
	}
	if linhas[0][0] != "stan" || linhas[0][5] != "latencia_resposta_us" {
		t.Errorf("cabecalho inesperado: %v", linhas[0])
	}

	// summary.json: precisa conter o bloco de ambiente completo
	dados, err := os.ReadFile(filepath.Join(pasta, "summary.json"))
	if err != nil {
		t.Fatalf("lendo summary.json: %v", err)
	}
	var lido metrics.Resumo
	if err := json.Unmarshal(dados, &lido); err != nil {
		t.Fatalf("relendo summary.json: %v", err)
	}
	if lido.Ambiente.VersaoGo == "" || lido.Ambiente.NumCPU == 0 || lido.Ambiente.LinhaDeComando == "" {
		t.Errorf("bloco de ambiente incompleto: %+v", lido.Ambiente)
	}
	if lido.Rodada.Semente != 7 || lido.Rodada.Repeticao != 2 {
		t.Errorf("parametros da rodada nao preservados: %+v", lido.Rodada)
	}
	if lido.Vazao.ChegadasMedidas != 20 {
		t.Errorf("ChegadasMedidas = %d, esperado 20", lido.Vazao.ChegadasMedidas)
	}
}

func TestPastaDaRodadaNomeia(t *testing.T) {
	raiz := t.TempDir()
	inicio := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)

	casos := map[float64]string{
		10:   "20260905T143000-10tps-1",
		1000: "20260905T143000-1000tps-1",
		2.5:  "20260905T143000-2.5tps-1",
	}
	for tps, esperado := range casos {
		pasta, err := pastaDaRodada(raiz, inicio, tps, 1)
		if err != nil {
			t.Fatalf("pastaDaRodada: %v", err)
		}
		if got := filepath.Base(pasta); got != esperado {
			t.Errorf("tps %v: pasta = %q, esperado %q", tps, got, esperado)
		}
	}
}

// --- relatorio ---

func resumoDeTeste(t *testing.T, tps float64, chegadas, descartadas int, de39 string, atrasoMedio time.Duration) metrics.Resumo {
	t.Helper()

	c := metrics.NovoColetor(chegadas, descartadas)
	base := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	for i := 0; i < chegadas; i++ {
		agendado := base.Add(time.Duration(i) * time.Millisecond)
		c.Registrar(metrics.Registro{
			Indice:   i,
			STAN:     fmt.Sprintf("%06d", i),
			Agendado: agendado,
			Envio:    agendado,
			Resposta: agendado.Add(2 * time.Millisecond),
			DE39:     de39,
		})
	}

	intervalo := time.Duration(float64(time.Second) / tps)
	r, err := c.Resumir(
		metrics.Rodada{
			Inicio: base, Fim: base.Add(time.Second),
			TPSAlvo: tps, Duracao: "1s", Warmup: "0s",
			ChegadasDescartada: descartadas, ChegadasTotais: chegadas,
			Conexoes: 8, Repeticao: 1,
		},
		metrics.Agendamento{
			AtrasoMedioUS:   atrasoMedio.Microseconds(),
			AtrasoMaximoUS:  (atrasoMedio * 10).Microseconds(),
			InjetorSaturado: atrasoMedio > intervalo,
		},
		metrics.CapturarAmbiente(nil, nil),
	)
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}
	return r
}

func TestRelatarSaida(t *testing.T) {
	var saida bytes.Buffer
	relatar(&saida, resumoDeTeste(t, 100, 100, 0, "00", 40*time.Microsecond), 0)

	esperados := []string{
		"alvo          : 100 TPS por 1s, 8 conexoes",
		"respondidas   : 100 (aprovadas 100, recusadas 0)",
		"falhas        : 0 erros de transporte, 0 timeouts",
		"vazao         : 100.00 TPS (100.0% do alvo)",
		"DE 39         : 00=100",
		"servico",
		"resposta",
		"p99.9",
	}
	for _, trecho := range esperados {
		if !strings.Contains(saida.String(), trecho) {
			t.Errorf("saida nao contem %q:\n%s", trecho, saida.String())
		}
	}
	if strings.Contains(saida.String(), "AVISO") {
		t.Errorf("aviso indevido com atraso medio de 40us a 100 TPS:\n%s", saida.String())
	}
}

// TestRelatarNaoAvisaPorPicoIsolado protege o criterio de saturacao.
//
// Um atraso maximo alto com atraso medio baixo e um sobressalto isolado do
// temporizador ou uma pausa do coletor de lixo, nao saturacao do injetor.
// Avisar nesse caso tornaria o aviso ruido em qualquer rodada de taxa alta.
func TestRelatarNaoAvisaPorPicoIsolado(t *testing.T) {
	// atraso medio de 120us contra intervalo de 2ms a 500 TPS; o maximo,
	// dez vezes maior, ainda nao caracteriza saturacao
	r := resumoDeTeste(t, 500, 100, 0, "00", 120*time.Microsecond)

	var saida bytes.Buffer
	relatar(&saida, r, 0)

	if strings.Contains(saida.String(), "AVISO") {
		t.Errorf("aviso indevido: pico isolado nao indica saturacao do injetor:\n%s", saida.String())
	}
}

func TestRelatarAvisaSobreSaturacaoDoInjetor(t *testing.T) {
	// atraso medio de 50ms contra intervalo de 10ms a 100 TPS
	r := resumoDeTeste(t, 100, 100, 0, "51", 50*time.Millisecond)

	var saida bytes.Buffer
	relatar(&saida, r, 3)

	if !strings.Contains(saida.String(), "AVISO") {
		t.Errorf("esperado aviso de saturacao do injetor:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "conexoes perdidas: 3") {
		t.Errorf("esperado relato de conexoes perdidas:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "DE 39         : 51=100") {
		t.Errorf("esperada distribuicao de DE 39:\n%s", saida.String())
	}
}

// TestConfigDoAutorizador cobre a ponte entre os dois processos: o autorizador
// grava a propria configuracao, o injetor a embute no summary.json.
func TestConfigDoAutorizador(t *testing.T) {
	dir := t.TempDir()

	valido := filepath.Join(dir, "valido.json")
	conteudo := `{"config":{"latency-base":"5ms","seed":99},"ambiente":{"gomaxprocs":4}}`
	if err := os.WriteFile(valido, []byte(conteudo), 0o644); err != nil {
		t.Fatalf("escrevendo: %v", err)
	}

	invalido := filepath.Join(dir, "invalido.json")
	if err := os.WriteFile(invalido, []byte("isto nao e json"), 0o644); err != nil {
		t.Fatalf("escrevendo: %v", err)
	}

	casos := map[string]struct {
		caminho string
		nulo    bool
	}{
		"arquivo valido":      {valido, false},
		"caminho vazio":       {"", true},
		"arquivo inexistente": {filepath.Join(dir, "nao-existe.json"), true},
		"json invalido":       {invalido, true},
	}

	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			got := configDoAutorizador(c.caminho)
			if c.nulo {
				if got != nil {
					t.Errorf("esperado nil, obtido %s", got)
				}
				return
			}
			if string(got) != conteudo {
				t.Errorf("conteudo = %s, esperado %s", got, conteudo)
			}
		})
	}
}

// TestConfigDoAutorizadorAusenteNaoAbortaARodada protege a decisao de nao
// falhar quando o arquivo nao existe: abortar custaria a medicao inteira.
func TestConfigDoAutorizadorAusenteNaoAbortaARodada(t *testing.T) {
	amb := metrics.CapturarAmbiente(nil, configDoAutorizador("/caminho/que/nao/existe.json"))

	var texto string
	if err := json.Unmarshal(amb.ConfigAutorizador, &texto); err != nil {
		t.Fatalf("config_autorizador deveria registrar a ausencia como string: %v", err)
	}
	if !strings.Contains(texto, "nao informado") {
		t.Errorf("config_autorizador = %q", texto)
	}
}
