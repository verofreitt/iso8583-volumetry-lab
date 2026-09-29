package main

import (
	"math"
	"testing"
)

// TestErlangBContraValoresConhecidos ancora a recorrencia.
func TestErlangBContraValoresConhecidos(t *testing.T) {
	casos := []struct {
		c        int
		a        float64
		esperado float64
	}{
		// um servidor, carga 1 erlang: metade das chegadas e bloqueada
		{1, 1, 0.5},
		// um servidor, carga 0,5
		{1, 0.5, 1.0 / 3.0},
		// dois servidores, carga 1: B = a^2/2 / (1 + a + a^2/2) = 0,5/2,5
		{2, 1, 0.2},
	}
	for _, c := range casos {
		if got := ErlangB(c.c, c.a); math.Abs(got-c.esperado) > 1e-9 {
			t.Errorf("ErlangB(%d, %v) = %.10f, esperado %.10f", c.c, c.a, got, c.esperado)
		}
	}
}

// TestErlangCReduzAMM1 e a ancora mais forte: com um unico servidor, a
// probabilidade de esperar e exatamente a utilizacao.
func TestErlangCReduzAMM1(t *testing.T) {
	for _, rho := range []float64{0.1, 0.5, 0.8, 0.95} {
		if got := ErlangC(1, rho); math.Abs(got-rho) > 1e-9 {
			t.Errorf("ErlangC(1, %v) = %.10f; em M/M/1 deveria ser a propria utilizacao", rho, got)
		}
	}
}

func TestErlangCCresceComACarga(t *testing.T) {
	anterior := 0.0
	for _, a := range []float64{5, 10, 20, 25, 28, 30, 31} {
		c := ErlangC(32, a)
		if c < anterior {
			t.Errorf("Erlang C caiu de %v para %v ao aumentar a carga para %v erlangs", anterior, c, a)
		}
		if c < 0 || c > 1 {
			t.Errorf("ErlangC(32, %v) = %v, fora de [0,1]", a, c)
		}
		anterior = c
	}
	if ErlangC(32, 40) != 1 {
		t.Error("sistema instavel deveria dar probabilidade de espera 1")
	}
}

// TestEsperaNoQuantilReproduzATabelaDoArtigo fixa os numeros citados na secao
// 10.4 de docs/experimento.md.
//
// Foram conferidos de forma independente antes de entrar no texto: uma
// implementacao errada de Erlang produziria previsoes plausiveis e falsas, e o
// argumento do artigo depende delas.
func TestEsperaNoQuantilReproduzATabelaDoArtigo(t *testing.T) {
	const (
		c      = 32
		lambda = 1000.0
	)
	casos := []struct {
		servicoMS  float64
		esperadoMS float64
	}{
		{27, 17.7},
		{29, 37.5},
		{30, 62.2},
		{31, 135.9},
	}
	for _, caso := range casos {
		w, ok := EsperaNoQuantil(c, lambda, caso.servicoMS/1000, 99)
		if !ok {
			t.Fatalf("servico de %v ms deveria ser estavel", caso.servicoMS)
		}
		if math.Abs(w*1000-caso.esperadoMS) > 0.1 {
			t.Errorf("servico de %v ms: p99 de espera = %.1f ms, esperado %.1f ms",
				caso.servicoMS, w*1000, caso.esperadoMS)
		}
	}
}

func TestEsperaNoQuantilInstavel(t *testing.T) {
	// 32 ms de servico a 1000 TPS com 32 conexoes: utilizacao exatamente 1
	if _, ok := EsperaNoQuantil(32, 1000, 0.032, 99); ok {
		t.Error("sistema com utilizacao 1 deveria ser reportado como instavel")
	}
	if _, ok := EsperaNoQuantil(32, 1000, 0.040, 99); ok {
		t.Error("sistema sobrecarregado deveria ser reportado como instavel")
	}
}

// TestEsperaNoQuantilCargaBaixa: quando menos chegadas esperam do que a cauda
// pedida, o quantil da espera e zero.
func TestEsperaNoQuantilCargaBaixa(t *testing.T) {
	// 27 ms a 100 TPS com 32 conexoes: utilizacao de 8%, quase ninguem espera
	w, ok := EsperaNoQuantil(32, 100, 0.027, 99)
	if !ok {
		t.Fatal("sistema deveria ser estavel")
	}
	if w != 0 {
		t.Errorf("p99 de espera = %v; com utilizacao de 8%% quase nenhuma chegada espera", w)
	}
}

func TestEsperaCresceComOServico(t *testing.T) {
	anterior := 0.0
	for _, ms := range []float64{27, 28, 29, 30, 31} {
		w, ok := EsperaNoQuantil(32, 1000, ms/1000, 99)
		if !ok {
			t.Fatalf("servico de %v ms deveria ser estavel", ms)
		}
		if w <= anterior {
			t.Errorf("espera nao cresceu ao subir o servico para %v ms: %v apos %v", ms, w, anterior)
		}
		anterior = w
	}
}
