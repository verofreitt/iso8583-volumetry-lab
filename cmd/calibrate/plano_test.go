package main

import (
	"fmt"
	"testing"
)

func TestPlanejarCobreTodasAsRodadas(t *testing.T) {
	niveis := []float64{100, 500, 1000}
	const repeticoes = 4

	for _, embaralhar := range []bool{false, true} {
		t.Run(fmt.Sprintf("embaralhar=%v", embaralhar), func(t *testing.T) {
			plano := planejar(niveis, repeticoes, embaralhar, 1)

			if len(plano) != len(niveis)*repeticoes {
				t.Fatalf("%d execucoes, esperado %d", len(plano), len(niveis)*repeticoes)
			}

			vistas := map[Execucao]int{}
			for _, e := range plano {
				vistas[e]++
			}
			for _, tps := range niveis {
				for rep := 1; rep <= repeticoes; rep++ {
					e := Execucao{TPS: tps, Repeticao: rep}
					if vistas[e] != 1 {
						t.Errorf("%g TPS repeticao %d apareceu %d vezes, esperado 1", tps, rep, vistas[e])
					}
				}
			}
		})
	}
}

func TestPlanejarSequencialPreservaAOrdem(t *testing.T) {
	plano := planejar([]float64{100, 500}, 2, false, 1)

	esperado := []Execucao{
		{TPS: 100, Repeticao: 1}, {TPS: 100, Repeticao: 2},
		{TPS: 500, Repeticao: 1}, {TPS: 500, Repeticao: 2},
	}
	for i := range esperado {
		if plano[i] != esperado[i] {
			t.Errorf("posicao %d = %+v, esperado %+v", i, plano[i], esperado[i])
		}
	}
}

// TestPlanejarEmbaralhaDeFato protege o proposito do sorteio: quebrar o
// confundimento entre nivel de carga e posicao na varredura.
//
// Sem sorteio, todas as repeticoes de um nivel ficam consecutivas, e qualquer
// deriva da maquina ao longo da varredura aparece como se fosse efeito do
// nivel.
func TestPlanejarEmbaralhaDeFato(t *testing.T) {
	niveis := []float64{100, 250, 500, 1000, 1500, 2000}
	plano := planejar(niveis, 5, true, 1)

	sequencial := planejar(niveis, 5, false, 1)
	iguais := 0
	for i := range plano {
		if plano[i] == sequencial[i] {
			iguais++
		}
	}
	if iguais == len(plano) {
		t.Fatal("a ordem sorteada saiu identica a sequencial")
	}

	// nenhum nivel deve ter todas as suas repeticoes consecutivas
	for _, tps := range niveis {
		var posicoes []int
		for i, e := range plano {
			if e.TPS == tps {
				posicoes = append(posicoes, i)
			}
		}
		consecutivas := true
		for i := 1; i < len(posicoes); i++ {
			if posicoes[i] != posicoes[i-1]+1 {
				consecutivas = false
				break
			}
		}
		if consecutivas {
			t.Errorf("as repeticoes de %g TPS sairam consecutivas em %v", tps, posicoes)
		}
	}
}

// TestPlanejarEhDeterministico: a mesma semente precisa produzir a mesma
// ordem, senao a varredura nao e reproduzivel.
func TestPlanejarEhDeterministico(t *testing.T) {
	niveis := []float64{100, 250, 500, 1000}

	a := planejar(niveis, 5, true, 42)
	b := planejar(niveis, 5, true, 42)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("posicao %d: %+v na primeira chamada, %+v na segunda", i, a[i], b[i])
		}
	}

	c := planejar(niveis, 5, true, 43)
	iguais := 0
	for i := range a {
		if a[i] == c[i] {
			iguais++
		}
	}
	if iguais == len(a) {
		t.Error("sementes diferentes produziram a mesma ordem")
	}
}
