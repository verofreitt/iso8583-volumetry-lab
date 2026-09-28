package main

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func configPadrao() Config {
	return Config{
		LatenciaDist:  string(Exponencial),
		TaxaAprovacao: 1,
		DistRecusas:   "51:40,05:30,14:20,91:10",
		Semente:       1,
	}
}

func novo(t *testing.T, ajustar func(*Config)) *Comportamento {
	t.Helper()
	c := configPadrao()
	if ajustar != nil {
		ajustar(&c)
	}
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}
	return comp
}

// --- validacao ---

func TestNovoComportamentoRejeitaConfigInvalida(t *testing.T) {
	casos := map[string]func(*Config){
		"latencia base negativa":    func(c *Config) { c.LatenciaBase = -time.Millisecond },
		"jitter negativo":           func(c *Config) { c.LatenciaJitter = -time.Millisecond },
		"aprovacao acima de 1":      func(c *Config) { c.TaxaAprovacao = 1.5 },
		"aprovacao negativa":        func(c *Config) { c.TaxaAprovacao = -0.1 },
		"distribuicao desconhecida": func(c *Config) { c.LatenciaDist = "normal" },
		"recusa sem peso":           func(c *Config) { c.DistRecusas = "51" },
		"codigo de 3 caracteres":    func(c *Config) { c.DistRecusas = "510:40" },
		"codigo de aprovacao":       func(c *Config) { c.DistRecusas = "00:40" },
		"codigo repetido":           func(c *Config) { c.DistRecusas = "51:40,51:30" },
		"peso zero":                 func(c *Config) { c.DistRecusas = "51:0" },
		"peso negativo":             func(c *Config) { c.DistRecusas = "51:-10" },
		"peso nao numerico":         func(c *Config) { c.DistRecusas = "51:muito" },
		"recusa sem distribuicao":   func(c *Config) { c.TaxaAprovacao = 0.5; c.DistRecusas = "" },
	}
	for nome, ajustar := range casos {
		t.Run(nome, func(t *testing.T) {
			c := configPadrao()
			ajustar(&c)
			if _, err := NovoComportamento(c); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

func TestParsearRecusasNormalizaPesos(t *testing.T) {
	// pesos que nao somam 100 devem ser normalizados
	recusas, err := parsearRecusas("51:1,05:1")
	if err != nil {
		t.Fatalf("parsearRecusas: %v", err)
	}
	if len(recusas) != 2 {
		t.Fatalf("%d recusas, esperado 2", len(recusas))
	}
	// ordem estavel por codigo: 05 antes de 51
	if recusas[0].codigo != "05" || recusas[1].codigo != "51" {
		t.Errorf("ordem instavel: %v", recusas)
	}
	if math.Abs(recusas[0].acumulado-0.5) > 1e-9 {
		t.Errorf("primeira fronteira = %v, esperado 0,5", recusas[0].acumulado)
	}
	if recusas[1].acumulado != 1 {
		t.Errorf("ultima fronteira = %v, esperado exatamente 1", recusas[1].acumulado)
	}
}

// --- determinismo ---

// TestDecidirEhDeterministico e o teste central do passo 5. A mesma semente e
// o mesmo STAN precisam produzir sempre a mesma decisao, independentemente da
// ordem de chamada ou de quantas outras requisicoes foram atendidas antes.
func TestDecidirEhDeterministico(t *testing.T) {
	c := novo(t, func(c *Config) {
		c.LatenciaBase = 5 * time.Millisecond
		c.LatenciaJitter = 2 * time.Millisecond
		c.TaxaAprovacao = 0.7
	})

	stans := []string{"000001", "000042", "123456", "999999"}

	primeira := map[string]struct {
		lat  time.Duration
		de39 string
	}{}
	for _, s := range stans {
		lat, de39 := c.Decidir(s, nil)
		primeira[s] = struct {
			lat  time.Duration
			de39 string
		}{lat, de39}
	}

	// repete em ordem inversa e intercalada: o resultado nao pode mudar
	for i := len(stans) - 1; i >= 0; i-- {
		s := stans[i]
		c.Decidir("999998", nil) // chamada intercalada, para embaralhar qualquer estado
		lat, de39 := c.Decidir(s, nil)
		if lat != primeira[s].lat || de39 != primeira[s].de39 {
			t.Errorf("STAN %s: (%v, %s) na segunda chamada, (%v, %s) na primeira",
				s, lat, de39, primeira[s].lat, primeira[s].de39)
		}
	}
}

// TestDecidirIndependeDaConcorrencia confirma que a decisao nao depende da
// goroutine que a executa.
func TestDecidirIndependeDaConcorrencia(t *testing.T) {
	c := novo(t, func(c *Config) {
		c.LatenciaJitter = time.Millisecond
		c.TaxaAprovacao = 0.5
	})

	const stan = "000123"
	esperadoLat, esperadoDE39 := c.Decidir(stan, nil)

	resultados := make(chan string, 64)
	for i := 0; i < 64; i++ {
		go func() {
			lat, de39 := c.Decidir(stan, nil)
			resultados <- fmt.Sprintf("%v/%s", lat, de39)
		}()
	}

	esperado := fmt.Sprintf("%v/%s", esperadoLat, esperadoDE39)
	for i := 0; i < 64; i++ {
		if got := <-resultados; got != esperado {
			t.Fatalf("decisao concorrente = %s, esperado %s", got, esperado)
		}
	}
}

// TestSementesDiferentesMudamAsDecisoes confirma que a semente de fato governa
// o sorteio.
func TestSementesDiferentesMudamAsDecisoes(t *testing.T) {
	a := novo(t, func(c *Config) { c.TaxaAprovacao = 0.5; c.Semente = 1 })
	b := novo(t, func(c *Config) { c.TaxaAprovacao = 0.5; c.Semente = 2 })

	diferencas := 0
	for i := 0; i < 1000; i++ {
		stan := fmt.Sprintf("%06d", i)
		_, da := a.Decidir(stan, nil)
		_, db := b.Decidir(stan, nil)
		if da != db {
			diferencas++
		}
	}
	if diferencas < 100 {
		t.Errorf("apenas %d decisoes diferentes em 1000 entre sementes distintas", diferencas)
	}
}

// --- taxa de aprovacao ---

func TestTaxaDeAprovacao(t *testing.T) {
	casos := []float64{0, 0.25, 0.5, 0.9, 1}
	const amostras = 20000

	for _, taxa := range casos {
		t.Run(fmt.Sprintf("%.2f", taxa), func(t *testing.T) {
			c := novo(t, func(c *Config) { c.TaxaAprovacao = taxa })

			aprovadas := 0
			for i := 0; i < amostras; i++ {
				if _, de39 := c.Decidir(fmt.Sprintf("%06d", i), nil); de39 == aprovado {
					aprovadas++
				}
			}

			obtida := float64(aprovadas) / amostras
			if math.Abs(obtida-taxa) > 0.02 {
				t.Errorf("taxa de aprovacao = %.4f, esperada %.2f", obtida, taxa)
			}
		})
	}
}

// TestDistribuicaoDeRecusas confirma que os pesos informados sao respeitados.
func TestDistribuicaoDeRecusas(t *testing.T) {
	c := novo(t, func(c *Config) {
		c.TaxaAprovacao = 0
		c.DistRecusas = "51:40,05:30,14:20,91:10"
	})

	const amostras = 40000
	contagem := map[string]int{}
	for i := 0; i < amostras; i++ {
		_, de39 := c.Decidir(fmt.Sprintf("%06d", i), nil)
		contagem[de39]++
	}

	esperado := map[string]float64{"51": 0.40, "05": 0.30, "14": 0.20, "91": 0.10}
	for codigo, proporcao := range esperado {
		obtida := float64(contagem[codigo]) / amostras
		if math.Abs(obtida-proporcao) > 0.02 {
			t.Errorf("codigo %s: %.4f, esperado %.2f", codigo, obtida, proporcao)
		}
	}
	if contagem[aprovado] != 0 {
		t.Errorf("%d aprovacoes com approval-rate 0", contagem[aprovado])
	}
}

// --- latencia ---

func TestLatenciaBaseSemJitter(t *testing.T) {
	c := novo(t, func(c *Config) { c.LatenciaBase = 7 * time.Millisecond })

	for i := 0; i < 100; i++ {
		lat, _ := c.Decidir(fmt.Sprintf("%06d", i), nil)
		if lat != 7*time.Millisecond {
			t.Fatalf("latencia = %v, esperado 7ms sem jitter", lat)
		}
	}
}

// TestDistribuicoesTemMediaIgualAoJitter fixa a parametrizacao declarada no
// artigo: nas duas distribuicoes, a media da dispersao e o valor de
// --latency-jitter.
func TestDistribuicoesTemMediaIgualAoJitter(t *testing.T) {
	const (
		base       = 10 * time.Millisecond
		jitter     = 5 * time.Millisecond
		amostras   = 50000
		tolerancia = 0.05 // 5%
	)

	for _, dist := range []Distribuicao{Exponencial, Lognormal} {
		t.Run(string(dist), func(t *testing.T) {
			c := novo(t, func(c *Config) {
				c.LatenciaBase = base
				c.LatenciaJitter = jitter
				c.LatenciaDist = string(dist)
			})

			var soma float64
			minimo := time.Duration(math.MaxInt64)
			for i := 0; i < amostras; i++ {
				lat, _ := c.Decidir(fmt.Sprintf("%06d", i), nil)
				if lat < minimo {
					minimo = lat
				}
				soma += float64(lat - base)
			}

			media := time.Duration(soma / amostras)
			desvio := math.Abs(float64(media-jitter)) / float64(jitter)
			if desvio > tolerancia {
				t.Errorf("media da dispersao = %v, esperado %v (desvio de %.1f%%)", media, jitter, 100*desvio)
			}
			if minimo < base {
				t.Errorf("latencia minima = %v, abaixo da base de %v: a dispersao deve ser sempre positiva", minimo, base)
			}
		})
	}
}

// TestLognormalTemCaudaMaisPesada confirma a razao de oferecer as duas
// distribuicoes: para a mesma media, a lognormal investiga percentis mais
// altos.
func TestLognormalTemCaudaMaisPesada(t *testing.T) {
	const amostras = 50000

	maximo := map[Distribuicao]time.Duration{}
	for _, dist := range []Distribuicao{Exponencial, Lognormal} {
		c := novo(t, func(c *Config) {
			c.LatenciaJitter = 5 * time.Millisecond
			c.LatenciaDist = string(dist)
		})
		for i := 0; i < amostras; i++ {
			if lat, _ := c.Decidir(fmt.Sprintf("%06d", i), nil); lat > maximo[dist] {
				maximo[dist] = lat
			}
		}
	}

	t.Logf("maximo em %d amostras: exponencial %v, lognormal %v",
		amostras, maximo[Exponencial], maximo[Lognormal])

	if maximo[Lognormal] <= maximo[Exponencial] {
		t.Errorf("maximo da lognormal (%v) deveria superar o da exponencial (%v)",
			maximo[Lognormal], maximo[Exponencial])
	}
}

// --- modo eco ---

// TestEcoApenasIgnoraTodoORestante cobre o alvo trivial usado para calibrar o
// injetor: sem latencia, sem sorteio e sempre aprovado.
func TestEcoApenasIgnoraTodoORestante(t *testing.T) {
	c := novo(t, func(c *Config) {
		c.EcoApenas = true
		c.LatenciaBase = time.Second
		c.LatenciaJitter = time.Second
		c.TaxaAprovacao = 0
	})

	for i := 0; i < 100; i++ {
		lat, de39 := c.Decidir(fmt.Sprintf("%06d", i), nil)
		if lat != 0 {
			t.Fatalf("latencia = %v em modo eco, esperado 0", lat)
		}
		if de39 != aprovado {
			t.Fatalf("DE 39 = %s em modo eco, esperado %s", de39, aprovado)
		}
	}
}
