package main

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

// autorizadorDeTeste sobe um servidor que reproduz o comportamento do
// cmd/authorizer no passo 1, usando o mesmo caminho de codigo de montagem da
// resposta. O binario do autorizador vive em outro package main e nao pode ser
// importado aqui; a verificacao entre os dois binarios reais e manual e esta
// documentada no README.
func autorizadorDeTeste(t *testing.T, de39 string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			bruta, err := iso.ReadFrame(conn)
			if err != nil {
				return
			}
			req, err := iso.Parse(bruta)
			if err != nil {
				return
			}
			resp, err := iso.BuildResponse(req, de39)
			if err != nil {
				return
			}
			empacotada, err := resp.Pack()
			if err != nil {
				return
			}
			if err := iso.WriteFrame(conn, empacotada); err != nil {
				return
			}
		}
	}()

	return ln.Addr().String()
}

// TestTrocarPontaAPonta cobre o caminho completo do passo 2: montagem da 0100,
// enquadramento, envio por TCP, leitura e parse da 0110.
func TestTrocarPontaAPonta(t *testing.T) {
	conn, err := net.Dial("tcp", autorizadorDeTeste(t, "00"))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	req := requisicaoFixa(time.Now())

	resp, err := trocar(conn, req)
	if err != nil {
		t.Fatalf("trocar: %v", err)
	}

	mti, err := resp.GetMTI()
	if err != nil {
		t.Fatalf("GetMTI: %v", err)
	}
	if mti != iso.MTIAuthResponse {
		t.Errorf("MTI = %q, esperado %q", mti, iso.MTIAuthResponse)
	}

	stan, err := resp.GetString(11)
	if err != nil {
		t.Fatalf("GetString DE 11: %v", err)
	}
	if stan != req.STAN {
		t.Errorf("DE 11 = %q, esperado %q", stan, req.STAN)
	}
}

// TestTrocarFalhaSemServidor confirma que a ausencia do autorizador vira erro,
// e nao bloqueio indefinido.
func TestTrocarFalhaSemServidor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	endereco := ln.Addr().String()

	conn, err := net.Dial("tcp", endereco)
	if err != nil {
		ln.Close()
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	// o servidor fecha sem responder
	aceita, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	aceita.Close()
	ln.Close()

	if _, err := trocar(conn, requisicaoFixa(time.Now())); err == nil {
		t.Error("esperado erro quando o autorizador fecha sem responder, obtido nil")
	}
}

func TestRelatarSaida(t *testing.T) {
	conn, err := net.Dial("tcp", autorizadorDeTeste(t, "51"))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	req := requisicaoFixa(time.Now())
	resp, err := trocar(conn, req)
	if err != nil {
		t.Fatalf("trocar: %v", err)
	}

	var saida bytes.Buffer
	if err := relatar(&saida, req, resp, 3*time.Millisecond); err != nil {
		t.Fatalf("relatar: %v", err)
	}

	for _, trecho := range []string{"MTI        : 0110", "DE 11 STAN : 000001", "DE 39      : 51"} {
		if !strings.Contains(saida.String(), trecho) {
			t.Errorf("saida nao contem %q:\n%s", trecho, saida.String())
		}
	}
}

// TestRelatarDetectaSTANDivergente protege a correlacao entre requisicao e
// resposta, que e a base da medicao de latencia por transacao nos passos
// seguintes.
func TestRelatarDetectaSTANDivergente(t *testing.T) {
	req := requisicaoFixa(time.Now())

	msg, err := req.Message()
	if err != nil {
		t.Fatalf("montando a 0100: %v", err)
	}
	resp, err := iso.BuildResponse(msg, "00")
	if err != nil {
		t.Fatalf("BuildResponse: %v", err)
	}
	if err := resp.Field(11, "999999"); err != nil {
		t.Fatalf("adulterando DE 11: %v", err)
	}

	err = relatar(io.Discard, req, resp, time.Millisecond)
	if err == nil {
		t.Fatal("esperado erro para STAN divergente, obtido nil")
	}
	if !strings.Contains(err.Error(), "999999") {
		t.Errorf("erro deveria citar o STAN recebido: %v", err)
	}
}

// TestRequisicaoFixaEhValida garante que a requisicao embutida no injetor
// satisfaz as larguras do spec.
func TestRequisicaoFixaEhValida(t *testing.T) {
	empacotada, err := requisicaoFixa(time.Now()).Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(empacotada) != 120 {
		t.Errorf("0100 com %d bytes, esperado 120", len(empacotada))
	}
	if !bytes.HasPrefix(empacotada, []byte("0100")) {
		t.Errorf("MTI inesperado: %s", empacotada[:4])
	}
}
