//go:build !windows

package clock

import "time"

// referencia ancora as leituras monotonicas do runtime, que nos sistemas
// diferentes do Windows ja tem resolucao de nanossegundos.
var referencia = time.Now()

func lerMonotonico() int64 {
	return int64(time.Since(referencia))
}

// resolucao mede empiricamente o menor passo nao-nulo entre leituras
// sucessivas do relogio.
//
// Aqui nao ha uma frequencia declarada para consultar, como o
// QueryPerformanceFrequency do Windows, entao o valor e observado.
func resolucao() time.Duration {
	menor := time.Duration(0)
	for i := 0; i < 100; i++ {
		anterior := time.Now()
		var passo time.Duration
		for passo == 0 {
			passo = time.Since(anterior)
		}
		if menor == 0 || passo < menor {
			menor = passo
		}
	}
	return menor
}

func fonte() string {
	return "relogio monotonico do runtime"
}
