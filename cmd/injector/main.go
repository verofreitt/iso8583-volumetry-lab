// Command injector e o gerador de carga do experimento.
//
// Este e o passo 2 da ordem de execucao descrita em CLAUDE.md: uma unica
// requisicao 0100 enviada ao autorizador, com leitura da 0110 correspondente,
// para fechar o caminho ponta a ponta. Nao ha controle de taxa, coleta de
// latencia nem leitura de massa: esses sao os passos 3, 4 e adiante.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	moov "github.com/moov-io/iso8583"
	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

const (
	// endereco do autorizador. Mesma maquina, via loopback.
	endereco = "127.0.0.1:8583"

	// prazo maximo para a troca de mensagens. Sem prazo, uma falha do
	// autorizador deixaria o injetor bloqueado indefinidamente em vez de
	// reportar erro.
	prazo = 5 * time.Second
)

// requisicaoFixa e a 0100 canonica descrita no README. Os valores sao
// sinteticos: o PAN e valido por Luhn e comeca por 9, faixa que o ISO/IEC 7812
// reserva para atribuicao nacional e que nao e alocada a nenhum esquema
// internacional de cartoes.
func requisicaoFixa(instante time.Time) iso.Requisicao {
	return iso.Requisicao{
		PAN:                   "9999990000000014",
		ProcessingCode:        "000000",
		Valor:                 "000000010000",
		STAN:                  "000001",
		MCC:                   "5411",
		POSEntryMode:          "021",
		InstituicaoAdquirente: "000001",
		RRN:                   "000000000001",
		TerminalID:            "TERM0001",
		Moeda:                 "986",
		Instante:              instante,
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	conn, err := net.DialTimeout("tcp", endereco, prazo)
	if err != nil {
		log.Fatalf("conectando em %s: %v", endereco, err)
	}
	defer conn.Close()

	log.Printf("conectado a %s", conn.RemoteAddr())

	req := requisicaoFixa(time.Now())

	inicio := time.Now()
	resp, err := trocar(conn, req)
	decorrido := time.Since(inicio)
	if err != nil {
		log.Fatalf("trocando mensagens: %v", err)
	}

	if err := relatar(os.Stdout, req, resp, decorrido); err != nil {
		log.Fatalf("lendo a resposta: %v", err)
	}
}

// trocar envia uma 0100 e devolve a 0110 recebida.
//
// O tempo medido aqui e apenas informativo. A medicao que sustenta o artigo
// depende do modelo aberto e da correcao da omissao coordenada, ambos ainda
// nao implementados; ate la nenhum numero produzido por este binario deve ser
// tratado como resultado.
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

	return iso.Parse(bruta)
}

// relatar escreve o resultado da troca e confirma a correlacao pelo STAN.
func relatar(w io.Writer, req iso.Requisicao, resp *moov.Message, decorrido time.Duration) error {
	mti, err := resp.GetMTI()
	if err != nil {
		return fmt.Errorf("lendo MTI: %w", err)
	}

	stan, err := resp.GetString(11)
	if err != nil {
		return fmt.Errorf("lendo DE 11: %w", err)
	}

	de39, err := resp.GetString(39)
	if err != nil {
		return fmt.Errorf("lendo DE 39: %w", err)
	}

	empacotada, err := resp.Pack()
	if err != nil {
		return fmt.Errorf("reempacotando a resposta: %w", err)
	}

	fmt.Fprintf(w, "resposta   : %s\n", empacotada)
	fmt.Fprintf(w, "MTI        : %s\n", mti)
	fmt.Fprintf(w, "DE 11 STAN : %s\n", stan)
	fmt.Fprintf(w, "DE 39      : %s\n", de39)
	fmt.Fprintf(w, "decorrido  : %s\n", decorrido)

	if stan != req.STAN {
		return fmt.Errorf("STAN da resposta %q difere do enviado %q", stan, req.STAN)
	}
	if mti != iso.MTIAuthResponse {
		return fmt.Errorf("MTI %q inesperado, esperado %q", mti, iso.MTIAuthResponse)
	}

	return nil
}
