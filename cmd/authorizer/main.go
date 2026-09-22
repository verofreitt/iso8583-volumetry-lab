// Command authorizer e o sistema sob teste do experimento: um autorizador mock
// que aceita requisicoes ISO 8583 0100 e responde 0110.
//
// O mock precisa ser previsivel, nao realista. Se o comportamento dele for
// opaco, nao ha como atribuir uma variacao de latencia ao injetor ou ao
// autorizador. Nao ha logica de negocio, cache nem qualquer adaptacao dinamica
// ao volume: qualquer nao-linearidade no resultado precisa vir de contencao
// real de recursos, e nao de esperteza do mock.
//
// Toda decisao e deterministica dada a semente. Ver comportamento.go.
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

const (
	// endereco fixo de escuta. Injetor e autorizador rodam na mesma maquina,
	// via loopback, e nada e exposto para fora do host.
	endereco = "127.0.0.1:8583"

	// aprovado e o DE 39 de transacao autorizada.
	aprovado = "00"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	var (
		c          Config
		configOut  string
		silencioso bool
	)
	flag.DurationVar(&c.LatenciaBase, "latency-base", 0, "latencia de servico base")
	flag.DurationVar(&c.LatenciaJitter, "latency-jitter", 0, "media da dispersao somada a latencia base")
	flag.StringVar(&c.LatenciaDist, "latency-dist", string(Exponencial), "distribuicao da dispersao: exponencial ou lognormal")
	flag.Float64Var(&c.TaxaAprovacao, "approval-rate", 1, "proporcao de respostas 00, entre 0 e 1")
	flag.StringVar(&c.DistRecusas, "decline-dist", "51:40,05:30,14:20,91:10", "distribuicao dos codigos de recusa")
	flag.IntVar(&c.MaxConns, "max-conns", 0, "teto de requisicoes atendidas simultaneamente; 0 remove o teto")
	flag.Int64Var(&c.Semente, "seed", 1, "semente das decisoes do mock")
	flag.BoolVar(&c.EcoApenas, "echo-only", false, "responde imediatamente, sem latencia nem sorteio, para calibrar o injetor")
	flag.StringVar(&configOut, "config-out", "", "arquivo onde a configuracao e o ambiente sao gravados")
	flag.BoolVar(&silencioso, "quiet", false, "suprime o log por conexao")
	flag.Parse()

	comportamento, err := NovoComportamento(c)
	if err != nil {
		log.Fatalf("configuracao invalida: %v", err)
	}

	if configOut != "" {
		if err := escreverConfig(configOut, c); err != nil {
			log.Fatalf("%v", err)
		}
		log.Printf("configuracao gravada em %s", configOut)
	}

	ln, err := net.Listen("tcp", endereco)
	if err != nil {
		log.Fatalf("escutando em %s: %v", endereco, err)
	}
	defer ln.Close()

	s := &servidor{
		comportamento: comportamento,
		silencioso:    silencioso,
	}
	if c.MaxConns > 0 {
		s.vagas = make(chan struct{}, c.MaxConns)
	}

	if c.EcoApenas {
		log.Printf("autorizador em modo eco: sem latencia e sem sorteio")
	} else {
		log.Printf("autorizador: base %v, jitter %v (%s), aprovacao %.3f, recusas %q, semente %d",
			c.LatenciaBase, c.LatenciaJitter, c.LatenciaDist, c.TaxaAprovacao, c.DistRecusas, c.Semente)
	}
	if c.MaxConns > 0 {
		log.Printf("teto de %d requisicoes simultaneas", c.MaxConns)
	}
	log.Printf("escutando em %s", ln.Addr())

	if err := s.servir(ln); err != nil {
		log.Fatalf("servindo: %v", err)
	}
}

type servidor struct {
	comportamento *Comportamento
	silencioso    bool

	// vagas limita quantas requisicoes sao atendidas ao mesmo tempo. Nil
	// quando nao ha teto.
	//
	// O teto e aplicado por requisicao, e nao por conexao, ainda que a flag se
	// chame --max-conns. Com um cliente que mantem conexoes persistentes, um
	// teto por conexao seria uma funcao degrau: abaixo do numero de conexoes
	// do pool nao teria efeito algum, e acima dele as conexoes excedentes
	// ficariam paradas para sempre e todas as suas requisicoes expirariam. Nao
	// produziria curva de saturacao, que e o proposito declarado do parametro.
	//
	// Aplicado por requisicao, o teto enfileira de verdade e a espera aparece
	// na latencia medida.
	vagas chan struct{}
}

// servir aceita conexoes concorrentes, cada uma atendida em sua propria
// goroutine.
func (s *servidor) servir(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}

		if !s.silencioso {
			log.Printf("conexao aceita de %s", conn.RemoteAddr())
		}

		go func() {
			defer conn.Close()
			err := s.atender(conn)
			if s.silencioso {
				return
			}
			if err != nil {
				log.Printf("conexao %s encerrada com erro: %v", conn.RemoteAddr(), err)
				return
			}
			log.Printf("conexao %s encerrada", conn.RemoteAddr())
		}()
	}
}

// atender le frames da conexao ate o fim do fluxo, respondendo cada 0100 com a
// 0110 correspondente.
func (s *servidor) atender(conn net.Conn) error {
	for {
		requisicao, err := iso.ReadFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		resposta, err := s.responder(requisicao)
		if err != nil {
			return err
		}

		if err := iso.WriteFrame(conn, resposta); err != nil {
			return err
		}
	}
}

// responder faz o parse de uma 0100, aplica a latencia de servico e devolve os
// bytes da 0110.
func (s *servidor) responder(requisicao []byte) ([]byte, error) {
	req, err := iso.Parse(requisicao)
	if err != nil {
		return nil, err
	}

	if s.vagas != nil {
		s.vagas <- struct{}{}
		defer func() { <-s.vagas }()
	}

	// o STAN e a chave de decisao: torna a resposta uma funcao pura da semente
	// e da requisicao, reproduzivel entre rodadas
	stan, err := req.GetString(11)
	if err != nil {
		return nil, err
	}

	latencia, de39 := s.comportamento.Decidir(stan)
	if latencia > 0 {
		time.Sleep(latencia)
	}

	resp, err := iso.BuildResponse(req, de39)
	if err != nil {
		return nil, err
	}

	return resp.Pack()
}
