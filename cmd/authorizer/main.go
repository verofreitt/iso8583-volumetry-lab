// Command authorizer e o sistema sob teste do experimento: um autorizador mock
// que aceita requisicoes ISO 8583 0100 e responde 0110.
//
// Nao ha flags nem metricas, e o DE 39 e sempre "00". Latencia artificial,
// taxa de aprovacao, distribuicao dos codigos de recusa e teto de conexoes
// simultaneas entram nos passos seguintes.
package main

import (
	"errors"
	"io"
	"log"
	"net"
	"os"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

const (
	// endereco fixo de escuta. Injetor e autorizador rodam na mesma maquina,
	// via loopback, e nada e exposto para fora do host.
	endereco = "127.0.0.1:8583"

	// codigoAprovado e o DE 39 devolvido em toda resposta neste passo.
	codigoAprovado = "00"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	ln, err := net.Listen("tcp", endereco)
	if err != nil {
		log.Fatalf("escutando em %s: %v", endereco, err)
	}
	defer ln.Close()

	log.Printf("autorizador escutando em %s", ln.Addr())

	if err := serve(ln); err != nil {
		log.Fatalf("servindo: %v", err)
	}
}

// serve aceita conexoes concorrentes, cada uma atendida em sua propria
// goroutine.
//
// O atendimento em serie do passo 1 nao serve ao injetor em modelo aberto: com
// um pool de conexoes, apenas a primeira seria atendida e as demais ficariam
// paradas na fila de accept do sistema operacional. O injetor mediria essa
// espera como latencia do autorizador, que e exatamente o tipo de artefato de
// aparato que invalida o experimento.
//
// Nao ha teto de conexoes simultaneas ainda; ele entra no passo 5, como
// --max-conns, para permitir provocar saturacao de forma controlada.
func serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}

		log.Printf("conexao aceita de %s", conn.RemoteAddr())
		go func() {
			defer conn.Close()
			if err := handleConn(conn); err != nil {
				log.Printf("conexao %s encerrada com erro: %v", conn.RemoteAddr(), err)
				return
			}
			log.Printf("conexao %s encerrada", conn.RemoteAddr())
		}()
	}
}

// handleConn le frames da conexao ate o fim do fluxo, respondendo cada 0100
// com a 0110 correspondente.
func handleConn(conn net.Conn) error {
	for {
		requisicao, err := iso.ReadFrame(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		resposta, err := responder(requisicao)
		if err != nil {
			return err
		}

		if err := iso.WriteFrame(conn, resposta); err != nil {
			return err
		}
	}
}

// responder faz o parse de uma 0100 e devolve os bytes da 0110.
func responder(requisicao []byte) ([]byte, error) {
	req, err := iso.Parse(requisicao)
	if err != nil {
		return nil, err
	}

	resp, err := iso.BuildResponse(req, codigoAprovado)
	if err != nil {
		return nil, err
	}

	empacotada, err := resp.Pack()
	if err != nil {
		return nil, err
	}

	return empacotada, nil
}
