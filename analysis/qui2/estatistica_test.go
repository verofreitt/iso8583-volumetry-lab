package main

import (
	"math"
	"testing"
)

// TestQui2ContraValoresConhecidos ancora a implementacao em valores
// tabelados.
//
// A funcao gama incompleta foi escrita a mao, por causa da restricao de
// dependencias da secao 8 do CLAUDE.md. Uma implementacao errada produziria
// p-valores plausiveis e falsos, que e o pior modo de falha possivel para uma
// analise que vai ao artigo. Os pontos abaixo sao os percentis criticos
// classicos da distribuicao qui-quadrado.
func TestQui2ContraValoresConhecidos(t *testing.T) {
	casos := []struct {
		x        float64
		gl       int
		pValor   float64
		toleranc float64
	}{
		// percentis de 95%: p deve sair exatamente em 0,05
		{3.841459, 1, 0.05, 1e-5},
		{5.991465, 2, 0.05, 1e-5},
		{7.814728, 3, 0.05, 1e-5},
		{12.591587, 6, 0.05, 1e-5},
		// percentis de 99%
		{6.634897, 1, 0.01, 1e-5},
		{9.210340, 2, 0.01, 1e-5},
		{16.811894, 6, 0.01, 1e-5},
		// percentis de 50%
		{0.454936, 1, 0.50, 1e-5},
		{1.386294, 2, 0.50, 1e-5},
		{5.348121, 6, 0.50, 1e-5},
		// Cauda extrema. Com 1 grau de liberdade, P(X2 > x) = 2*Q(sqrt(x)),
		// onde Q e a cauda superior da normal padrao. Para x = 100:
		// 2 * Q(10) = 2 * 7.619853024e-24 = 1.5239706048e-23.
		{100, 1, 1.5239706048e-23, 1e-32},
	}

	for _, c := range casos {
		got := qui2Cauda(c.x, c.gl)
		if math.Abs(got-c.pValor) > c.toleranc {
			t.Errorf("qui2Cauda(%v, %d) = %.10g, esperado %.10g", c.x, c.gl, got, c.pValor)
		}
	}
}

func TestQui2CaudaExtremos(t *testing.T) {
	if got := qui2Cauda(0, 3); got != 1 {
		t.Errorf("qui2Cauda(0, 3) = %v, esperado 1", got)
	}
	if got := qui2Cauda(-1, 3); got != 1 {
		t.Errorf("qui2Cauda(-1, 3) = %v, esperado 1", got)
	}
	// monotonicidade: a cauda so pode diminuir conforme x cresce
	anterior := 1.0
	for x := 0.5; x < 40; x += 0.5 {
		p := qui2Cauda(x, 4)
		if p > anterior {
			t.Fatalf("p-valor cresceu de %v para %v em x = %v", anterior, p, x)
		}
		anterior = p
	}
}

// TestQui2TabelaIndependente: tabela perfeitamente proporcional tem
// estatistica zero e p-valor 1.
func TestQui2TabelaIndependente(t *testing.T) {
	tab := Tabela{
		Valores:   []string{"a", "b", "c"},
		Recusas:   []int{150, 150, 150},
		Aprovadas: []int{850, 850, 850},
	}

	res, err := Qui2(tab)
	if err != nil {
		t.Fatalf("Qui2: %v", err)
	}
	if res.Estatistica > 1e-9 {
		t.Errorf("estatistica = %v, esperado 0 para tabela proporcional", res.Estatistica)
	}
	if math.Abs(res.PValor-1) > 1e-9 {
		t.Errorf("p-valor = %v, esperado 1", res.PValor)
	}
	if res.GrausDeLiberdade != 2 {
		t.Errorf("graus de liberdade = %d, esperado 2", res.GrausDeLiberdade)
	}
}

// TestQui2TabelaDependente: um valor com taxa muito diferente derruba a
// independencia. E o cenario do controle positivo.
func TestQui2TabelaDependente(t *testing.T) {
	tab := Tabela{
		Valores:   []string{"5411", "5812", "5967"},
		Recusas:   []int{150, 150, 400},
		Aprovadas: []int{850, 850, 600},
	}

	res, err := Qui2(tab)
	if err != nil {
		t.Fatalf("Qui2: %v", err)
	}
	if res.PValor >= 0.05 {
		t.Errorf("p-valor = %v; um valor com taxa 0,40 contra 0,15 deveria rejeitar a independencia", res.PValor)
	}
}

// TestQui2AvisaEsperadoBaixo confirma que a premissa do teste e verificada.
func TestQui2AvisaEsperadoBaixo(t *testing.T) {
	tab := Tabela{
		Valores:   []string{"a", "b"},
		Recusas:   []int{1, 0},
		Aprovadas: []int{5, 4},
	}

	res, err := Qui2(tab)
	if err != nil {
		t.Fatalf("Qui2: %v", err)
	}
	if res.EsperadoMinimo >= 5 {
		t.Errorf("EsperadoMinimo = %v, esperado abaixo de 5", res.EsperadoMinimo)
	}
}

func TestQui2RejeitaTabelasImpossiveis(t *testing.T) {
	casos := map[string]Tabela{
		"vazia":         {},
		"um valor so":   {Valores: []string{"a"}, Recusas: []int{10}, Aprovadas: []int{90}},
		"sem recusas":   {Valores: []string{"a", "b"}, Recusas: []int{0, 0}, Aprovadas: []int{50, 50}},
		"sem aprovadas": {Valores: []string{"a", "b"}, Recusas: []int{50, 50}, Aprovadas: []int{0, 0}},
	}
	for nome, tab := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := Qui2(tab); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

// --- Wilson ---

// TestWilsonContraValoresConhecidos ancora o intervalo em casos calculados a
// mao a partir da definicao.
//
// Para p = 15/100 e z = 1.959964:
//
//	denominador = 1 + z2/n            = 1.03841459
//	centro      = (p + z2/2n) / den   = 0.169207295 / 1.03841459 = 0.1629472
//	margem      = z*sqrt(p(1-p)/n + z2/4n2) / den
//	            = 1.959964 * 0.03702751 / 1.03841459             = 0.0698883
//	intervalo   = [0.0930599, 0.2328356]
func TestWilsonContraValoresConhecidos(t *testing.T) {
	inf, sup := Wilson(15, 100)
	if math.Abs(inf-0.0930599) > 1e-7 || math.Abs(sup-0.2328356) > 1e-7 {
		t.Errorf("Wilson(15, 100) = [%.7f, %.7f], esperado [0.0930599, 0.2328356]", inf, sup)
	}

	// p = 40/100
	inf, sup = Wilson(40, 100)
	if math.Abs(inf-0.3094013) > 1e-7 || math.Abs(sup-0.4979974) > 1e-7 {
		t.Errorf("Wilson(40, 100) = [%.7f, %.7f], esperado [0.3094013, 0.4979974]", inf, sup)
	}
}

// TestWilsonPermaneceNoIntervaloValido e a razao de preferir Wilson ao
// intervalo normal simples, que escaparia de [0,1] nos extremos.
func TestWilsonPermaneceNoIntervaloValido(t *testing.T) {
	casos := [][2]int{{0, 10}, {10, 10}, {1, 1000}, {999, 1000}, {0, 1}, {1, 1}}
	for _, c := range casos {
		inf, sup := Wilson(c[0], c[1])
		if inf < 0 || sup > 1 || inf > sup {
			t.Errorf("Wilson(%d, %d) = [%v, %v], fora de [0,1] ou invertido", c[0], c[1], inf, sup)
		}
	}
	if inf, sup := Wilson(0, 0); inf != 0 || sup != 0 {
		t.Errorf("Wilson(0, 0) = [%v, %v], esperado [0, 0]", inf, sup)
	}
}

// TestWilsonEstreitaComAmostra confirma o comportamento esperado do intervalo.
func TestWilsonEstreitaComAmostra(t *testing.T) {
	larguraAnterior := 1.0
	for _, n := range []int{10, 100, 1000, 10000} {
		inf, sup := Wilson(n*15/100, n)
		largura := sup - inf
		if largura >= larguraAnterior {
			t.Errorf("com n = %d a largura foi %v, nao menor que %v", n, largura, larguraAnterior)
		}
		larguraAnterior = largura
	}
}

// TestWilsonCobreATaxaVerdadeira e o criterio usado para afirmar, no artigo,
// que a taxa injetada foi recuperada.
func TestWilsonCobreATaxaVerdadeira(t *testing.T) {
	// 7000 transacoes com taxa verdadeira de 0,40 produzem cerca de 2800
	// recusas; o intervalo precisa conter 0,40
	inf, sup := Wilson(2800, 7000)
	if inf > 0.40 || sup < 0.40 {
		t.Errorf("IC [%.4f, %.4f] nao cobre a taxa verdadeira 0.40", inf, sup)
	}
	// e precisa ser estreito o bastante para distinguir de 0,15
	if inf <= 0.15 {
		t.Errorf("IC inferior %.4f nao separa 0,40 de 0,15", inf)
	}
}
