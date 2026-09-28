package iso8583

import (
	"fmt"

	moov "github.com/moov-io/iso8583"
)

const (
	// MTIAuthRequest e a requisicao de autorizacao.
	MTIAuthRequest = "0100"
	// MTIAuthResponse e a resposta de autorizacao.
	MTIAuthResponse = "0110"
)

// echoDEs sao os data elements copiados da requisicao 0100 para a resposta 0110.
//
// DE 18 (MCC) e DE 22 (POS entry mode) ficam de fora: descrevem o ponto de
// captura e a natureza do estabelecimento, sao informacao do lado da requisicao
// e nao tem funcao na resposta. DE 39 nao entra aqui porque e gerado pelo
// autorizador, nao ecoado.
var echoDEs = []int{2, 3, 4, 7, 11, 12, 13, 32, 37, 41, 49}

// EchoDEs devolve a lista dos data elements ecoados na resposta.
func EchoDEs() []int {
	return append([]int(nil), echoDEs...)
}

// NewMessage cria uma mensagem vazia sobre o Spec do experimento.
func NewMessage() *moov.Message {
	return moov.NewMessage(Spec)
}

// Parse desempacota os bytes de uma mensagem ja desenquadrada.
func Parse(raw []byte) (*moov.Message, error) {
	msg := NewMessage()
	if err := msg.Unpack(raw); err != nil {
		return nil, fmt.Errorf("desempacotando mensagem: %w", err)
	}
	return msg, nil
}

// BuildResponse monta a 0110 correspondente a uma 0100, ecoando os data
// elements de echoDEs que estiverem presentes na requisicao e preenchendo o
// DE 39 com o codigo informado.
func BuildResponse(req *moov.Message, de39 string) (*moov.Message, error) {
	mti, err := req.GetMTI()
	if err != nil {
		return nil, fmt.Errorf("lendo MTI da requisicao: %w", err)
	}
	if mti != MTIAuthRequest {
		return nil, fmt.Errorf("MTI %q nao suportado, esperado %q", mti, MTIAuthRequest)
	}
	if len(de39) != 2 {
		return nil, fmt.Errorf("DE 39 deve ter 2 caracteres, recebido %q", de39)
	}

	presentes := req.GetFields()

	resp := NewMessage()
	resp.MTI(MTIAuthResponse)

	for _, de := range echoDEs {
		if _, ok := presentes[de]; !ok {
			continue
		}
		valor, err := req.GetString(de)
		if err != nil {
			return nil, fmt.Errorf("lendo DE %d da requisicao: %w", de, err)
		}
		if err := resp.Field(de, valor); err != nil {
			return nil, fmt.Errorf("gravando DE %d na resposta: %w", de, err)
		}
	}

	if err := resp.Field(39, de39); err != nil {
		return nil, fmt.Errorf("gravando DE 39 na resposta: %w", err)
	}

	return resp, nil
}
