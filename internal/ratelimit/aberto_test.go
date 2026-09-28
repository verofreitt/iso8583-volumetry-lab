package ratelimit

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNovoAbertoRejeitaParametrosInvalidos(t *testing.T) {
	casos := []struct {
		nome    string
		tps     float64
		duracao time.Duration
	}{
		{"tps zero", 0, time.Second},
		{"tps negativo", -1, time.Second},
		{"tps infinito", math.Inf(1), time.Second},
		{"tps nan", math.NaN(), time.Second},
		{"duracao zero", 10, 0},
		{"duracao negativa", 10, -time.Second},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if _, err := NovoAberto(c.tps, c.duracao); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

// TestDeslocamentoNaoDeriva e o teste que justifica calcular o instante de cada
// chegada a partir do indice em vez de acumular um intervalo.
//
// A 3 TPS o intervalo e 333,333...ms, que nao e representavel em nanossegundos.
// Acumular o intervalo truncado faria a rodada derivar; o calculo por indice
// mantem o erro limitado a um nanossegundo em qualquer ponto.
func TestDeslocamentoNaoDeriva(t *testing.T) {
	a, err := NovoAberto(3, time.Hour)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	for _, i := range []int{1, 3, 1000, 10800} {
		esperado := time.Duration(math.Round(float64(i) / 3 * float64(time.Second)))
		obtido := a.deslocamento(i)
		if diferenca := obtido - esperado; diferenca > time.Nanosecond || diferenca < -time.Nanosecond {
			t.Errorf("deslocamento(%d) = %v, esperado %v", i, obtido, esperado)
		}
	}

	// o deslocamento da chegada de indice 3 deve ser exatamente um segundo
	if got := a.deslocamento(3); got != time.Second {
		t.Errorf("deslocamento(3) = %v, esperado 1s", got)
	}
}

func TestChegadasQuantidade(t *testing.T) {
	casos := []struct {
		tps      float64
		duracao  time.Duration
		esperado int
	}{
		{10, time.Second, 10},
		{10, 2 * time.Second, 20},
		{1, 5 * time.Second, 5},
		{2.5, 4 * time.Second, 10},
	}
	for _, c := range casos {
		a, err := NovoAberto(c.tps, c.duracao)
		if err != nil {
			t.Fatalf("NovoAberto: %v", err)
		}
		if got := a.Chegadas(); got != c.esperado {
			t.Errorf("%v TPS por %v: Chegadas() = %d, esperado %d", c.tps, c.duracao, got, c.esperado)
		}
	}
}

// TestExecutarDisparaQuantidadeEsperada confirma a taxa aplicada.
func TestExecutarDisparaQuantidadeEsperada(t *testing.T) {
	a, err := NovoAberto(50, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	var contador atomic.Int64
	res := a.Executar(context.Background(), func(Chegada) {
		contador.Add(1)
	})

	const esperado = 20 // 50 TPS por 0,4 s
	if res.Chegadas != esperado {
		t.Errorf("Chegadas = %d, esperado %d", res.Chegadas, esperado)
	}
	if got := contador.Load(); got != esperado {
		t.Errorf("funcao chamada %d vezes, esperado %d", got, esperado)
	}
}

// TestExecutarModeloAberto e o teste central do pacote.
//
// A funcao de chegada bloqueia por muito mais tempo que o intervalo entre
// chegadas. Em modelo fechado isso reduziria drasticamente o numero de
// chegadas, porque cada uma esperaria a anterior terminar. Em modelo aberto o
// numero de chegadas depende apenas do relogio e permanece o esperado.
func TestExecutarModeloAberto(t *testing.T) {
	a, err := NovoAberto(100, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	var (
		contador       atomic.Int64
		simultaneas    atomic.Int64
		maxSimultaneas atomic.Int64
	)

	res := a.Executar(context.Background(), func(Chegada) {
		atual := simultaneas.Add(1)
		for {
			anterior := maxSimultaneas.Load()
			if atual <= anterior || maxSimultaneas.CompareAndSwap(anterior, atual) {
				break
			}
		}
		// bloqueia por 20 intervalos de chegada
		time.Sleep(200 * time.Millisecond)
		simultaneas.Add(-1)
		contador.Add(1)
	})

	const esperado = 30 // 100 TPS por 0,3 s
	if res.Chegadas != esperado {
		t.Errorf("Chegadas = %d, esperado %d: chamadas lentas nao devem reduzir a taxa", res.Chegadas, esperado)
	}
	if got := contador.Load(); got != esperado {
		t.Errorf("funcao concluida %d vezes, esperado %d", got, esperado)
	}

	// se as chamadas fossem serializadas, nunca haveria mais de uma em voo
	if got := maxSimultaneas.Load(); got < 2 {
		t.Errorf("maximo de chamadas simultaneas = %d; em modelo aberto deve haver sobreposicao", got)
	}
}

// TestExecutarAguardaChamadasEmVoo garante que Executar so retorna depois que
// toda requisicao disparada terminou. Sem isso, o injetor encerraria a rodada
// descartando respostas ainda em transito e subestimaria a vazao.
func TestExecutarAguardaChamadasEmVoo(t *testing.T) {
	a, err := NovoAberto(20, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	var concluidas atomic.Int64
	res := a.Executar(context.Background(), func(Chegada) {
		time.Sleep(150 * time.Millisecond)
		concluidas.Add(1)
	})

	if got := concluidas.Load(); int(got) != res.Chegadas {
		t.Errorf("%d chamadas concluidas para %d chegadas: Executar retornou antes do fim", got, res.Chegadas)
	}
	if !res.Fim.After(res.Inicio) {
		t.Error("Fim deveria ser posterior a Inicio")
	}
}

// TestExecutarInstantesAgendados verifica que o instante entregue a funcao e o
// instante pretendido, uniformemente espacado, e nao o momento do disparo.
// E esse valor que corrige a omissao coordenada na medicao.
func TestExecutarInstantesAgendados(t *testing.T) {
	a, err := NovoAberto(50, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	var (
		mu        sync.Mutex
		recebidas []Chegada
	)

	res := a.Executar(context.Background(), func(c Chegada) {
		mu.Lock()
		recebidas = append(recebidas, c)
		mu.Unlock()
	})

	if len(recebidas) != res.Chegadas {
		t.Fatalf("%d chegadas recebidas, esperado %d", len(recebidas), res.Chegadas)
	}

	vistos := make(map[int]time.Time, len(recebidas))
	for _, c := range recebidas {
		if anterior, ok := vistos[c.Indice]; ok {
			t.Errorf("indice %d entregue duas vezes (%v e %v)", c.Indice, anterior, c.Agendado)
		}
		vistos[c.Indice] = c.Agendado
	}

	// os instantes agendados sao exatos, independentemente do atraso real
	for i, agendado := range vistos {
		esperado := res.Inicio.Add(a.deslocamento(i))
		if !agendado.Equal(esperado) {
			t.Errorf("chegada %d agendada para %v, esperado %v", i, agendado, esperado)
		}
	}
}

// TestExecutarAtrasoContabilizado confirma que o agendador registra o proprio
// atraso quando nao consegue sustentar a taxa. A taxa pedida aqui e alta o
// bastante para que o relogio do sistema nao a acompanhe com precisao.
func TestExecutarAtrasoContabilizado(t *testing.T) {
	const tps = 20000

	a, err := NovoAberto(tps, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	res := a.Executar(context.Background(), func(Chegada) {})

	if res.AtrasoMaximo < 0 {
		t.Errorf("AtrasoMaximo negativo: %v", res.AtrasoMaximo)
	}
	t.Logf("a %d TPS: %d chegadas, atraso maximo de agendamento %v",
		tps, res.Chegadas, res.AtrasoMaximo)
}

// TestExecutarCancelamento confirma que cancelar o contexto interrompe o
// agendamento sem abandonar chamadas em voo.
func TestExecutarCancelamento(t *testing.T) {
	a, err := NovoAberto(50, 10*time.Second)
	if err != nil {
		t.Fatalf("NovoAberto: %v", err)
	}

	ctx, cancelar := context.WithCancel(context.Background())
	var concluidas atomic.Int64

	go func() {
		time.Sleep(150 * time.Millisecond)
		cancelar()
	}()

	inicio := time.Now()
	res := a.Executar(ctx, func(Chegada) {
		time.Sleep(50 * time.Millisecond)
		concluidas.Add(1)
	})
	decorrido := time.Since(inicio)

	if decorrido > 2*time.Second {
		t.Errorf("Executar levou %v; o cancelamento deveria ter interrompido a rodada de 10s", decorrido)
	}
	if res.Chegadas == 0 {
		t.Error("nenhuma chegada disparada antes do cancelamento")
	}
	if res.Chegadas >= a.Chegadas() {
		t.Errorf("Chegadas = %d; esperado menos que o total de %d por causa do cancelamento", res.Chegadas, a.Chegadas())
	}
	if got := concluidas.Load(); int(got) != res.Chegadas {
		t.Errorf("%d chamadas concluidas para %d chegadas disparadas: chamadas em voo foram abandonadas", got, res.Chegadas)
	}
}
