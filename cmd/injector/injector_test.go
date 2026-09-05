package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
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

// --- rodada completa ---

// TestRodadaModeloAberto e o teste central do passo 3.
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

	var c contadores
	res := agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		if err := requisitar(context.Background(), p, ch, &c); err != nil {
			c.erros.Add(1)
			t.Logf("chegada %d: %v", ch.Indice, err)
		}
	})

	const esperado = 20 // 50 TPS por 0,4 s
	if res.Chegadas != esperado {
		t.Errorf("Chegadas = %d, esperado %d", res.Chegadas, esperado)
	}
	if got := c.aprovadas.Load(); got != esperado {
		t.Errorf("aprovadas = %d, esperado %d", got, esperado)
	}
	if got := c.erros.Load(); got != 0 {
		t.Errorf("erros = %d, esperado 0", got)
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

	var (
		mu     sync.Mutex
		vistos = map[string]bool{}
		falhas atomic.Int64
	)

	agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		conn, err := p.adquirir(context.Background())
		if err != nil {
			falhas.Add(1)
			return
		}
		req := requisicao(ch)
		// trocar ja confere a correlacao entre o STAN enviado e o recebido
		if _, err := trocar(conn, req); err != nil {
			p.descartar(conn)
			falhas.Add(1)
			t.Logf("chegada %d: %v", ch.Indice, err)
			return
		}
		p.devolver(conn)

		mu.Lock()
		if vistos[req.STAN] {
			t.Errorf("STAN %s emitido duas vezes", req.STAN)
		}
		vistos[req.STAN] = true
		mu.Unlock()
	})

	if got := falhas.Load(); got != 0 {
		t.Errorf("%d falhas na rodada", got)
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

	var c contadores
	agendador.Executar(context.Background(), func(ch ratelimit.Chegada) {
		if err := requisitar(context.Background(), p, ch, &c); err != nil {
			c.erros.Add(1)
		}
	})

	const esperado = 20
	if got := c.recusadas.Load(); got != esperado {
		t.Errorf("recusadas = %d, esperado %d", got, esperado)
	}
	if got := c.aprovadas.Load(); got != 0 {
		t.Errorf("aprovadas = %d, esperado 0", got)
	}
	if got := c.erros.Load(); got != 0 {
		t.Errorf("erros = %d; recusa de negocio nao e erro de transporte", got)
	}
}

// --- relatorio ---

func TestRelatarSaida(t *testing.T) {
	var c contadores
	c.aprovadas.Store(95)
	c.recusadas.Store(5)

	inicio := time.Now()
	res := ratelimit.Resultado{
		Inicio:       inicio,
		Fim:          inicio.Add(10 * time.Second),
		Chegadas:     100,
		AtrasoMaximo: 250 * time.Microsecond,
		AtrasoMedio:  40 * time.Microsecond,
	}

	var saida bytes.Buffer
	relatar(&saida, 10, 10*time.Second, 8, res, &c, 0)

	esperados := []string{
		"alvo          : 10 TPS por 10s, 8 conexoes",
		"chegadas      : 100",
		"respondidas   : 100 (aprovadas 95, recusadas 5)",
		"erros         : 0",
		"vazao         : 10.00 TPS (100.0% do alvo)",
	}
	for _, trecho := range esperados {
		if !strings.Contains(saida.String(), trecho) {
			t.Errorf("saida nao contem %q:\n%s", trecho, saida.String())
		}
	}
	if strings.Contains(saida.String(), "AVISO") {
		t.Errorf("aviso indevido com atraso de 250us a 10 TPS:\n%s", saida.String())
	}
}

// TestRelatarAvisaSobreSaturacaoDoInjetor cobre o aviso que impede a leitura
// equivocada de uma rodada em que o gargalo foi o proprio gerador de carga.
func TestRelatarAvisaSobreSaturacaoDoInjetor(t *testing.T) {
	var c contadores
	c.aprovadas.Store(50)

	inicio := time.Now()
	res := ratelimit.Resultado{
		Inicio:       inicio,
		Fim:          inicio.Add(time.Second),
		Chegadas:     100,
		AtrasoMaximo: 80 * time.Millisecond,
		AtrasoMedio:  50 * time.Millisecond, // intervalo a 100 TPS e 10ms
	}

	var saida bytes.Buffer
	relatar(&saida, 100, time.Second, 8, res, &c, 3)

	if !strings.Contains(saida.String(), "AVISO") {
		t.Errorf("esperado aviso de saturacao do injetor:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "conexoes perdidas: 3") {
		t.Errorf("esperado relato de conexoes perdidas:\n%s", saida.String())
	}
	if !strings.Contains(saida.String(), "50.0% do alvo") {
		t.Errorf("esperado percentual do alvo:\n%s", saida.String())
	}
}
