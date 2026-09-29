// Command fila calcula utilizacao e espera prevista de um pool de conexoes
// tratado como sistema M/M/c.
//
// Serve para confrontar a diferenca observada entre latencia de resposta e de
// servico com a previsao de um modelo de filas, e assim verificar se o
// enfileiramento no pool explica a diferenca.
//
// ATENCAO ao uso. O M/M/c pressupoe tempo de servico exponencial, de
// coeficiente de variacao 1, e chegadas de Poisson. Nenhuma das duas hipoteses
// vale aqui: o servico do alvo e uma constante somada a uma lognormal, de CV
// proximo de 0,44, e as chegadas do modelo aberto sao quase deterministicas.
// As duas diferencas reduzem a fila em relacao ao M/M/c, que portanto
// SUPERESTIMA a espera.
//
// O resultado vale como verificacao de ordem de grandeza do mecanismo, nao
// como ajuste de modelo.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func main() {
	servidores := flag.Int("c", 32, "servidores; aqui, conexoes do pool")
	taxa := flag.Float64("lambda", 1000, "taxa de chegada, por segundo")
	servicos := flag.String("servico", "27,29,30,31", "tempos medios de servico a avaliar, em ms")
	quantil := flag.Float64("quantil", 99, "quantil da espera a reportar")
	flag.Parse()

	fmt.Printf("M/M/%d, chegadas a %g por segundo\n\n", *servidores, *taxa)
	fmt.Printf("%10s %11s %12s %10s %12s\n",
		"E[S] (ms)", "capac (TPS)", "utilizacao", "Erlang C", fmt.Sprintf("p%g espera", *quantil))

	for _, campo := range strings.Split(*servicos, ",") {
		ms, err := strconv.ParseFloat(strings.TrimSpace(campo), 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "servico %q invalido: %v\n", campo, err)
			os.Exit(1)
		}

		es := ms / 1000
		cap := float64(*servidores) / es
		rho := *taxa * es / float64(*servidores)
		c := ErlangC(*servidores, *taxa*es)

		espera := "instavel"
		if w, ok := EsperaNoQuantil(*servidores, *taxa, es, *quantil); ok {
			espera = fmt.Sprintf("%.1f ms", w*1000)
		}

		fmt.Printf("%10.1f %11.0f %11.1f%% %10.3f %12s\n", ms, cap, 100*rho, c, espera)
	}

	fmt.Printf("\nM/M/c pressupoe servico exponencial e chegadas de Poisson; nenhuma\n")
	fmt.Printf("das duas vale aqui, e o modelo superestima a espera. Use como\n")
	fmt.Printf("verificacao de ordem de grandeza, nao como ajuste.\n")
}

// ErlangB devolve a probabilidade de bloqueio de um sistema com c servidores e
// carga oferecida a, em erlangs.
//
// Calculada pela recorrencia numericamente estavel, e nao pela razao de
// fatoriais, que estoura para c moderado.
func ErlangB(c int, a float64) float64 {
	b := 1.0
	for n := 1; n <= c; n++ {
		b = a * b / (float64(n) + a*b)
	}
	return b
}

// ErlangC devolve a probabilidade de uma chegada ter de esperar.
func ErlangC(c int, a float64) float64 {
	rho := a / float64(c)
	if rho >= 1 {
		return 1
	}
	b := ErlangB(c, a)
	return b / (1 - rho*(1-b))
}

// EsperaNoQuantil devolve o quantil informado da espera em fila.
//
// Em M/M/c a cauda da espera e exponencial: P(W > t) = C * exp(-(c*mu - λ)t).
// Resolver para P(W > t) = 1 - q/100 da o quantil. Devolve false quando o
// sistema e instavel.
func EsperaNoQuantil(c int, lambda, es, quantil float64) (float64, bool) {
	a := lambda * es
	if a/float64(c) >= 1 {
		return 0, false
	}

	cauda := 1 - quantil/100
	pEspera := ErlangC(c, a)
	if pEspera <= cauda {
		// menos chegadas esperam do que a cauda pedida: o quantil e zero
		return 0, true
	}

	mu := 1 / es
	drenagem := float64(c)*mu - lambda
	return math.Log(pEspera/cauda) / drenagem, true
}
