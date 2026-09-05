package iso8583

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MaxFrameSize e o maior payload representavel pelo prefixo de 2 bytes.
const MaxFrameSize = 65535

// WriteFrame escreve o payload precedido do prefixo de tamanho de 2 bytes em
// big-endian.
//
// O prefixo e o payload sao emitidos em uma unica chamada a Write. Duas
// chamadas separadas poderiam ser entregues em segmentos TCP distintos, o que
// somaria um atraso de rede a latencia medida sem que ele pertenca ao sistema
// sob teste.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("payload vazio")
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("payload de %d bytes excede o maximo de %d", len(payload), MaxFrameSize)
	}

	frame := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(payload)))
	copy(frame[2:], payload)

	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("escrevendo frame: %w", err)
	}
	return nil
}

// ReadFrame le um frame completo: 2 bytes de tamanho em big-endian seguidos do
// payload.
//
// Usa io.ReadFull nas duas leituras. Uma chamada direta a Read pode devolver
// menos bytes que o solicitado sem que isso seja erro, e tratar o retorno curto
// como frame completo dessincronizaria o fluxo de forma silenciosa.
//
// Devolve io.EOF quando a conexao se encerra de forma limpa entre frames.
func ReadFrame(r io.Reader) ([]byte, error) {
	var cabecalho [2]byte
	if _, err := io.ReadFull(r, cabecalho[:]); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("lendo prefixo de tamanho: %w", err)
	}

	tamanho := int(binary.BigEndian.Uint16(cabecalho[:]))
	if tamanho == 0 {
		return nil, fmt.Errorf("frame com tamanho zero")
	}

	payload := make([]byte, tamanho)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("lendo payload de %d bytes: %w", tamanho, err)
	}

	return payload, nil
}
