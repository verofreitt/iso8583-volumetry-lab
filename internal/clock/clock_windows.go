//go:build windows

package clock

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procContador   = kernel32.NewProc("QueryPerformanceCounter")
	procFrequencia = kernel32.NewProc("QueryPerformanceFrequency")

	// frequencia do contador, em tiques por segundo. Em maquinas atuais fica
	// em 10 MHz, o que corresponde a resolucao de 100 ns.
	frequencia = lerFrequencia()

	// origemTiques permite converter para nanossegundos sobre um valor
	// pequeno, evitando o estouro que ocorreria ao multiplicar o contador
	// absoluto por um bilhao.
	origemTiques = lerContador()
)

func lerFrequencia() int64 {
	var f int64
	syscall.SyscallN(procFrequencia.Addr(), uintptr(unsafe.Pointer(&f)))
	if f <= 0 {
		// sem contador de alta resolucao nao ha o que fazer aqui; o pacote
		// cai para o relogio do runtime e a resolucao declarada no
		// summary.json passa a refleti-lo.
		return 0
	}
	return f
}

func lerContador() int64 {
	var c int64
	syscall.SyscallN(procContador.Addr(), uintptr(unsafe.Pointer(&c)))
	return c
}

// lerMonotonico devolve nanossegundos desde a inicializacao do pacote.
func lerMonotonico() int64 {
	if frequencia == 0 {
		return int64(time.Since(time.Time{}))
	}

	tiques := lerContador() - origemTiques

	// a divisao e feita em duas partes para nao perder precisao nem estourar:
	// os segundos inteiros primeiro, o resto depois.
	segundos := tiques / frequencia
	resto := tiques % frequencia
	return segundos*int64(time.Second) + resto*int64(time.Second)/frequencia
}

func resolucao() time.Duration {
	if frequencia == 0 {
		return 0
	}
	return time.Duration(int64(time.Second) / frequencia)
}

func fonte() string {
	if frequencia == 0 {
		return "runtime (QueryPerformanceCounter indisponivel)"
	}
	return "QueryPerformanceCounter"
}
