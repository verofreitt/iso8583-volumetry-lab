// Package ratelimit implementa o controle de taxa do injetor em modelo aberto.
//
// Em modelo aberto as chegadas sao independentes das conclusoes: o instante em
// que uma requisicao deve partir e determinado apenas pelo relogio, nunca pelo
// termino da requisicao anterior. O contraste e com o modelo fechado, em que o
// laco aguarda cada resposta antes de emitir a proxima requisicao:
//
//	// ERRADO - modelo fechado
//	for {
//	    inicio := time.Now()
//	    enviar()
//	    aguardarResposta()
//	    registrar(time.Since(inicio))
//	}
//
// Num laco fechado uma resposta lenta atrasa a proxima requisicao, o sistema
// nunca e submetido a taxa pretendida e a cauda da distribuicao desaparece da
// medicao. A diferenca de comportamento entre os dois modelos esta documentada
// em Schroeder, Wierman & Harchol-Balter (2006), Open Versus Closed: A
// Cautionary Tale, NSDI'06.
package ratelimit

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// Chegada descreve uma chegada agendada.
type Chegada struct {
	// Indice e a ordem da chegada, a partir de zero.
	Indice int

	// Agendado e o instante de chegada pretendido, calculado a partir do
	// inicio da rodada e da taxa. E deste instante, e nao do momento em que o
	// envio de fato ocorreu, que a latencia deve ser medida: se o injetor
	// atrasar por contencao interna, esse atraso pertence a latencia observada
	// pelo cliente e precisa aparecer no numero. Ver secao 5.2 do CLAUDE.md.
	Agendado time.Time
}

// Resultado resume a execucao de uma rodada do ponto de vista do agendador.
type Resultado struct {
	// Inicio e o instante da primeira chegada agendada.
	Inicio time.Time

	// Fim e o instante em que a ultima chamada em voo terminou.
	Fim time.Time

	// Chegadas e a quantidade de chegadas disparadas.
	Chegadas int

	// AtrasoMaximo e a maior diferenca observada entre o instante em que uma
	// chegada foi disparada e o instante em que ela deveria ter ocorrido.
	//
	// E uma medida de pior caso: uma unica pausa do coletor de lixo ou um
	// sobressalto na resolucao do temporizador do sistema operacional basta
	// para eleva-la. Sozinha, nao indica que o injetor deixou de sustentar a
	// taxa.
	AtrasoMaximo time.Duration

	// AtrasoMedio e o atraso medio de agendamento sobre todas as chegadas.
	//
	// E este, e nao o maximo, o diagnostico de saturacao do proprio injetor:
	// um atraso medio da ordem do intervalo entre chegadas significa que o
	// agendador ficou sistematicamente para tras, e a rodada passa a medir o
	// gerador de carga em vez do sistema sob teste.
	AtrasoMedio time.Duration
}

// Aberto dispara chegadas a uma taxa fixa, em modelo aberto.
type Aberto struct {
	tps     float64
	duracao time.Duration
}

// NovoAberto cria um agendador para a taxa e a duracao informadas.
func NovoAberto(tps float64, duracao time.Duration) (*Aberto, error) {
	if !(tps > 0) || math.IsInf(tps, 0) {
		return nil, fmt.Errorf("tps deve ser positivo e finito, recebido %v", tps)
	}
	if duracao <= 0 {
		return nil, fmt.Errorf("duracao deve ser positiva, recebida %v", duracao)
	}
	return &Aberto{tps: tps, duracao: duracao}, nil
}

// Chegadas devolve quantas chegadas serao disparadas em uma rodada completa.
func (a *Aberto) Chegadas() int {
	return int(math.Ceil(a.duracao.Seconds() * a.tps))
}

// deslocamento devolve o instante de chegada da i-esima requisicao em relacao
// ao inicio da rodada.
//
// O calculo parte sempre do indice, e nunca da acumulacao de um intervalo
// truncado. Somar repetidamente um intervalo arredondado para nanossegundos
// faria a taxa efetiva derivar ao longo da rodada, e a taxa declarada no artigo
// deixaria de corresponder a taxa aplicada.
func (a *Aberto) deslocamento(i int) time.Duration {
	return time.Duration(math.Round(float64(i) / a.tps * float64(time.Second)))
}

// Executar dispara as chegadas da rodada, cada uma em sua propria goroutine, e
// aguarda o termino de todas antes de retornar.
//
// Nenhum limite e imposto a quantidade de chamadas simultaneas. Isso e
// deliberado: um teto transformaria o modelo aberto em fechado assim que fosse
// atingido, escondendo justamente a saturacao que o experimento pretende
// medir. Em compensacao, se o sistema sob teste parar de responder, o consumo
// de memoria do injetor cresce com a taxa; a contencao de recursos precisa
// aparecer nos numeros, e nao ser mascarada.
//
// Cancelar ctx interrompe o agendamento de novas chegadas, mas as chamadas ja
// em voo sao aguardadas.
func (a *Aberto) Executar(ctx context.Context, fn func(Chegada)) Resultado {
	inicio := time.Now()

	// os atrasos sao calculados no laco de agendamento, que e uma unica
	// goroutine: nao ha concorrencia sobre estes contadores.
	var (
		wg          sync.WaitGroup
		atrasoMax   time.Duration
		atrasoTotal time.Duration
		chegadas    int
	)

	temporizador := time.NewTimer(time.Hour)
	if !temporizador.Stop() {
		<-temporizador.C
	}
	defer temporizador.Stop()

	for i := 0; ; i++ {
		deslocamento := a.deslocamento(i)
		if deslocamento >= a.duracao {
			break
		}

		agendado := inicio.Add(deslocamento)
		if !aguardarAte(ctx, temporizador, agendado) {
			break
		}

		// o atraso e medido antes de disparar a goroutine: mede o quanto o
		// agendador se desviou do plano, nao o custo da chamada.
		if atraso := time.Since(agendado); atraso > 0 {
			atrasoTotal += atraso
			if atraso > atrasoMax {
				atrasoMax = atraso
			}
		}

		chegadas++
		wg.Add(1)
		go func(c Chegada) {
			defer wg.Done()
			fn(c)
		}(Chegada{Indice: i, Agendado: agendado})
	}

	wg.Wait()

	var atrasoMedio time.Duration
	if chegadas > 0 {
		atrasoMedio = atrasoTotal / time.Duration(chegadas)
	}

	return Resultado{
		Inicio:       inicio,
		Fim:          time.Now(),
		Chegadas:     chegadas,
		AtrasoMaximo: atrasoMax,
		AtrasoMedio:  atrasoMedio,
	}
}

// aguardarAte espera ate o instante informado. Devolve false se ctx foi
// cancelado. Se o instante ja passou, retorna imediatamente sem dormir: a
// chegada e disparada com atraso, e o atraso e contabilizado.
func aguardarAte(ctx context.Context, temporizador *time.Timer, instante time.Time) bool {
	espera := time.Until(instante)
	if espera <= 0 {
		return ctx.Err() == nil
	}

	temporizador.Reset(espera)
	select {
	case <-temporizador.C:
		return true
	case <-ctx.Done():
		if !temporizador.Stop() {
			<-temporizador.C
		}
		return false
	}
}
