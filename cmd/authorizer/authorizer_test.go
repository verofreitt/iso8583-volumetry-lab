package main

import (
	"net"
	"testing"
	"time"

	iso "github.com/verofreitt/iso8583-volumetry-lab/internal/iso8583"
)

// servidorDeTeste sobe o autorizador em uma porta efemera de loopback e
// devolve o endereco. O listener e fechado ao fim do teste, o que faz serve
// retornar.
func servidorDeTeste(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("escutando: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		_ = serve(ln)
	}()

	return ln.Addr().String()
}

func requisicaoDeTeste(t *testing.T, stan string) []byte {
	t.Helper()

	msg := iso.NewMessage()
	msg.MTI(iso.MTIAuthRequest)
	campos := map[int]string{
		2: "9999990000000014", 3: "000000", 4: "000000010000",
		7: "0905143000", 11: stan, 12: "143000", 13: "0905",
		18: "5411", 22: "021", 32: "000001",
		37: "000000000001", 41: "TERM0001", 49: "986",
	}
	for de, valor := range campos {
		if err := msg.Field(de, valor); err != nil {
			t.Fatalf("gravando DE %d: %v", de, err)
		}
	}

	empacotada, err := msg.Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return empacotada
}

// TestAutorizadorRespondeAutorizacao e o teste ponta a ponta do passo 1:
// conecta por TCP, envia uma 0100 enquadrada e verifica a 0110 recebida.
func TestAutorizadorRespondeAutorizacao(t *testing.T) {
	conn, err := net.Dial("tcp", servidorDeTeste(t))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	if err := iso.WriteFrame(conn, requisicaoDeTeste(t, "000001")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	bruta, err := iso.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	resp, err := iso.Parse(bruta)
	if err != nil {
		t.Fatalf("Parse da resposta: %v", err)
	}

	mti, err := resp.GetMTI()
	if err != nil {
		t.Fatalf("GetMTI: %v", err)
	}
	if mti != iso.MTIAuthResponse {
		t.Errorf("MTI = %q, esperado %q", mti, iso.MTIAuthResponse)
	}

	de39, err := resp.GetString(39)
	if err != nil {
		t.Fatalf("GetString DE 39: %v", err)
	}
	if de39 != "00" {
		t.Errorf("DE 39 = %q, esperado %q", de39, "00")
	}

	// o STAN e o que correlaciona requisicao e resposta na analise; se ele nao
	// voltar intacto, nenhuma medicao de latencia por transacao e possivel.
	stan, err := resp.GetString(11)
	if err != nil {
		t.Fatalf("GetString DE 11: %v", err)
	}
	if stan != "000001" {
		t.Errorf("DE 11 = %q, esperado %q", stan, "000001")
	}
}

// TestAutorizadorMultiplasRequisicoesNaMesmaConexao verifica que a conexao
// permanece sincronizada apos varios pares 0100/0110, requisito do injetor,
// que reaproveita conexoes.
func TestAutorizadorMultiplasRequisicoesNaMesmaConexao(t *testing.T) {
	conn, err := net.Dial("tcp", servidorDeTeste(t))
	if err != nil {
		t.Fatalf("conectando: %v", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	for _, stan := range []string{"000001", "000002", "000003"} {
		if err := iso.WriteFrame(conn, requisicaoDeTeste(t, stan)); err != nil {
			t.Fatalf("WriteFrame STAN %s: %v", stan, err)
		}

		bruta, err := iso.ReadFrame(conn)
		if err != nil {
			t.Fatalf("ReadFrame STAN %s: %v", stan, err)
		}

		resp, err := iso.Parse(bruta)
		if err != nil {
			t.Fatalf("Parse STAN %s: %v", stan, err)
		}

		obtido, err := resp.GetString(11)
		if err != nil {
			t.Fatalf("GetString DE 11: %v", err)
		}
		if obtido != stan {
			t.Errorf("DE 11 = %q, esperado %q", obtido, stan)
		}
	}
}

// TestResponderRejeitaMensagemInvalida confirma que lixo no fluxo produz erro
// em vez de resposta silenciosa.
func TestResponderRejeitaMensagemInvalida(t *testing.T) {
	if _, err := responder([]byte("nao e uma mensagem iso 8583")); err == nil {
		t.Error("esperado erro para payload invalido, obtido nil")
	}
}
