package clock

import (
	"testing"
	"time"
)

// TestResolucaoSuficienteParaLoopback e o teste que motiva a existencia deste
// pacote. Uma troca em loopback leva dezenas de microssegundos; um relogio com
// passo de centenas de microssegundos a mede como zero.
func TestResolucaoSuficienteParaLoopback(t *testing.T) {
	r := Resolucao()
	t.Logf("fonte: %s, resolucao: %v", Fonte(), r)

	if r <= 0 {
		t.Fatal("resolucao nao determinada")
	}
	if r > 10*time.Microsecond {
		t.Errorf("resolucao de %v e grosseira demais para medir uma troca em loopback", r)
	}
}

// TestAgoraAvancaEmPassosFinos compara o passo observado deste relogio com o
// do time.Now do runtime.
func TestAgoraAvancaEmPassosFinos(t *testing.T) {
	var menor time.Duration
	for i := 0; i < 1000; i++ {
		a := Agora()
		var passo time.Duration
		for passo == 0 {
			passo = Agora().Sub(a)
		}
		if menor == 0 || passo < menor {
			menor = passo
		}
	}

	t.Logf("menor passo observado: %v", menor)
	if menor > 10*time.Microsecond {
		t.Errorf("menor passo de %v: o relogio nao resolve latencias de loopback", menor)
	}
}

// TestDuracaoCurtaNaoZera cobre o defeito concreto encontrado antes deste
// pacote existir: os instantes de envio e resposta saiam identicos, e a
// latencia de servico saia zero.
func TestDuracaoCurtaNaoZera(t *testing.T) {
	zeros := 0
	const tentativas = 200

	for i := 0; i < tentativas; i++ {
		inicio := Agora()
		// trabalho curto, da ordem de uma troca em loopback
		soma := 0
		for j := 0; j < 2000; j++ {
			soma += j
		}
		_ = soma
		if Agora().Sub(inicio) <= 0 {
			zeros++
		}
	}

	if zeros > 0 {
		t.Errorf("%d de %d medicoes curtas sairam zeradas", zeros, tentativas)
	}
}

func TestAgoraEhMonotonico(t *testing.T) {
	anterior := Agora()
	for i := 0; i < 10000; i++ {
		agora := Agora()
		if agora.Before(anterior) {
			t.Fatalf("relogio retrocedeu: %v antes de %v", agora, anterior)
		}
		anterior = agora
	}
}

// TestAncoraProximaDoRelogioDeParede confirma que os instantes continuam
// correspondendo ao horario civil, ainda que a precisao venha do contador.
func TestAncoraProximaDoRelogioDeParede(t *testing.T) {
	diferenca := Agora().Sub(time.Now())
	if diferenca < 0 {
		diferenca = -diferenca
	}
	if diferenca > 100*time.Millisecond {
		t.Errorf("relogio afastado do horario civil em %v", diferenca)
	}
}

func TestDesdeEAte(t *testing.T) {
	inicio := Agora()
	futuro := inicio.Add(50 * time.Millisecond)

	if d := Ate(futuro); d <= 0 || d > 50*time.Millisecond {
		t.Errorf("Ate = %v, esperado algo em (0, 50ms]", d)
	}
	if d := Desde(inicio); d < 0 {
		t.Errorf("Desde = %v, esperado nao-negativo", d)
	}
	if d := Ate(inicio.Add(-time.Second)); d >= 0 {
		t.Errorf("Ate de instante passado = %v, esperado negativo", d)
	}
}
