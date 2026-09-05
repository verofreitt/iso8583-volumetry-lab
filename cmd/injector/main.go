// Command injector e o gerador de carga do experimento.
//
// Este e o passo 3 da ordem de execucao descrita em CLAUDE.md: controle de
// taxa em modelo aberto. As chegadas sao agendadas pelo relogio,
// independentemente das conclusoes, e cada envio ocorre em sua propria
// goroutine.
//
// Ainda nao ha coleta de latencia com histograma, warm-up, semente, leitura de
// massa nem escrita de raw.csv e summary.json: esses sao o passo 4. Os numeros
// impressos aqui servem para verificar que a taxa pretendida esta sendo
// aplicada, e nao como resultado de experimento.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	moov "github.com/moov-io/iso8583"
	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
	"github.com/verofreitt/iso8583-volumetry-lab/internal/ratelimit"
)

const (
	// endereco do autorizador. Mesma maquina, via loopback.
	endereco = "127.0.0.1:8583"

	// prazo maximo de uma troca individual. Sem prazo, uma requisicao sem
	// resposta seguraria uma conexao do pool ate o fim da rodada.
	prazo = 5 * time.Second
)

// contadores acompanha o desfecho de cada requisicao.
//
// Recusas de negocio e falhas de transporte sao contadas separadamente. Uma
// recusa e uma resposta valida do autorizador; um timeout e falha de
// desempenho. Somar os dois invalidaria a analise.
type contadores struct {
	aprovadas atomic.Int64
	recusadas atomic.Int64
	erros     atomic.Int64
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	tps := flag.Float64("tps", 10, "taxa de chegada pretendida, em transacoes por segundo")
	duracao := flag.Duration("duration", 10*time.Second, "duracao da rodada")
	conexoes := flag.Int("conns", 8, "conexoes persistentes mantidas com o autorizador")
	flag.Parse()

	agendador, err := ratelimit.NovoAberto(*tps, *duracao)
	if err != nil {
		log.Fatalf("configurando o agendador: %v", err)
	}

	p, err := novoPool(endereco, *conexoes, prazo)
	if err != nil {
		log.Fatalf("abrindo o pool: %v", err)
	}
	defer p.fechar()

	log.Printf("%d conexoes abertas com %s", *conexoes, endereco)
	log.Printf("alvo de %g TPS por %v (%d chegadas previstas)", *tps, *duracao, agendador.Chegadas())

	ctx, parar := signal.NotifyContext(context.Background(), os.Interrupt)
	defer parar()

	var c contadores
	res := agendador.Executar(ctx, func(ch ratelimit.Chegada) {
		if err := requisitar(ctx, p, ch, &c); err != nil {
			c.erros.Add(1)
		}
	})

	relatar(os.Stdout, *tps, *duracao, *conexoes, res, &c, p.perdidasTotal())
}

// requisitar executa uma troca completa para uma chegada agendada.
func requisitar(ctx context.Context, p *pool, ch ratelimit.Chegada, c *contadores) error {
	conn, err := p.adquirir(ctx)
	if err != nil {
		return err
	}

	resp, err := trocar(conn, requisicao(ch))
	if err != nil {
		// o fluxo pode ter ficado dessincronizado: a conexao nao volta ao pool
		p.descartar(conn)
		return err
	}
	p.devolver(conn)

	de39, err := resp.GetString(39)
	if err != nil {
		return fmt.Errorf("lendo DE 39: %w", err)
	}

	if de39 == "00" {
		c.aprovadas.Add(1)
	} else {
		c.recusadas.Add(1)
	}
	return nil
}

// requisicao monta a 0100 de uma chegada.
//
// O STAN deriva do indice da chegada, o que garante unicidade dentro da rodada
// e permite conferir a correlacao entre requisicao e resposta. O campo tem
// seis digitos, entao a numeracao reinicia a cada milhao de requisicoes; uma
// rodada mais longa que isso precisaria de outra chave de correlacao, e o
// limite esta registrado em docs/experimento.md.
//
// Os demais valores sao fixos. A variacao da massa entra no passo 4, com a
// leitura dos CSVs de entrada e a semente que define a ordem de consumo.
func requisicao(ch ratelimit.Chegada) iso.Requisicao {
	return iso.Requisicao{
		PAN:                   "9999990000000014",
		ProcessingCode:        "000000",
		Valor:                 "000000010000",
		STAN:                  fmt.Sprintf("%06d", ch.Indice%1000000),
		MCC:                   "5411",
		POSEntryMode:          "021",
		InstituicaoAdquirente: "000001",
		RRN:                   "000000000001",
		TerminalID:            "TERM0001",
		Moeda:                 "986",
		Instante:              ch.Agendado,
	}
}

// trocar envia uma 0100 e devolve a 0110 recebida, conferindo a correlacao
// pelo STAN.
func trocar(conn net.Conn, req iso.Requisicao) (*moov.Message, error) {
	if err := conn.SetDeadline(time.Now().Add(prazo)); err != nil {
		return nil, fmt.Errorf("definindo prazo: %w", err)
	}

	empacotada, err := req.Pack()
	if err != nil {
		return nil, err
	}

	if err := iso.WriteFrame(conn, empacotada); err != nil {
		return nil, err
	}

	bruta, err := iso.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("lendo a resposta: %w", err)
	}

	resp, err := iso.Parse(bruta)
	if err != nil {
		return nil, err
	}

	stan, err := resp.GetString(11)
	if err != nil {
		return nil, fmt.Errorf("lendo DE 11: %w", err)
	}
	if stan != req.STAN {
		return nil, fmt.Errorf("STAN da resposta %q difere do enviado %q", stan, req.STAN)
	}

	return resp, nil
}

// relatar escreve o resumo da rodada.
//
// A vazao alcancada e calculada sobre a janela de chegadas, e nao sobre o
// tempo decorrido ate a ultima resposta. As N chegadas de uma rodada ocupam os
// instantes 0, 1/TPS, ..., (N-1)/TPS, ou seja, uma janela que termina um
// intervalo antes do fim da rodada. Dividir pelo tempo decorrido produziria
// vazao acima de 100% do alvo, o que e impossivel em modelo aberto: nenhuma
// resposta pode ser contada antes de sua chegada ter sido agendada.
//
// Com a janela de chegadas como denominador, a vazao so fica abaixo do alvo
// quando alguma requisicao deixou de ser respondida, que e exatamente o sinal
// de saturacao que a metrica deve capturar.
func relatar(w io.Writer, tps float64, duracao time.Duration, conexoes int,
	res ratelimit.Resultado, c *contadores, perdidas int) {

	aprovadas := c.aprovadas.Load()
	recusadas := c.recusadas.Load()
	erros := c.erros.Load()
	respondidas := aprovadas + recusadas

	janela := time.Duration(float64(res.Chegadas) / tps * float64(time.Second))
	decorrido := res.Fim.Sub(res.Inicio)

	var alcancado float64
	if janela > 0 {
		alcancado = float64(respondidas) / janela.Seconds()
	}

	fmt.Fprintf(w, "alvo          : %g TPS por %v, %d conexoes\n", tps, duracao, conexoes)
	fmt.Fprintf(w, "chegadas      : %d (janela de %v)\n", res.Chegadas, janela.Round(time.Millisecond))
	fmt.Fprintf(w, "respondidas   : %d (aprovadas %d, recusadas %d)\n", respondidas, aprovadas, recusadas)
	fmt.Fprintf(w, "erros         : %d\n", erros)
	if perdidas > 0 {
		fmt.Fprintf(w, "conexoes perdidas: %d\n", perdidas)
	}
	fmt.Fprintf(w, "vazao         : %.2f TPS (%.1f%% do alvo)\n", alcancado, 100*alcancado/tps)
	fmt.Fprintf(w, "tempo total   : %v (ate a ultima resposta)\n", decorrido.Round(time.Millisecond))
	fmt.Fprintf(w, "atraso medio  : %v (agendamento do injetor)\n", res.AtrasoMedio.Round(time.Microsecond))
	fmt.Fprintf(w, "atraso maximo : %v (agendamento do injetor)\n", res.AtrasoMaximo.Round(time.Microsecond))

	// o criterio de saturacao usa o atraso medio, nao o maximo. Um unico
	// sobressalto do temporizador ou uma pausa do coletor de lixo eleva o
	// maximo sem que o injetor tenha deixado de sustentar a taxa; ja um atraso
	// medio da ordem do intervalo entre chegadas significa atraso sistematico.
	intervalo := time.Duration(float64(time.Second) / tps)
	if res.AtrasoMedio > intervalo {
		fmt.Fprintf(w, "\nAVISO: o atraso medio de agendamento (%v) excedeu o intervalo entre\n", res.AtrasoMedio.Round(time.Microsecond))
		fmt.Fprintf(w, "chegadas (%v). O injetor nao sustentou a taxa pretendida; esta rodada\n", intervalo.Round(time.Microsecond))
		fmt.Fprintf(w, "mede o injetor, nao o autorizador.\n")
	}
}
