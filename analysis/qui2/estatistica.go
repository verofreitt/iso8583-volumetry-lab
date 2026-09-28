package main

import (
	"fmt"
	"math"
)

// Este arquivo implementa o qui-quadrado e o intervalo de Wilson usando apenas
// a biblioteca padrao, como exige a secao 8 do CLAUDE.md. Trazer uma
// dependencia de estatistica exigiria justifica-la em uma frase no artigo, e
// as duas funcoes necessarias cabem em poucas dezenas de linhas.

// Tabela e uma tabela de contingencia 2 x k: para cada valor do atributo, a
// contagem de recusas e de aprovacoes.
type Tabela struct {
	Valores   []string
	Recusas   []int
	Aprovadas []int
}

// Totais por linha, coluna e geral.
func (t Tabela) Totais() (porValor []int, recusas, aprovadas, geral int) {
	porValor = make([]int, len(t.Valores))
	for i := range t.Valores {
		porValor[i] = t.Recusas[i] + t.Aprovadas[i]
		recusas += t.Recusas[i]
		aprovadas += t.Aprovadas[i]
	}
	return porValor, recusas, aprovadas, recusas + aprovadas
}

// ResultadoQui2 e o desfecho do teste de independencia.
type ResultadoQui2 struct {
	Estatistica      float64
	GrausDeLiberdade int
	PValor           float64

	// EsperadoMinimo e a menor frequencia esperada da tabela. O teste
	// pressupoe frequencias esperadas suficientes; abaixo de 5 o p-valor
	// deixa de ser confiavel e isso precisa ser reportado, nao ignorado.
	EsperadoMinimo float64
}

// Qui2 aplica o teste de independencia de Pearson sobre a tabela.
//
// A hipotese nula e que a recusa independe do atributo. Rejeita-la significa
// que a taxa de recusa difere entre os valores do atributo mais do que o acaso
// explicaria.
func Qui2(t Tabela) (ResultadoQui2, error) {
	porValor, recusas, aprovadas, geral := t.Totais()
	if geral == 0 {
		return ResultadoQui2{}, fmt.Errorf("tabela vazia")
	}
	if len(t.Valores) < 2 {
		return ResultadoQui2{}, fmt.Errorf("o teste exige ao menos dois valores do atributo, recebido %d", len(t.Valores))
	}
	if recusas == 0 || aprovadas == 0 {
		return ResultadoQui2{}, fmt.Errorf("uma das colunas esta vazia: %d recusas, %d aprovadas", recusas, aprovadas)
	}

	var (
		x2       float64
		esperMin = math.Inf(1)
	)
	for i := range t.Valores {
		linha := float64(porValor[i])
		for _, par := range [2]struct {
			observado int
			coluna    int
		}{{t.Recusas[i], recusas}, {t.Aprovadas[i], aprovadas}} {
			esperado := linha * float64(par.coluna) / float64(geral)
			if esperado < esperMin {
				esperMin = esperado
			}
			if esperado > 0 {
				d := float64(par.observado) - esperado
				x2 += d * d / esperado
			}
		}
	}

	gl := len(t.Valores) - 1
	return ResultadoQui2{
		Estatistica:      x2,
		GrausDeLiberdade: gl,
		PValor:           qui2Cauda(x2, gl),
		EsperadoMinimo:   esperMin,
	}, nil
}

// qui2Cauda devolve P(X2 > x) com gl graus de liberdade, que e a funcao gama
// incompleta complementar Q(gl/2, x/2).
func qui2Cauda(x float64, gl int) float64 {
	if x <= 0 {
		return 1
	}
	return gamaIncompletaQ(float64(gl)/2, x/2)
}

// gamaIncompletaQ devolve Q(a,x) = 1 - P(a,x), a gama incompleta regularizada
// complementar.
//
// Usa a serie para x < a+1 e a fracao continuada de Lentz caso contrario, que
// e a decomposicao classica: cada forma converge rapidamente no seu dominio e
// lentamente no do outro.
func gamaIncompletaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gamaSerie(a, x)
	}
	return gamaFracaoContinuada(a, x)
}

const (
	epsilonGama  = 3e-14
	minimoGama   = 1e-300
	maxIteracoes = 1000
)

// gamaSerie devolve P(a,x) pela serie.
func gamaSerie(a, x float64) float64 {
	lnGamaA, _ := math.Lgamma(a)

	ap := a
	soma := 1 / a
	termo := soma
	for n := 0; n < maxIteracoes; n++ {
		ap++
		termo *= x / ap
		soma += termo
		if math.Abs(termo) < math.Abs(soma)*epsilonGama {
			break
		}
	}
	return soma * math.Exp(-x+a*math.Log(x)-lnGamaA)
}

// gamaFracaoContinuada devolve Q(a,x) pela fracao continuada, avaliada pelo
// metodo de Lentz modificado.
func gamaFracaoContinuada(a, x float64) float64 {
	lnGamaA, _ := math.Lgamma(a)

	b := x + 1 - a
	c := 1 / minimoGama
	d := 1 / b
	h := d

	for i := 1; i <= maxIteracoes; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < minimoGama {
			d = minimoGama
		}
		c = b + an/c
		if math.Abs(c) < minimoGama {
			c = minimoGama
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < epsilonGama {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-lnGamaA) * h
}

// zNormal95 e o quantil 0,975 da normal padrao, para intervalos de 95%.
const zNormal95 = 1.959963984540054

// Wilson devolve o intervalo de confianca de 95% para uma proporcao, pelo
// metodo do escore de Wilson.
//
// Wilson e preferido ao intervalo normal simples porque permanece dentro de
// [0,1] e se comporta bem com proporcoes proximas dos extremos e com amostras
// pequenas — situacoes que aparecem quando um valor de atributo e raro na
// massa.
func Wilson(sucessos, total int) (inferior, superior float64) {
	if total == 0 {
		return 0, 0
	}

	n := float64(total)
	p := float64(sucessos) / n
	z2 := zNormal95 * zNormal95

	denominador := 1 + z2/n
	centro := (p + z2/(2*n)) / denominador
	margem := zNormal95 * math.Sqrt(p*(1-p)/n+z2/(4*n*n)) / denominador

	inferior = centro - margem
	superior = centro + margem
	if inferior < 0 {
		inferior = 0
	}
	if superior > 1 {
		superior = 1
	}
	return inferior, superior
}
