package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

// servidorDeTeste sobe o autorizador em uma porta efemera de loopback e
// devolve o endereco.
func servidorDeTeste(t *testing.T, ajustar func(*Config)) string {
	t.Helper()

	c := configPadrao()
	if ajustar != nil {
		ajustar(&c)
	}
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	s := &servidor{comportamento: comp, silencioso: true}
	if c.MaxConns > 0 {
		s.vagas = make(chan struct{}, c.MaxConns)
	}
	go func() { _ = s.servir(ln) }()

	return ln.Addr().String()
}

func requisicaoDeTeste(t *testing.T, stan string) []byte {
	t.Helper()

	empacotada, err := iso.Requisicao{
		PAN:                   "9999990000000014",
		ProcessingCode:        "000000",
		Valor:                 "000000010000",
		STAN:                  stan,
		MCC:                   "5411",
		POSEntryMode:          "021",
		InstituicaoAdquirente: "000001",
		RRN:                   "000000000001",
		TerminalID:            "TERM0001",
		Moeda:                 "986",
		Instante:              time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC),
	}.Pack()
	if err != nil {
		t.Fatalf("montando a 0100: %v", err)
	}
	return empacotada
}

// trocar envia uma 0100 e devolve o DE 39 com o tempo decorrido.
func trocar(t *testing.T, conn net.Conn, stan string) (string, time.Duration) {
	t.Helper()

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	inicio := time.Now()
	if err := iso.WriteFrame(conn, requisicaoDeTeste(t, stan)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	bruta, err := iso.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	decorrido := time.Since(inicio)

	resp, err := iso.Parse(bruta)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	mti, err := resp.GetMTI()
	if err != nil {
		t.Fatalf("GetMTI: %v", err)
	}
	if mti != iso.MTIAuthResponse {
		t.Errorf("MTI = %q, esperado %q", mti, iso.MTIAuthResponse)
	}

	obtido, err := resp.GetString(11)
	if err != nil {
		t.Fatalf("GetString DE 11: %v", err)
	}
	if obtido != stan {
		t.Errorf("DE 11 = %q, esperado %q", obtido, stan)
	}

	de39, err := resp.GetString(39)
	if err != nil {
		t.Fatalf("GetString DE 39: %v", err)
	}
	return de39, decorrido
}

func TestAutorizadorRespondeAutorizacao(t *testing.T) {
	conn, err := net.Dial("tcp", servidorDeTeste(t, nil))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	if de39, _ := trocar(t, conn, "000001"); de39 != aprovado {
		t.Errorf("DE 39 = %q, esperado %q", de39, aprovado)
	}
}

func TestAutorizadorMultiplasRequisicoesNaMesmaConexao(t *testing.T) {
	conn, err := net.Dial("tcp", servidorDeTeste(t, nil))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	for _, stan := range []string{"000001", "000002", "000003"} {
		trocar(t, conn, stan)
	}
}

// TestAutorizadorAplicaLatenciaConfigurada confirma que a latencia base chega
// ao cliente.
func TestAutorizadorAplicaLatenciaConfigurada(t *testing.T) {
	const base = 40 * time.Millisecond

	conn, err := net.Dial("tcp", servidorDeTeste(t, func(c *Config) { c.LatenciaBase = base }))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	_, decorrido := trocar(t, conn, "000001")
	if decorrido < base {
		t.Errorf("resposta em %v, esperado ao menos %v", decorrido, base)
	}
}

// TestAutorizadorRespeitaTetoDeSimultaneas cobre --max-conns.
//
// Com teto de 1 e latencia de servico apreciavel, duas requisicoes simultaneas
// em conexoes distintas precisam ser atendidas em serie: a segunda so termina
// depois da primeira.
func TestAutorizadorRespeitaTetoDeSimultaneas(t *testing.T) {
	const base = 100 * time.Millisecond

	endereco := servidorDeTeste(t, func(c *Config) {
		c.LatenciaBase = base
		c.MaxConns = 1
	})

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		decorridos []time.Duration
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", endereco)
			if err != nil {
				t.Errorf("conectando: %v", err)
				return
			}
			defer conn.Close()

			_, d := trocar(t, conn, "00000"+string(rune('1'+i)))
			mu.Lock()
			decorridos = append(decorridos, d)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(decorridos) != 2 {
		t.Fatalf("%d respostas, esperado 2", len(decorridos))
	}

	// serializadas: a mais lenta espera a outra terminar
	maior := decorridos[0]
	if decorridos[1] > maior {
		maior = decorridos[1]
	}
	if maior < 2*base {
		t.Errorf("resposta mais lenta em %v; com teto de 1 as duas deveriam serializar em ao menos %v", maior, 2*base)
	}
}

// TestAutorizadorSemTetoAtendeEmParalelo e o contraponto do teste anterior.
func TestAutorizadorSemTetoAtendeEmParalelo(t *testing.T) {
	const base = 100 * time.Millisecond

	endereco := servidorDeTeste(t, func(c *Config) { c.LatenciaBase = base })

	var wg sync.WaitGroup
	inicio := time.Now()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", endereco)
			if err != nil {
				t.Errorf("conectando: %v", err)
				return
			}
			defer conn.Close()
			trocar(t, conn, "00000"+string(rune('1'+i)))
		}(i)
	}
	wg.Wait()

	if decorrido := time.Since(inicio); decorrido > 3*base {
		t.Errorf("4 requisicoes paralelas levaram %v; sem teto deveriam terminar perto de %v", decorrido, base)
	}
}

// TestAutorizadorMesmaSementeMesmasRespostas confirma a reprodutibilidade
// ponta a ponta: duas execucoes com a mesma semente devolvem exatamente os
// mesmos codigos para os mesmos STANs.
func TestAutorizadorMesmaSementeMesmasRespostas(t *testing.T) {
	stans := []string{"000001", "000002", "000003", "000004", "000005", "000006"}

	coletar := func(semente int64) []string {
		endereco := servidorDeTeste(t, func(c *Config) {
			c.TaxaAprovacao = 0.5
			c.Semente = semente
		})
		conn, err := net.Dial("tcp", endereco)
		if err != nil {
			t.Fatalf("conectando: %v", err)
		}
		defer conn.Close()

		var codigos []string
		for _, s := range stans {
			de39, _ := trocar(t, conn, s)
			codigos = append(codigos, de39)
		}
		return codigos
	}

	primeira := coletar(7)
	segunda := coletar(7)

	for i := range primeira {
		if primeira[i] != segunda[i] {
			t.Errorf("STAN %s: %q na primeira execucao, %q na segunda", stans[i], primeira[i], segunda[i])
		}
	}
}

func TestResponderRejeitaMensagemInvalida(t *testing.T) {
	s := &servidor{comportamento: novo(t, nil), silencioso: true}
	if _, err := s.responder([]byte("nao e uma mensagem iso 8583")); err == nil {
		t.Error("esperado erro para payload invalido, obtido nil")
	}
}

// --- registro da configuracao ---

// TestEscreverConfig cobre o arquivo que alimenta o bloco config_autorizador
// do summary.json do injetor.
func TestEscreverConfig(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "autorizador.json")

	c := configPadrao()
	c.LatenciaBase = 5 * time.Millisecond
	c.LatenciaJitter = 2 * time.Millisecond
	c.TaxaAprovacao = 0.85
	c.MaxConns = 32
	c.Semente = 99

	if err := escreverConfig(caminho, c); err != nil {
		t.Fatalf("escreverConfig: %v", err)
	}

	dados, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatalf("lendo: %v", err)
	}

	var lido struct {
		Config struct {
			LatenciaBase   string  `json:"latency-base"`
			LatenciaJitter string  `json:"latency-jitter"`
			LatenciaDist   string  `json:"latency-dist"`
			TaxaAprovacao  float64 `json:"approval-rate"`
			DistRecusas    string  `json:"decline-dist"`
			MaxConns       int     `json:"max-conns"`
			Semente        int64   `json:"seed"`
		} `json:"config"`
		Ambiente Ambiente `json:"ambiente"`
	}
	if err := json.Unmarshal(dados, &lido); err != nil {
		t.Fatalf("relendo o JSON: %v", err)
	}

	// duracoes legiveis, nao nanossegundos
	if lido.Config.LatenciaBase != "5ms" {
		t.Errorf("latency-base = %q, esperado \"5ms\"", lido.Config.LatenciaBase)
	}
	if lido.Config.LatenciaJitter != "2ms" {
		t.Errorf("latency-jitter = %q, esperado \"2ms\"", lido.Config.LatenciaJitter)
	}
	if lido.Config.TaxaAprovacao != 0.85 || lido.Config.MaxConns != 32 || lido.Config.Semente != 99 {
		t.Errorf("config nao preservada: %+v", lido.Config)
	}

	// o ambiente do autorizador importa: os dois processos disputam CPU
	if lido.Ambiente.VersaoGo == "" || lido.Ambiente.GOMAXPROCS == 0 ||
		lido.Ambiente.NumCPU == 0 || lido.Ambiente.GOGC == "" ||
		lido.Ambiente.LinhaDeComando == "" {
		t.Errorf("bloco de ambiente incompleto: %+v", lido.Ambiente)
	}
}
