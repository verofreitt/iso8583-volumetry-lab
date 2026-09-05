package iso8583

import (
	"testing"
	"time"
)

// TestRequisicaoDerivaCamposTemporais fixa a origem dos DE 7, 12 e 13.
//
// O instante escolhido esta em UTC-3, de modo que a hora local difere da hora
// UTC. Se o DE 7 fosse formatado em hora local, ou os DE 12 e 13 em UTC, o
// teste falharia — o que nao aconteceria se todos usassem o mesmo fuso.
func TestRequisicaoDerivaCamposTemporais(t *testing.T) {
	zona := time.FixedZone("TESTE", -3*60*60)
	req := Requisicao{
		PAN: "9999990000000014", ProcessingCode: "000000", Valor: "000000010000",
		STAN: "000001", MCC: "5411", POSEntryMode: "021",
		InstituicaoAdquirente: "000001", RRN: "000000000001",
		TerminalID: "TERM0001", Moeda: "986",
		Instante: time.Date(2026, 9, 5, 14, 30, 0, 0, zona),
	}

	msg, err := req.Message()
	if err != nil {
		t.Fatalf("Message: %v", err)
	}

	casos := map[int]string{
		7:  "0905173000", // data/hora de transmissao em UTC: 14:30 -03:00 = 17:30Z
		12: "143000",     // hora local
		13: "0905",       // data local
	}
	for de, esperado := range casos {
		obtido, err := msg.GetString(de)
		if err != nil {
			t.Fatalf("GetString DE %d: %v", de, err)
		}
		if obtido != esperado {
			t.Errorf("DE %d = %q, esperado %q", de, obtido, esperado)
		}
	}
}

// TestRequisicaoPackLayout confirma que o construtor produz exatamente a 0100
// canonica documentada no README.
func TestRequisicaoPackLayout(t *testing.T) {
	empacotada, err := requisicaoCanonica().Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	const esperado = "0100723844010880800016999999000000001400000000000001000009051430000000011430000905541102106000001000000000001TERM0001986"
	if got := string(empacotada); got != esperado {
		t.Errorf("layout divergente\n esperado: %s\n obtido:   %s", esperado, got)
	}
}

// TestRequisicaoPackRejeitaLarguraIncorreta confirma que a ausencia de padding
// transforma um valor mal formado em erro, e nao em fluxo corrompido.
func TestRequisicaoPackRejeitaLarguraIncorreta(t *testing.T) {
	casos := map[string]func(*Requisicao){
		"STAN curto":     func(r *Requisicao) { r.STAN = "1" },
		"moeda longa":    func(r *Requisicao) { r.Moeda = "9866" },
		"terminal curto": func(r *Requisicao) { r.TerminalID = "T1" },
		"valor curto":    func(r *Requisicao) { r.Valor = "100" },
	}

	for nome, quebrar := range casos {
		t.Run(nome, func(t *testing.T) {
			req := requisicaoCanonica()
			quebrar(&req)
			if _, err := req.Pack(); err == nil {
				t.Error("esperado erro no Pack, obtido nil")
			}
		})
	}
}

// TestRequisicaoRejeitaLLVARAcimaDoMaximo cobre os campos de tamanho variavel,
// onde o limite e o maximo declarado e nao uma largura exata.
func TestRequisicaoRejeitaLLVARAcimaDoMaximo(t *testing.T) {
	req := requisicaoCanonica()
	req.PAN = "99999900000000123456" // 20 digitos, acima do maximo de 19

	if _, err := req.Pack(); err == nil {
		t.Error("esperado erro para PAN acima de 19 digitos, obtido nil")
	}
}
