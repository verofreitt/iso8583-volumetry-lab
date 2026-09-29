package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEhCalibracao(t *testing.T) {
	casos := map[string]bool{
		"--echo-only":                    true,
		"-echo-only":                     true,
		"--echo-only --quiet":            true,
		"":                               false,
		"--latency-base 20ms":            false,
		"--latency-base 20ms --seed 42":  false,
		"--approval-rate 0.85 --seed 42": false,
	}
	for args, esperado := range casos {
		if got := ehCalibracao(args); got != esperado {
			t.Errorf("ehCalibracao(%q) = %v, esperado %v", args, got, esperado)
		}
	}
}

func TestArgsDoAutorizador(t *testing.T) {
	got := argsDoAutorizador("  --latency-base 20ms   --seed 42 ")
	esperado := []string{"--latency-base", "20ms", "--seed", "42"}
	if len(got) != len(esperado) {
		t.Fatalf("%d argumentos, esperado %d: %v", len(got), len(esperado), got)
	}
	for i := range esperado {
		if got[i] != esperado[i] {
			t.Errorf("argumento %d = %q, esperado %q", i, got[i], esperado[i])
		}
	}
	if len(argsDoAutorizador("")) != 0 {
		t.Error("string vazia deveria produzir nenhum argumento")
	}
}

// TestExperimentoNaoReportaLimitesDoAparato protege contra um relato falso.
//
// Numa varredura contra alvo com latencia configurada, a mediana de servico e
// a latencia do alvo. Reporta-la como "piso de servico, ida e volta em
// loopback" descreveria o sistema sob teste como se fosse o aparato, e o numero
// iria ao artigo com o rotulo errado.
func TestExperimentoNaoReportaLimitesDoAparato(t *testing.T) {
	niveis := []Nivel{nivelCom(100, repeticaoBoa()), nivelCom(1000, repeticaoBoa())}

	experimento := Relatorio{
		Calibracao:   false,
		Procedimento: Procedimento{Repeticoes: 5, Duracao: "2m", Conexoes: 32, ModoAlvo: "--latency-base 20ms"},
		Niveis:       niveis,
	}
	experimento.concluir()

	var saida bytes.Buffer
	experimento.imprimir(&saida)
	texto := saida.String()

	if strings.Contains(texto, "limites declarados do aparato") {
		t.Errorf("experimento nao deveria reportar limites do aparato:\n%s", texto)
	}
	if strings.Contains(texto, "ida e volta em loopback") {
		t.Errorf("a mediana de servico de um experimento nao e o piso de loopback:\n%s", texto)
	}
	if !strings.Contains(texto, "=== experimento ===") {
		t.Errorf("o cabecalho deveria identificar a varredura como experimento:\n%s", texto)
	}
	if !strings.Contains(texto, "--latency-base 20ms") {
		t.Errorf("o alvo deveria ser identificado pelos argumentos reais:\n%s", texto)
	}
	if experimento.Limites.TetoInjecaoTPS != 0 {
		t.Errorf("TetoInjecaoTPS = %g; limites nao devem ser apurados fora da calibracao",
			experimento.Limites.TetoInjecaoTPS)
	}
}

// TestCalibracaoReportaLimites e o contraponto.
func TestCalibracaoReportaLimites(t *testing.T) {
	calibracao := Relatorio{
		Calibracao:   true,
		Procedimento: Procedimento{Repeticoes: 5, Duracao: "15s", Conexoes: 32, ModoAlvo: "--echo-only"},
		Niveis:       []Nivel{nivelCom(100, repeticaoBoa()), nivelCom(1000, repeticaoBoa())},
	}
	calibracao.concluir()

	var saida bytes.Buffer
	calibracao.imprimir(&saida)
	texto := saida.String()

	if !strings.Contains(texto, "limites declarados do aparato") {
		t.Errorf("calibracao deveria reportar os limites:\n%s", texto)
	}
	if !strings.Contains(texto, "=== calibracao do aparato ===") {
		t.Errorf("cabecalho de calibracao ausente:\n%s", texto)
	}
	if calibracao.Limites.TetoInjecaoTPS != 1000 {
		t.Errorf("TetoInjecaoTPS = %g, esperado 1000", calibracao.Limites.TetoInjecaoTPS)
	}
}

// TestSementeDaRodada cobre a correcao da pseudorreplicacao.
//
// Sem variacao, as repeticoes de um nivel recebem exatamente as mesmas
// decisoes de negocio, porque o desfecho e funcao de (semente do mock, STAN) e
// as tres entradas ficam fixas. Foi o que aconteceu no experimento de 28/09.
func TestSementeDaRodada(t *testing.T) {
	const base = 42

	// fixa: toda repeticao consome a massa na mesma ordem
	for rep := 1; rep <= 5; rep++ {
		if got := sementeDaRodada(base, rep, false); got != base {
			t.Errorf("sem variar, repeticao %d deu semente %d, esperado %d", rep, got, base)
		}
	}

	// variavel: cada repeticao recebe uma semente distinta
	vistas := map[int64]int{}
	for rep := 1; rep <= 5; rep++ {
		vistas[sementeDaRodada(base, rep, true)]++
	}
	if len(vistas) != 5 {
		t.Errorf("%d sementes distintas em 5 repeticoes, esperado 5: %v", len(vistas), vistas)
	}

	// a mesma repeticao precisa dar sempre a mesma semente, ou a rodada deixa
	// de ser reproduzivel
	if sementeDaRodada(base, 3, true) != sementeDaRodada(base, 3, true) {
		t.Error("a semente de uma repeticao mudou entre chamadas")
	}
}
