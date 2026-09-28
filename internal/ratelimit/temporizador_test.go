package ratelimit

import (
	"sort"
	"testing"
	"time"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/clock"
)

// TestGranularidadeDoTemporizador mede o excesso de um time.Timer em relacao
// ao prazo pedido, e e o teste que explica o teto de injecao do aparato.
//
// O relogio de alta resolucao corrigiu a MEDICAO da latencia, nao o
// AGENDAMENTO. A espera entre chegadas continua sujeita ao temporizador do
// runtime, cuja granularidade no Windows e a do tique do sistema.
//
// A distincao tem consequencia concreta. Enquanto agenda e medicao usavam o
// mesmo time.Now, de passo grosseiro, o atraso do temporizador era invisivel
// para a propria medicao: o injetor reportava atraso medio de 500 a 660 us
// independentemente da taxa, e a varredura sugeria teto proximo de 2000 TPS.
// Medido contra o QueryPerformanceCounter, o atraso real apareceu entre 1200 e
// 2300 us, e o teto caiu para 500 TPS.
//
// O numero antigo nao estava errado por acaso: a medicao era cega ao proprio
// erro. Este teste registra a grandeza que impoe o teto.
//
// O desvio ocorre nos DOIS sentidos: medido contra o QueryPerformanceCounter, o
// temporizador do runtime dispara ate centenas de microssegundos ANTES do prazo
// pedido. E o que se espera de um temporizador governado por um relogio de
// passo grosseiro: quando ele julga que 1 ms passou, o tempo real decorrido
// esta em qualquer ponto de uma janela da largura do tique.
func TestGranularidadeDoTemporizador(t *testing.T) {
	if testing.Short() {
		t.Skip("medicao de temporizador: pulada em -short")
	}

	pedidos := []time.Duration{
		100 * time.Microsecond,
		500 * time.Microsecond,
		time.Millisecond,
		2 * time.Millisecond,
		10 * time.Millisecond,
	}

	t.Logf("%-12s %12s %12s %12s %12s", "pedido", "desvio min", "desvio p50", "desvio p90", "desvio max")

	var excessoMedianoEm1ms time.Duration

	for _, pedido := range pedidos {
		const amostras = 200
		excessos := make([]time.Duration, 0, amostras)

		temporizador := time.NewTimer(time.Hour)
		if !temporizador.Stop() {
			<-temporizador.C
		}

		for i := 0; i < amostras; i++ {
			alvo := clock.Agora().Add(pedido)
			temporizador.Reset(pedido)
			<-temporizador.C
			excessos = append(excessos, clock.Desde(alvo))
		}
		temporizador.Stop()

		sort.Slice(excessos, func(i, j int) bool { return excessos[i] < excessos[j] })
		p50 := excessos[len(excessos)/2]
		p90 := excessos[int(float64(len(excessos))*0.9)]
		maximo := excessos[len(excessos)-1]

		t.Logf("%-12v %12v %12v %12v %12v",
			pedido, excessos[0].Round(time.Microsecond), p50.Round(time.Microsecond),
			p90.Round(time.Microsecond), maximo.Round(time.Microsecond))

		if pedido == time.Millisecond {
			excessoMedianoEm1ms = p50
		}
	}

	// o desvio do temporizador supera em ordens de grandeza a resolucao do
	// relogio: sao dois limites distintos do aparato, e so o segundo foi
	// corrigido pelo internal/clock
	if excessoMedianoEm1ms <= 100*clock.Resolucao() {
		t.Errorf("desvio mediano de %v nao supera em muito a resolucao do relogio de %v; "+
			"o teste perdeu a capacidade de distinguir agendamento de medicao",
			excessoMedianoEm1ms, clock.Resolucao())
	}
}
