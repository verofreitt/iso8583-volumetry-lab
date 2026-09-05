package iso8583

import (
	"fmt"
	"time"

	moov "github.com/moov-io/iso8583"
)

// Requisicao reune os valores de uma autorizacao 0100. Os campos correspondem
// um a um ao subconjunto de data elements documentado em docs/experimento.md.
//
// Os campos de largura fixa devem ser informados ja na largura exata: o spec
// nao declara padding, e um valor de largura incorreta falha no Pack. Ver a
// justificativa em spec.go.
type Requisicao struct {
	PAN                   string // DE 2, LLVAR ate 19
	ProcessingCode        string // DE 3, exatamente 6
	Valor                 string // DE 4, exatamente 12
	STAN                  string // DE 11, exatamente 6
	MCC                   string // DE 18, exatamente 4
	POSEntryMode          string // DE 22, exatamente 3
	InstituicaoAdquirente string // DE 32, LLVAR ate 11
	RRN                   string // DE 37, exatamente 12
	TerminalID            string // DE 41, exatamente 8
	Moeda                 string // DE 49, exatamente 3

	// Instante origina os DE 7, 12 e 13. O DE 7 e a data/hora de transmissao e
	// segue a convencao da norma de ser expresso em UTC; os DE 12 e 13 sao
	// hora e data locais do ponto de captura.
	Instante time.Time
}

// Message monta a mensagem 0100 correspondente.
func (r Requisicao) Message() (*moov.Message, error) {
	msg := NewMessage()
	msg.MTI(MTIAuthRequest)

	campos := []struct {
		de    int
		valor string
	}{
		{2, r.PAN},
		{3, r.ProcessingCode},
		{4, r.Valor},
		{7, r.Instante.UTC().Format("0102150405")}, // MMDDhhmmss
		{11, r.STAN},
		{12, r.Instante.Format("150405")}, // hhmmss
		{13, r.Instante.Format("0102")},   // MMDD
		{18, r.MCC},
		{22, r.POSEntryMode},
		{32, r.InstituicaoAdquirente},
		{37, r.RRN},
		{41, r.TerminalID},
		{49, r.Moeda},
	}

	for _, c := range campos {
		if err := msg.Field(c.de, c.valor); err != nil {
			return nil, fmt.Errorf("gravando DE %d: %w", c.de, err)
		}
	}

	return msg, nil
}

// Pack monta e empacota a 0100 em uma unica operacao.
func (r Requisicao) Pack() ([]byte, error) {
	msg, err := r.Message()
	if err != nil {
		return nil, err
	}

	empacotada, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("empacotando 0100: %w", err)
	}

	return empacotada, nil
}
