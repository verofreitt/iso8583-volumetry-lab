package iso8583

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

func TestFramingRoundTrip(t *testing.T) {
	payload := []byte("0100mensagem de teste")

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	if got, want := buf.Len(), 2+len(payload); got != want {
		t.Errorf("frame com %d bytes, esperado %d", got, want)
	}
	cabecalho := buf.Bytes()[:2]
	if cabecalho[0] != 0x00 || cabecalho[1] != byte(len(payload)) {
		t.Errorf("prefixo big-endian = % x, esperado 00 %02x", cabecalho, len(payload))
	}

	lido, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(lido, payload) {
		t.Errorf("payload = %q, esperado %q", lido, payload)
	}
}

// TestReadFrameLeituraFragmentada e o teste que justifica o uso de io.ReadFull.
// OneByteReader devolve um byte por chamada a Read, exatamente o que um socket
// TCP pode fazer sob carga. Uma implementacao com Read direto falharia aqui.
func TestReadFrameLeituraFragmentada(t *testing.T) {
	payload := []byte("0110resposta fragmentada em varios segmentos")

	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	lido, err := ReadFrame(iotest.OneByteReader(&buf))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(lido, payload) {
		t.Errorf("payload = %q, esperado %q", lido, payload)
	}
}

// TestReadFrameFramesConsecutivos confirma que o fluxo permanece sincronizado
// entre frames sucessivos na mesma conexao.
func TestReadFrameFramesConsecutivos(t *testing.T) {
	payloads := [][]byte{[]byte("primeiro"), []byte("segundo"), []byte("terceiro")}

	var buf bytes.Buffer
	for _, p := range payloads {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	for i, esperado := range payloads {
		lido, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame %d: %v", i, err)
		}
		if !bytes.Equal(lido, esperado) {
			t.Errorf("frame %d = %q, esperado %q", i, lido, esperado)
		}
	}

	if _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("apos o ultimo frame esperava io.EOF, obtido %v", err)
	}
}

// TestReadFrameEOFLimpo distingue o encerramento normal da conexao, que
// acontece entre frames, de um erro de transporte.
func TestReadFrameEOFLimpo(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("esperado io.EOF em fluxo vazio, obtido %v", err)
	}
}

// TestReadFrameTruncadoNaoEhEOF garante que um frame incompleto seja reportado
// como erro, e nao confundido com encerramento limpo.
func TestReadFrameTruncadoNaoEhEOF(t *testing.T) {
	casos := map[string][]byte{
		"prefixo incompleto": {0x00},
		"payload incompleto": {0x00, 0x10, 'a', 'b', 'c'},
	}
	for nome, dados := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(dados))
			if err == nil {
				t.Fatal("esperado erro, obtido nil")
			}
			if errors.Is(err, io.EOF) {
				t.Errorf("frame truncado nao deve ser reportado como io.EOF: %v", err)
			}
		})
	}
}

func TestWriteFrameRejeitaPayloadInvalido(t *testing.T) {
	var buf bytes.Buffer

	if err := WriteFrame(&buf, nil); err == nil {
		t.Error("esperado erro para payload vazio, obtido nil")
	}
	if err := WriteFrame(&buf, make([]byte, MaxFrameSize+1)); err == nil {
		t.Error("esperado erro para payload acima do maximo, obtido nil")
	}
	if buf.Len() != 0 {
		t.Errorf("nada deveria ter sido escrito, %d bytes no buffer", buf.Len())
	}
}

func TestReadFrameRejeitaTamanhoZero(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x00})); err == nil {
		t.Error("esperado erro para frame de tamanho zero, obtido nil")
	}
}
