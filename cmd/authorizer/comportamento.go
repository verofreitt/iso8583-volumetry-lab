package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Distribuicao nomeia a forma da dispersao da latencia de servico.
type Distribuicao string

const (
	// Exponencial tem media igual ao jitter configurado. E a escolha natural
	// para tempo de servico sem memoria e produz cauda moderada.
	Exponencial Distribuicao = "exponencial"

	// Lognormal tem media igual ao jitter configurado e sigma fixo em 1, o
	// que implica mu = ln(jitter) - 1/2. Produz cauda mais pesada que a
	// exponencial para a mesma media, util para investigar percentis altos.
	Lognormal Distribuicao = "lognormal"
)

// sigmaLognormal e o desvio-padrao em escala logaritmica. Fica fixo para que a
// distribuicao seja descrita por um unico parametro, o jitter, e para que o
// artigo possa declarar a parametrizacao sem ambiguidade.
const sigmaLognormal = 1.0

// recusa associa um codigo de resposta a seu peso acumulado.
type recusa struct {
	codigo    string
	acumulado float64
}

// Comportamento decide a latencia de servico e o codigo de resposta de cada
// requisicao.
//
// A decisao e uma funcao pura de (semente, STAN): nao ha estado compartilhado,
// nao ha trava e o resultado independe da ordem em que as requisicoes chegam
// ou de qual goroutine as atende.
//
// Um gerador pseudoaleatorio compartilhado produziria uma sequencia
// determinada pela semente, mas o mapeamento entre valores sorteados e
// requisicoes dependeria do escalonador. A distribuicao agregada seria estavel
// e a rodada nao seria reproduzivel requisicao a requisicao, o que contraria a
// exigencia de reprodutibilidade bit a bit.
type Comportamento struct {
	base      time.Duration
	jitter    time.Duration
	dist      Distribuicao
	aprovacao float64
	recusas   []recusa
	vieses    []Vies
	semente   int64
	ecoApenas bool
}

// NovoComportamento valida os parametros e monta o comportamento do mock.
func NovoComportamento(c Config) (*Comportamento, error) {
	if c.LatenciaBase < 0 {
		return nil, fmt.Errorf("latency-base nao pode ser negativa, recebida %v", c.LatenciaBase)
	}
	if c.LatenciaJitter < 0 {
		return nil, fmt.Errorf("latency-jitter nao pode ser negativo, recebido %v", c.LatenciaJitter)
	}
	if c.TaxaAprovacao < 0 || c.TaxaAprovacao > 1 {
		return nil, fmt.Errorf("approval-rate deve estar entre 0 e 1, recebida %v", c.TaxaAprovacao)
	}

	dist := Distribuicao(c.LatenciaDist)
	if dist != Exponencial && dist != Lognormal {
		return nil, fmt.Errorf("latency-dist deve ser %q ou %q, recebido %q", Exponencial, Lognormal, c.LatenciaDist)
	}

	recusas, err := parsearRecusas(c.DistRecusas)
	if err != nil {
		return nil, err
	}

	vieses, err := parsearVieses(c.ViesRecusa)
	if err != nil {
		return nil, err
	}

	// qualquer caminho que possa recusar precisa de codigos para sortear
	podeRecusar := c.TaxaAprovacao < 1
	for _, v := range vieses {
		if v.Taxa > 0 {
			podeRecusar = true
		}
	}
	if podeRecusar && len(recusas) == 0 {
		return nil, fmt.Errorf("recusa possivel (approval-rate menor que 1 ou decline-bias com taxa positiva) exige decline-dist nao vazia")
	}

	return &Comportamento{
		base:      c.LatenciaBase,
		jitter:    c.LatenciaJitter,
		dist:      dist,
		aprovacao: c.TaxaAprovacao,
		recusas:   recusas,
		vieses:    vieses,
		semente:   c.Semente,
		ecoApenas: c.EcoApenas,
	}, nil
}

// Decidir devolve a latencia de servico a aplicar e o codigo do DE 39.
//
// ler e consultado apenas para os atributos que tem vies configurado. Sem
// vieses, nenhuma leitura ocorre e o caminho critico fica inalterado.
//
// A decisao continua sendo funcao pura de (semente, STAN, atributos da
// requisicao): o sorteio vem do STAN, e os atributos apenas escolhem qual taxa
// de recusa se aplica. Nao ha estado entre requisicoes.
func (c *Comportamento) Decidir(stan string, ler LeitorAtributo) (time.Duration, string) {
	if c.ecoApenas {
		return 0, aprovado
	}

	f := novaFonte(c.semente, stan)

	latencia := c.base
	if c.jitter > 0 {
		latencia += c.dispersao(&f)
	}

	return latencia, c.codigo(&f, ler)
}

// dispersao sorteia o acrescimo aleatorio a latencia base.
func (c *Comportamento) dispersao(f *fonte) time.Duration {
	media := float64(c.jitter)

	switch c.dist {
	case Lognormal:
		// mu escolhido para que a media da lognormal seja o jitter
		mu := math.Log(media) - sigmaLognormal*sigmaLognormal/2
		return time.Duration(math.Exp(mu + sigmaLognormal*f.normal()))
	default: // Exponencial
		// inversa da acumulada: -media * ln(1-u)
		return time.Duration(-media * math.Log(1-f.uniforme()))
	}
}

// codigo decide entre aprovacao e recusa, e qual recusa.
//
// O limiar e escrito como taxa de aprovacao efetiva, e nao como taxa de
// recusa, para que o mapeamento entre o valor sorteado e o desfecho seja
// identico ao de antes da introducao do vies: sem vies configurado, uma rodada
// reproduz exatamente os resultados anteriores.
func (c *Comportamento) codigo(f *fonte, ler LeitorAtributo) string {
	aprovacaoEfetiva := 1 - c.taxaDeRecusa(ler)

	if f.uniforme() < aprovacaoEfetiva {
		return aprovado
	}

	u := f.uniforme()
	for _, r := range c.recusas {
		if u < r.acumulado {
			return r.codigo
		}
	}
	// alcancavel apenas por erro de arredondamento no ultimo intervalo
	return c.recusas[len(c.recusas)-1].codigo
}

// parsearRecusas interpreta "51:40,05:30,14:20,91:10" como distribuicao de
// codigos de recusa, convertendo os pesos em fronteiras acumuladas.
//
// Os pesos sao normalizados, entao nao precisam somar 100.
func parsearRecusas(spec string) ([]recusa, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	type peso struct {
		codigo string
		valor  float64
	}

	var (
		pesos  []peso
		total  float64
		vistos = map[string]bool{}
	)

	for _, parte := range strings.Split(spec, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" {
			continue
		}

		codigo, valorTexto, ok := strings.Cut(parte, ":")
		if !ok {
			return nil, fmt.Errorf("entrada %q de decline-dist deve ter a forma codigo:peso", parte)
		}

		codigo = strings.TrimSpace(codigo)
		if len(codigo) != 2 {
			return nil, fmt.Errorf("codigo de recusa %q deve ter 2 caracteres: o DE 39 e an2", codigo)
		}
		if codigo == aprovado {
			return nil, fmt.Errorf("codigo %q e aprovacao e nao pode constar de decline-dist", aprovado)
		}
		if vistos[codigo] {
			return nil, fmt.Errorf("codigo de recusa %q repetido em decline-dist", codigo)
		}
		vistos[codigo] = true

		valor, err := strconv.ParseFloat(strings.TrimSpace(valorTexto), 64)
		if err != nil {
			return nil, fmt.Errorf("peso de %q em decline-dist: %w", codigo, err)
		}
		if valor <= 0 {
			return nil, fmt.Errorf("peso de %q deve ser positivo, recebido %v", codigo, valor)
		}

		pesos = append(pesos, peso{codigo: codigo, valor: valor})
		total += valor
	}

	if len(pesos) == 0 {
		return nil, nil
	}

	// ordem estavel: a fronteira acumulada precisa ser identica entre
	// execucoes para que a mesma semente produza os mesmos codigos.
	sort.Slice(pesos, func(i, j int) bool { return pesos[i].codigo < pesos[j].codigo })

	recusas := make([]recusa, 0, len(pesos))
	var acumulado float64
	for _, p := range pesos {
		acumulado += p.valor / total
		recusas = append(recusas, recusa{codigo: p.codigo, acumulado: acumulado})
	}
	// elimina o residuo de ponto flutuante na ultima fronteira
	recusas[len(recusas)-1].acumulado = 1

	return recusas, nil
}

// fonte e um gerador pseudoaleatorio determinístico derivado de uma semente e
// de um STAN. Usa splitmix64, que e rapido, nao tem estado global e tem
// qualidade suficiente para sorteios independentes por requisicao.
type fonte struct {
	estado uint64
}

func novaFonte(semente int64, stan string) fonte {
	// FNV-1a sobre o STAN, misturado com a semente
	const (
		fnvOffset = 14695981039346656037
		fnvPrime  = 1099511628211
	)
	h := uint64(fnvOffset)
	for i := 0; i < len(stan); i++ {
		h ^= uint64(stan[i])
		h *= fnvPrime
	}
	return fonte{estado: h ^ (uint64(semente) * 0x9E3779B97F4A7C15)}
}

func (f *fonte) proximo() uint64 {
	f.estado += 0x9E3779B97F4A7C15
	z := f.estado
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// uniforme devolve um valor em [0,1).
func (f *fonte) uniforme() float64 {
	return float64(f.proximo()>>11) / float64(uint64(1)<<53)
}

// normal devolve um valor da normal padrao, por Box-Muller.
func (f *fonte) normal() float64 {
	u1 := f.uniforme()
	// ln(0) e indefinido; o menor valor representavel evita o caso
	if u1 <= 0 {
		u1 = math.SmallestNonzeroFloat64
	}
	u2 := f.uniforme()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
