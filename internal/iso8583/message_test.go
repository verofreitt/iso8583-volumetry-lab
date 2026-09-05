package iso8583

import (
	"strings"
	"testing"

	moov "github.com/moov-io/iso8583"
)

// requisicaoExemplo e a 0100 canonica usada nos testes e na verificacao manual
// descrita no README.
//
// O PAN comeca por 9 porque o ISO/IEC 7812 reserva o MII 9 para atribuicao
// nacional, faixa nao alocada a nenhum esquema internacional de cartoes. O
// numero e valido por Luhn e inteiramente sintetico.
func requisicaoExemplo(t *testing.T) *moov.Message {
	t.Helper()

	msg := NewMessage()
	msg.MTI(MTIAuthRequest)

	campos := []struct {
		de    int
		valor string
	}{
		{2, "9999990000000014"},
		{3, "000000"},
		{4, "000000010000"},
		{7, "0905143000"},
		{11, "000001"},
		{12, "143000"},
		{13, "0905"},
		{18, "5411"},
		{22, "021"},
		{32, "000001"},
		{37, "000000000001"},
		{41, "TERM0001"},
		{49, "986"},
	}
	for _, c := range campos {
		if err := msg.Field(c.de, c.valor); err != nil {
			t.Fatalf("gravando DE %d: %v", c.de, err)
		}
	}
	return msg
}

// TestPackRequisicaoLayout fixa o layout exato da 0100 em bytes. E este teste
// que sustenta a afirmacao, no artigo, de que as mensagens geradas sao
// mensagens ISO 8583 validas e nao apenas aceitas pelo mock.
func TestPackRequisicaoLayout(t *testing.T) {
	esperado := strings.Join([]string{
		"0100",             // MTI
		"7238440108808000", // bitmap primario: DE 2,3,4,7,11,12,13,18,22,32,37,41,49
		"16",               // DE 2 LLVAR: tamanho
		"9999990000000014", // DE 2 PAN
		"000000",           // DE 3 processing code
		"000000010000",     // DE 4 valor
		"0905143000",       // DE 7 data/hora de transmissao
		"000001",           // DE 11 STAN
		"143000",           // DE 12 hora local
		"0905",             // DE 13 data local
		"5411",             // DE 18 MCC
		"021",              // DE 22 POS entry mode
		"06",               // DE 32 LLVAR: tamanho
		"000001",           // DE 32 instituicao adquirente
		"000000000001",     // DE 37 RRN
		"TERM0001",         // DE 41 terminal
		"986",              // DE 49 moeda
	}, "")

	empacotada, err := requisicaoExemplo(t).Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if got := string(empacotada); got != esperado {
		t.Errorf("layout da 0100 divergente\n esperado: %s\n obtido:   %s", esperado, got)
	}
	if len(empacotada) != 120 {
		t.Errorf("tamanho da 0100 = %d bytes, esperado 120", len(empacotada))
	}
}

// TestParseRoundTrip confirma que o parse recupera exatamente o que foi
// empacotado, sem transformacao de valor.
func TestParseRoundTrip(t *testing.T) {
	original := requisicaoExemplo(t)
	empacotada, err := original.Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	lida, err := Parse(empacotada)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	mti, err := lida.GetMTI()
	if err != nil {
		t.Fatalf("GetMTI: %v", err)
	}
	if mti != MTIAuthRequest {
		t.Errorf("MTI = %q, esperado %q", mti, MTIAuthRequest)
	}

	for de := range original.GetFields() {
		if de == 0 || de == 1 {
			continue
		}
		esperado, err := original.GetString(de)
		if err != nil {
			t.Fatalf("GetString original DE %d: %v", de, err)
		}
		obtido, err := lida.GetString(de)
		if err != nil {
			t.Fatalf("GetString lida DE %d: %v", de, err)
		}
		if obtido != esperado {
			t.Errorf("DE %d = %q, esperado %q", de, obtido, esperado)
		}
	}

	reempacotada, err := lida.Pack()
	if err != nil {
		t.Fatalf("Pack apos parse: %v", err)
	}
	if string(reempacotada) != string(empacotada) {
		t.Errorf("reempacotamento divergente\n esperado: %s\n obtido:   %s", empacotada, reempacotada)
	}
}

// TestBuildResponseLayout fixa o layout exato da 0110.
func TestBuildResponseLayout(t *testing.T) {
	resp, err := BuildResponse(requisicaoExemplo(t), "00")
	if err != nil {
		t.Fatalf("BuildResponse: %v", err)
	}

	empacotada, err := resp.Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	esperado := strings.Join([]string{
		"0110",             // MTI de resposta
		"723800010A808000", // bitmap: DE 2,3,4,7,11,12,13,32,37,39,41,49
		"16", "9999990000000014",
		"000000",
		"000000010000",
		"0905143000",
		"000001",
		"143000",
		"0905",
		"06", "000001",
		"000000000001",
		"00",       // DE 39
		"TERM0001", // DE 41
		"986",      // DE 49
	}, "")

	if got := string(empacotada); got != esperado {
		t.Errorf("layout da 0110 divergente\n esperado: %s\n obtido:   %s", esperado, got)
	}
}

// TestBuildResponseNaoEcoaCamposDeRequisicao trava a decisao documentada: DE 18
// e DE 22 descrevem o ponto de captura e nao aparecem na resposta.
func TestBuildResponseNaoEcoaCamposDeRequisicao(t *testing.T) {
	resp, err := BuildResponse(requisicaoExemplo(t), "00")
	if err != nil {
		t.Fatalf("BuildResponse: %v", err)
	}

	presentes := resp.GetFields()
	for _, de := range []int{18, 22} {
		if _, ok := presentes[de]; ok {
			t.Errorf("DE %d nao deveria estar presente na 0110", de)
		}
	}
	for _, de := range EchoDEs() {
		if _, ok := presentes[de]; !ok {
			t.Errorf("DE %d deveria ter sido ecoado na 0110", de)
		}
	}
	if _, ok := presentes[39]; !ok {
		t.Error("DE 39 ausente na 0110")
	}
}

// TestBuildResponseOmiteDEAusente confirma que um DE nao informado na
// requisicao nao e inventado na resposta.
func TestBuildResponseOmiteDEAusente(t *testing.T) {
	req := NewMessage()
	req.MTI(MTIAuthRequest)
	if err := req.Field(11, "000042"); err != nil {
		t.Fatalf("gravando DE 11: %v", err)
	}

	resp, err := BuildResponse(req, "51")
	if err != nil {
		t.Fatalf("BuildResponse: %v", err)
	}

	presentes := resp.GetFields()
	if _, ok := presentes[2]; ok {
		t.Error("DE 2 ausente na requisicao nao deveria aparecer na resposta")
	}
	stan, err := resp.GetString(11)
	if err != nil {
		t.Fatalf("GetString DE 11: %v", err)
	}
	if stan != "000042" {
		t.Errorf("DE 11 = %q, esperado %q", stan, "000042")
	}
	de39, err := resp.GetString(39)
	if err != nil {
		t.Fatalf("GetString DE 39: %v", err)
	}
	if de39 != "51" {
		t.Errorf("DE 39 = %q, esperado %q", de39, "51")
	}
}

func TestBuildResponseRejeitaMTIInesperado(t *testing.T) {
	req := NewMessage()
	req.MTI("0200")

	if _, err := BuildResponse(req, "00"); err == nil {
		t.Fatal("esperado erro para MTI 0200, obtido nil")
	}
}

func TestBuildResponseRejeitaDE39Invalido(t *testing.T) {
	for _, de39 := range []string{"", "0", "000"} {
		if _, err := BuildResponse(requisicaoExemplo(t), de39); err == nil {
			t.Errorf("esperado erro para DE 39 = %q, obtido nil", de39)
		}
	}
}

// TestPackRejeitaLarguraIncorreta verifica a garantia declarada em spec.go: sem
// padding, um campo fixo de largura errada falha no Pack em vez de corromper o
// fluxo em silencio.
func TestPackRejeitaLarguraIncorreta(t *testing.T) {
	msg := NewMessage()
	msg.MTI(MTIAuthRequest)
	if err := msg.Field(11, "1"); err != nil {
		t.Fatalf("gravando DE 11: %v", err)
	}

	if _, err := msg.Pack(); err == nil {
		t.Fatal("esperado erro ao empacotar DE 11 com 1 digito em campo n6, obtido nil")
	}
}
