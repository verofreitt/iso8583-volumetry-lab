package massa

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func transacaoValida() Transacao {
	const parcial = "999999000000001"
	return Transacao{
		ID:             "1",
		PAN:            parcial + fmt.Sprintf("%d", DigitoLuhn(parcial)),
		ProcessingCode: "000000",
		Valor:          "000000010000",
		MCC:            "5411",
		POSEntryMode:   "051",
		Adquirente:     "000001",
		TerminalID:     "TERM0001",
		Moeda:          "986",
	}
}

// --- Luhn ---

// TestLuhn cobre a garantia que o artigo faz sobre a massa: os PANs sao
// numericamente validos, ainda que inteiramente sinteticos.
func TestLuhn(t *testing.T) {
	// o digito calculado torna o numero valido
	for _, parcial := range []string{
		"999999000000001", "999999000000002", "900000000000000", "999999999999999",
	} {
		completo := parcial + fmt.Sprintf("%d", DigitoLuhn(parcial))
		if !ValidoLuhn(completo) {
			t.Errorf("%s deveria ser valido por Luhn", completo)
		}
	}

	// alterar um digito invalida
	base := "9999990000000014"
	if !ValidoLuhn(base) {
		t.Fatalf("%s deveria ser valido", base)
	}
	for i := 0; i < len(base); i++ {
		alterado := []byte(base)
		alterado[i] = '0' + byte((int(base[i]-'0')+1)%10)
		if ValidoLuhn(string(alterado)) {
			t.Errorf("%s deveria ser invalido: digito %d alterado", alterado, i)
		}
	}
}

func TestLuhnRejeitaEntradasInvalidas(t *testing.T) {
	for _, s := range []string{"", "9", "abcd", "99999900000000a"} {
		if ValidoLuhn(s) {
			t.Errorf("%q nao deveria ser valido por Luhn", s)
		}
	}
}

// --- validacao ---

func TestValidarAceitaTransacaoBoa(t *testing.T) {
	if err := transacaoValida().Validar(); err != nil {
		t.Errorf("transacao valida recusada: %v", err)
	}
}

// TestValidarRejeitaLarguraIncorreta protege a rodada: sem padding no spec, um
// campo de largura errada falharia no Pack no meio da medicao.
func TestValidarRejeitaLarguraIncorreta(t *testing.T) {
	casos := map[string]func(*Transacao){
		"processing_code curto": func(t *Transacao) { t.ProcessingCode = "0000" },
		"valor curto":           func(t *Transacao) { t.Valor = "100" },
		"valor longo":           func(t *Transacao) { t.Valor = "0000000100000" },
		"mcc curto":             func(t *Transacao) { t.MCC = "541" },
		"pos entry mode longo":  func(t *Transacao) { t.POSEntryMode = "0511" },
		"terminal curto":        func(t *Transacao) { t.TerminalID = "TERM" },
		"moeda longa":           func(t *Transacao) { t.Moeda = "9866" },
		"pan vazio":             func(t *Transacao) { t.PAN = "" },
		"pan acima do maximo":   func(t *Transacao) { t.PAN = "99999900000000123456" },
		"adquirente vazio":      func(t *Transacao) { t.Adquirente = "" },
		"adquirente longo":      func(t *Transacao) { t.Adquirente = "000000000001" },
	}
	for nome, quebrar := range casos {
		t.Run(nome, func(t *testing.T) {
			tr := transacaoValida()
			quebrar(&tr)
			if err := tr.Validar(); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

func TestValidarRejeitaNaoNumericos(t *testing.T) {
	casos := map[string]func(*Transacao){
		"pan com letra":   func(t *Transacao) { t.PAN = "99999900000000AB" },
		"valor com letra": func(t *Transacao) { t.Valor = "00000001000X" },
		"mcc com letra":   func(t *Transacao) { t.MCC = "54A1" },
	}
	for nome, quebrar := range casos {
		t.Run(nome, func(t *testing.T) {
			tr := transacaoValida()
			quebrar(&tr)
			if err := tr.Validar(); err == nil {
				t.Error("esperado erro, obtido nil")
			}
		})
	}
}

// TestValidarRejeitaPANForaDaFaixaDeTeste protege a restricao 2 do CLAUDE.md:
// nenhum BIN real em uso pode ser emitido pelo gerador.
func TestValidarRejeitaPANForaDaFaixaDeTeste(t *testing.T) {
	tr := transacaoValida()
	// PAN valido por Luhn, mas comecando por 4, fora da faixa reservada
	const parcial = "400000000000000"
	tr.PAN = parcial + fmt.Sprintf("%d", DigitoLuhn(parcial))

	err := tr.Validar()
	if err == nil {
		t.Fatal("esperado erro para PAN fora da faixa de teste")
	}
	if !strings.Contains(err.Error(), "MII 9") {
		t.Errorf("o erro deveria explicar a faixa reservada: %v", err)
	}
}

func TestValidarRejeitaLuhnInvalido(t *testing.T) {
	tr := transacaoValida()
	tr.PAN = "9999990000000015" // digito verificador errado

	if err := tr.Validar(); err == nil {
		t.Error("esperado erro para PAN invalido por Luhn")
	}
}

// --- leitura e escrita ---

func massaDeExemplo(n int) []Transacao {
	var linhas []Transacao
	for i := 0; i < n; i++ {
		parcial := fmt.Sprintf("99999900000%04d", i)
		linhas = append(linhas, Transacao{
			ID:             fmt.Sprintf("%d", i+1),
			PAN:            parcial + fmt.Sprintf("%d", DigitoLuhn(parcial)),
			ProcessingCode: "000000",
			Valor:          FormatarValor(int64(1000 + i)),
			MCC:            "5411",
			POSEntryMode:   "051",
			Adquirente:     "000001",
			TerminalID:     fmt.Sprintf("TERM%04d", i+1),
			Moeda:          "986",
		})
	}
	return linhas
}

func TestEscreverELerRoundTrip(t *testing.T) {
	original := massaDeExemplo(10)

	var buf bytes.Buffer
	if err := Escrever(&buf, original); err != nil {
		t.Fatalf("Escrever: %v", err)
	}

	m, err := LerDe(&buf)
	if err != nil {
		t.Fatalf("LerDe: %v", err)
	}
	if m.Tamanho() != len(original) {
		t.Fatalf("%d transacoes lidas, esperado %d", m.Tamanho(), len(original))
	}
	for i := range original {
		if m.Em(i) != original[i] {
			t.Errorf("transacao %d = %+v, esperado %+v", i, m.Em(i), original[i])
		}
	}
}

func TestLerValidaCabecalho(t *testing.T) {
	conteudo := "id,pan,codigo,amount,mcc,pos_entry_mode,acquirer_id,terminal_id,currency\n"
	if _, err := LerDe(strings.NewReader(conteudo)); err == nil {
		t.Error("esperado erro para cabecalho divergente")
	}
}

// TestLerRejeitaLinhaInvalida confirma que a validacao acontece na carga, e
// nao no envio: uma falha no meio da rodada custaria a medicao inteira.
func TestLerRejeitaLinhaInvalida(t *testing.T) {
	var buf bytes.Buffer
	linhas := massaDeExemplo(3)
	linhas[1].Valor = "100" // largura errada
	if err := Escrever(&buf, linhas); err != nil {
		t.Fatalf("Escrever: %v", err)
	}

	_, err := LerDe(&buf)
	if err == nil {
		t.Fatal("esperado erro para linha invalida")
	}
	if !strings.Contains(err.Error(), "linha 3") {
		t.Errorf("o erro deveria identificar a linha do CSV: %v", err)
	}
}

func TestLerRejeitaArquivoVazio(t *testing.T) {
	if _, err := LerDe(strings.NewReader("")); err == nil {
		t.Error("esperado erro para CSV vazio")
	}
	so := strings.Join(Colunas, ",") + "\n"
	if _, err := LerDe(strings.NewReader(so)); err == nil {
		t.Error("esperado erro para CSV so com cabecalho")
	}
}

// --- ordem de consumo ---

// TestEmbaralharEhDeterministico protege a reprodutibilidade: a semente
// governa a ordem de consumo, e duas rodadas com a mesma semente precisam
// consumir as mesmas transacoes na mesma sequencia.
func TestEmbaralharEhDeterministico(t *testing.T) {
	carregar := func(semente int64) []string {
		var buf bytes.Buffer
		if err := Escrever(&buf, massaDeExemplo(50)); err != nil {
			t.Fatalf("Escrever: %v", err)
		}
		m, err := LerDe(&buf)
		if err != nil {
			t.Fatalf("LerDe: %v", err)
		}
		m.Embaralhar(semente)

		var ids []string
		for i := 0; i < 50; i++ {
			ids = append(ids, m.Em(i).ID)
		}
		return ids
	}

	a, b := carregar(42), carregar(42)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("posicao %d: %q na primeira carga, %q na segunda", i, a[i], b[i])
		}
	}

	// sementes diferentes produzem ordens diferentes
	c := carregar(43)
	iguais := 0
	for i := range a {
		if a[i] == c[i] {
			iguais++
		}
	}
	if iguais == len(a) {
		t.Error("sementes diferentes produziram a mesma ordem")
	}
}

// TestEmbaralharPreservaOConjunto confirma que embaralhar nao perde nem
// duplica transacoes.
func TestEmbaralharPreservaOConjunto(t *testing.T) {
	var buf bytes.Buffer
	if err := Escrever(&buf, massaDeExemplo(100)); err != nil {
		t.Fatalf("Escrever: %v", err)
	}
	m, err := LerDe(&buf)
	if err != nil {
		t.Fatalf("LerDe: %v", err)
	}
	m.Embaralhar(7)

	vistos := map[string]int{}
	for i := 0; i < m.Tamanho(); i++ {
		vistos[m.Em(i).ID]++
	}
	if len(vistos) != 100 {
		t.Errorf("%d IDs distintos, esperado 100", len(vistos))
	}
	for id, n := range vistos {
		if n != 1 {
			t.Errorf("ID %s apareceu %d vezes", id, n)
		}
	}
}

// TestEmEhCiclico confirma o reuso da massa em rodadas mais longas que ela.
func TestEmEhCiclico(t *testing.T) {
	var buf bytes.Buffer
	if err := Escrever(&buf, massaDeExemplo(10)); err != nil {
		t.Fatalf("Escrever: %v", err)
	}
	m, err := LerDe(&buf)
	if err != nil {
		t.Fatalf("LerDe: %v", err)
	}

	for i := 0; i < 10; i++ {
		if m.Em(i) != m.Em(i+10) || m.Em(i) != m.Em(i+20) {
			t.Errorf("consumo na posicao %d nao e ciclico", i)
		}
	}
}

// --- valor ---

func TestFormatarValor(t *testing.T) {
	casos := map[int64]string{
		0: "000000000000", 1: "000000000001", 10000: "000000010000",
		999999999999: "999999999999",
	}
	for centavos, esperado := range casos {
		if got := FormatarValor(centavos); got != esperado {
			t.Errorf("FormatarValor(%d) = %q, esperado %q", centavos, got, esperado)
		}
	}
}

func TestValorEmCentavos(t *testing.T) {
	v, err := ValorEmCentavos("000000010000")
	if err != nil {
		t.Fatalf("ValorEmCentavos: %v", err)
	}
	if v != 10000 {
		t.Errorf("ValorEmCentavos = %d, esperado 10000", v)
	}
}
