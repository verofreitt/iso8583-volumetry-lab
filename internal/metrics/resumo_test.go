package metrics

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func rodadaDeTeste(tps float64, totais, descartadas int) Rodada {
	return Rodada{
		Inicio:             base,
		Fim:                base.Add(10 * time.Second),
		TPSAlvo:            tps,
		Duracao:            "10s",
		Warmup:             "0s",
		ChegadasDescartada: descartadas,
		ChegadasTotais:     totais,
		Conexoes:           8,
		Repeticao:          1,
		Semente:            42,
	}
}

func ambienteDeTeste() Ambiente {
	return CapturarAmbiente(map[string]string{"tps": "10"},
		json.RawMessage(`{"config":{"latency-base":"5ms"}}`))
}

// TestEstatisticasPercentis confirma que os percentis vem do histograma sobre
// os valores individuais, e nao de qualquer agregacao previa.
//
// A serie tem 1000 valores: 980 de 1ms e 20 de 100ms. A mediana e o p95 ficam
// em 1ms; o p99 e o p99,9 precisam enxergar a cauda, porque os 2% lentos
// ocupam as posicoes 981 a 1000 da serie ordenada. Uma media movel ou
// amostragem apagaria esses valores, que sao o objeto do experimento.
//
// A proporcao de 2% e deliberada. Com exatamente 1% de valores lentos o p99
// cairia sobre a fronteira entre os dois grupos, e o resultado dependeria da
// convencao de arredondamento do histograma em vez da presenca da cauda.
func TestEstatisticasPercentis(t *testing.T) {
	var valores []time.Duration
	for i := 0; i < 980; i++ {
		valores = append(valores, time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		valores = append(valores, 100*time.Millisecond)
	}

	e, err := estatisticas(valores)
	if err != nil {
		t.Fatalf("estatisticas: %v", err)
	}

	if e.Amostras != 1000 {
		t.Errorf("Amostras = %d, esperado 1000", e.Amostras)
	}
	if e.MedianaUS < 990 || e.MedianaUS > 1010 {
		t.Errorf("MedianaUS = %d, esperado ~1000", e.MedianaUS)
	}
	if e.P95US < 990 || e.P95US > 1010 {
		t.Errorf("P95US = %d, esperado ~1000", e.P95US)
	}
	// os 20 valores de 100ms ocupam os 2% superiores: p99 e p99,9 os alcancam
	if e.P99US < 99000 {
		t.Errorf("P99US = %d, esperado ~100000: a cauda sumiu da medicao", e.P99US)
	}
	if e.P999US < 99000 {
		t.Errorf("P999US = %d, esperado ~100000: a cauda sumiu da medicao", e.P999US)
	}
	if e.MaximoUS < 99000 || e.MaximoUS > 101000 {
		t.Errorf("MaximoUS = %d, esperado ~100000", e.MaximoUS)
	}
	if e.MinimoUS < 990 || e.MinimoUS > 1010 {
		t.Errorf("MinimoUS = %d, esperado ~1000", e.MinimoUS)
	}
	// media = (980*1ms + 20*100ms)/1000 = 2,98ms
	if e.MediaUS < 2900 || e.MediaUS > 3100 {
		t.Errorf("MediaUS = %.1f, esperado ~2980", e.MediaUS)
	}
}

func TestEstatisticasVazio(t *testing.T) {
	e, err := estatisticas(nil)
	if err != nil {
		t.Fatalf("estatisticas: %v", err)
	}
	if e.Amostras != 0 {
		t.Errorf("Amostras = %d, esperado 0", e.Amostras)
	}
}

// TestEstatisticasLatenciaSubmicrossegundo cobre o piso do histograma: o menor
// valor rastreavel e 1 us, e latencias menores precisam ser registradas ali em
// vez de causar erro.
func TestEstatisticasLatenciaSubmicrossegundo(t *testing.T) {
	e, err := estatisticas([]time.Duration{100 * time.Nanosecond, 200 * time.Nanosecond})
	if err != nil {
		t.Fatalf("estatisticas: %v", err)
	}
	if e.Amostras != 2 {
		t.Errorf("Amostras = %d, esperado 2", e.Amostras)
	}
}

// TestResumirSeparaDesfechos e o teste que protege a exigencia da secao 6 do
// CLAUDE.md: recusa de negocio, erro de transporte e timeout sao coisas
// diferentes e nao podem ser somados.
func TestResumirSeparaDesfechos(t *testing.T) {
	c := NovoColetor(10, 0)

	for i := 0; i < 5; i++ { // aprovadas
		c.Registrar(registroEm(i, 0, 5))
	}
	for i := 5; i < 8; i++ { // recusadas
		r := registroEm(i, 0, 5)
		r.DE39 = "51"
		c.Registrar(r)
	}
	c.Registrar(Registro{ // erro de transporte
		Indice: 8, STAN: "000008",
		Agendado: base, Envio: base,
		Erro: "conexao fechada",
	})
	c.Registrar(Registro{ // timeout
		Indice: 9, STAN: "000009",
		Agendado: base, Envio: base,
		Erro: "prazo esgotado", Timeout: true,
	})

	r, err := c.Resumir(rodadaDeTeste(10, 10, 0), Agendamento{}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	d := r.Desfechos
	if d.Aprovadas != 5 {
		t.Errorf("Aprovadas = %d, esperado 5", d.Aprovadas)
	}
	if d.Recusadas != 3 {
		t.Errorf("Recusadas = %d, esperado 3", d.Recusadas)
	}
	if d.ErrosTransporte != 1 {
		t.Errorf("ErrosTransporte = %d, esperado 1", d.ErrosTransporte)
	}
	if d.Timeouts != 1 {
		t.Errorf("Timeouts = %d, esperado 1", d.Timeouts)
	}
	if got, want := d.TaxaAprovacao, 5.0/8.0; got != want {
		t.Errorf("TaxaAprovacao = %v, esperado %v", got, want)
	}
	if d.DistribuicaoDE39["00"] != 5 || d.DistribuicaoDE39["51"] != 3 {
		t.Errorf("DistribuicaoDE39 = %v", d.DistribuicaoDE39)
	}

	// falhas nao entram nas estatisticas de latencia
	if r.LatenciaServico.Amostras != 8 {
		t.Errorf("LatenciaServico.Amostras = %d, esperado 8", r.LatenciaServico.Amostras)
	}
}

func TestResumirCodigosDE39Ordenados(t *testing.T) {
	d := Desfechos{DistribuicaoDE39: map[string]int64{"51": 3, "00": 5, "05": 1}}
	got := d.CodigosDE39()
	esperado := []string{"00", "05", "51"}
	for i := range esperado {
		if got[i] != esperado[i] {
			t.Errorf("CodigosDE39() = %v, esperado %v", got, esperado)
			break
		}
	}
}

// TestResumirVazaoUsaJanelaDeChegadas protege o denominador da metrica mais
// importante do experimento. Ver a discussao em docs/experimento.md secao 5.3.
func TestResumirVazaoUsaJanelaDeChegadas(t *testing.T) {
	c := NovoColetor(100, 0)
	for i := 0; i < 100; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}

	r, err := c.Resumir(rodadaDeTeste(10, 100, 0), Agendamento{}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	if r.Vazao.JanelaSegundos != 10 {
		t.Errorf("JanelaSegundos = %v, esperado 10", r.Vazao.JanelaSegundos)
	}
	if r.Vazao.AlcancadoTPS != 10 {
		t.Errorf("AlcancadoTPS = %v, esperado 10", r.Vazao.AlcancadoTPS)
	}
	if r.Vazao.PercentualDoAlvo != 100 {
		t.Errorf("PercentualDoAlvo = %v, esperado 100", r.Vazao.PercentualDoAlvo)
	}
}

// TestResumirVazaoAbaixoDoAlvo confirma que requisicoes sem resposta derrubam
// a vazao alcancada, que e o sinal de saturacao que a metrica deve capturar.
func TestResumirVazaoAbaixoDoAlvo(t *testing.T) {
	c := NovoColetor(100, 0)
	for i := 0; i < 60; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}
	for i := 60; i < 100; i++ {
		c.Registrar(Registro{
			Indice: i, STAN: leftPad(i),
			Agendado: base, Envio: base,
			Erro: "prazo esgotado", Timeout: true,
		})
	}

	r, err := c.Resumir(rodadaDeTeste(10, 100, 0), Agendamento{}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	if r.Vazao.AlcancadoTPS != 6 {
		t.Errorf("AlcancadoTPS = %v, esperado 6", r.Vazao.AlcancadoTPS)
	}
	if r.Vazao.PercentualDoAlvo != 60 {
		t.Errorf("PercentualDoAlvo = %v, esperado 60", r.Vazao.PercentualDoAlvo)
	}
}

// TestResumirWarmupForaDaJanela confirma que a janela de vazao considera
// apenas as chegadas medidas.
func TestResumirWarmupForaDaJanela(t *testing.T) {
	c := NovoColetor(100, 20)
	for i := 0; i < 100; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}

	r, err := c.Resumir(rodadaDeTeste(10, 100, 20), Agendamento{}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	if r.Vazao.ChegadasMedidas != 80 {
		t.Errorf("ChegadasMedidas = %d, esperado 80", r.Vazao.ChegadasMedidas)
	}
	if r.Vazao.JanelaSegundos != 8 {
		t.Errorf("JanelaSegundos = %v, esperado 8", r.Vazao.JanelaSegundos)
	}
	if r.Vazao.Respondidas != 80 {
		t.Errorf("Respondidas = %d, esperado 80", r.Vazao.Respondidas)
	}
	if r.Vazao.AlcancadoTPS != 10 {
		t.Errorf("AlcancadoTPS = %v, esperado 10", r.Vazao.AlcancadoTPS)
	}
}

// TestAmbienteCompleto protege a exigencia da secao 5.4 do CLAUDE.md: sem o
// bloco de ambiente a rodada e inutil para o artigo.
func TestAmbienteCompleto(t *testing.T) {
	a := ambienteDeTeste()

	if a.VersaoGo != runtime.Version() {
		t.Errorf("VersaoGo = %q, esperado %q", a.VersaoGo, runtime.Version())
	}
	if a.GOMAXPROCS != runtime.GOMAXPROCS(0) {
		t.Errorf("GOMAXPROCS = %d, esperado %d", a.GOMAXPROCS, runtime.GOMAXPROCS(0))
	}
	if a.NumCPU != runtime.NumCPU() {
		t.Errorf("NumCPU = %d, esperado %d", a.NumCPU, runtime.NumCPU())
	}
	if a.GOOS != runtime.GOOS || a.GOARCH != runtime.GOARCH {
		t.Errorf("GOOS/GOARCH = %s/%s", a.GOOS, a.GOARCH)
	}
	if a.GOGC == "" {
		t.Error("GOGC vazio: o valor efetivo precisa constar mesmo quando nao foi definido")
	}
	if a.LinhaDeComando == "" {
		t.Error("LinhaDeComando vazia")
	}
	if len(a.ConfigAutorizador) == 0 {
		t.Error("ConfigAutorizador vazia")
	}
	if len(a.FlagsInjetor) == 0 {
		t.Error("FlagsInjetor vazio")
	}
	if a.RelogioFonte == "" {
		t.Error("RelogioFonte vazio: sem ela nao ha como julgar o piso de medicao")
	}
	if a.RelogioResolucaoNS <= 0 {
		t.Errorf("RelogioResolucaoNS = %d, esperado positivo", a.RelogioResolucaoNS)
	}
	if a.RelogioResolucaoNS > 10000 {
		t.Errorf("resolucao de %d ns e grosseira demais para medir loopback", a.RelogioResolucaoNS)
	}
}

// TestEscreverJSONContemBlocos confirma a estrutura do summary.json.
func TestEscreverJSONContemBlocos(t *testing.T) {
	c := NovoColetor(10, 0)
	for i := 0; i < 10; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}

	resumo, err := c.Resumir(rodadaDeTeste(10, 10, 0), Agendamento{AtrasoMedioUS: 120, AtrasoMaximoUS: 3400}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}

	var buf bytes.Buffer
	if err := resumo.EscreverJSON(&buf); err != nil {
		t.Fatalf("EscreverJSON: %v", err)
	}

	var lido map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &lido); err != nil {
		t.Fatalf("relendo o JSON: %v", err)
	}

	for _, bloco := range []string{"rodada", "vazao", "agendamento", "latencia_servico", "latencia_resposta", "desfechos", "ambiente"} {
		if _, ok := lido[bloco]; !ok {
			t.Errorf("summary.json sem o bloco %q", bloco)
		}
	}

	// o arquivo e indentado para permitir comparacao entre rodadas com diff
	if !strings.Contains(buf.String(), "\n  \"rodada\"") {
		t.Error("summary.json deveria estar indentado")
	}
}

// TestAmbienteSemConfigDoAutorizador confirma que a ausencia do arquivo vira
// um registro explicito, e nao um campo vazio que passaria despercebido.
func TestAmbienteSemConfigDoAutorizador(t *testing.T) {
	a := CapturarAmbiente(map[string]string{"tps": "10"}, nil)

	var texto string
	if err := json.Unmarshal(a.ConfigAutorizador, &texto); err != nil {
		t.Fatalf("config_autorizador ausente deveria ser uma string JSON: %v", err)
	}
	if !strings.Contains(texto, "nao informado") {
		t.Errorf("config_autorizador = %q, esperado registro explicito de ausencia", texto)
	}
}

func TestResumirSemRegistros(t *testing.T) {
	r, err := NovoColetor(0, 0).Resumir(rodadaDeTeste(10, 0, 0), Agendamento{}, ambienteDeTeste())
	if err != nil {
		t.Fatalf("Resumir: %v", err)
	}
	if r.Vazao.Respondidas != 0 || r.LatenciaServico.Amostras != 0 {
		t.Error("resumo de rodada vazia deveria ter contadores zerados")
	}
}
