package main

import (
	"fmt"
	"math"
	"testing"
)

// leitorDe monta um LeitorAtributo a partir de um mapa, para os testes.
func leitorDe(atributos map[string]string) LeitorAtributo {
	return func(a string) string { return atributos[a] }
}

// --- parse ---

func TestParsearVieses(t *testing.T) {
	vieses, err := parsearVieses("mcc=5967:0.40, pos_entry_mode=810:0.25")
	if err != nil {
		t.Fatalf("parsearVieses: %v", err)
	}
	if len(vieses) != 2 {
		t.Fatalf("%d vieses, esperado 2", len(vieses))
	}

	// a ordem de declaracao e preservada: o primeiro que casar prevalece, e
	// iteracao de mapa em Go nao e deterministica
	if vieses[0].Atributo != "mcc" || vieses[0].Valor != "5967" || vieses[0].Taxa != 0.40 {
		t.Errorf("primeiro vies = %+v", vieses[0])
	}
	if vieses[1].Atributo != "pos_entry_mode" || vieses[1].Valor != "810" || vieses[1].Taxa != 0.25 {
		t.Errorf("segundo vies = %+v", vieses[1])
	}
}

func TestParsearViesesVazio(t *testing.T) {
	for _, spec := range []string{"", "   "} {
		v, err := parsearVieses(spec)
		if err != nil {
			t.Errorf("parsearVieses(%q): %v", spec, err)
		}
		if len(v) != 0 {
			t.Errorf("parsearVieses(%q) = %v, esperado vazio", spec, v)
		}
	}
}

func TestParsearViesesRejeitaInvalidos(t *testing.T) {
	casos := map[string]string{
		"sem igual":            "mcc:0.40",
		"sem dois pontos":      "mcc=5967",
		"atributo vazio":       "=5967:0.40",
		"atributo descohecido": "categoria=5967:0.40",
		"valor vazio":          "mcc=:0.40",
		"taxa nao numerica":    "mcc=5967:muito",
		"taxa acima de 1":      "mcc=5967:1.5",
		"taxa negativa":        "mcc=5967:-0.1",
		"vies repetido":        "mcc=5967:0.40,mcc=5967:0.20",
	}
	for nome, spec := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := parsearVieses(spec); err == nil {
				t.Errorf("esperado erro para %q, obtido nil", spec)
			}
		})
	}
}

// TestAtributoDesconhecidoListaOsAceitos confirma que a mensagem de erro
// orienta, em vez de apenas recusar.
func TestAtributoDesconhecidoListaOsAceitos(t *testing.T) {
	_, err := parsearVieses("categoria=5967:0.40")
	if err == nil {
		t.Fatal("esperado erro")
	}
	for _, esperado := range []string{"mcc", "pos_entry_mode", "terminal_id"} {
		if !contemTexto(err.Error(), esperado) {
			t.Errorf("a mensagem deveria listar %q: %v", esperado, err)
		}
	}
}

func contemTexto(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDEDoAtributo(t *testing.T) {
	casos := map[string]int{
		"mcc": 18, "pos_entry_mode": 22, "acquirer_id": 32,
		"terminal_id": 41, "processing_code": 3, "currency": 49,
	}
	for atributo, de := range casos {
		got, ok := DEDoAtributo(atributo)
		if !ok || got != de {
			t.Errorf("DEDoAtributo(%q) = %d, %v; esperado %d, true", atributo, got, ok, de)
		}
	}
	if _, ok := DEDoAtributo("inexistente"); ok {
		t.Error("atributo inexistente nao deveria ser aceito")
	}
}

// --- validacao de configuracao ---

// TestViesComTaxaPositivaExigeCodigos cobre o caso em que approval-rate e 1
// mas o vies pode recusar: sem codigos de recusa nao haveria o que sortear.
func TestViesComTaxaPositivaExigeCodigos(t *testing.T) {
	c := configPadrao()
	c.TaxaAprovacao = 1
	c.DistRecusas = ""
	c.ViesRecusa = "mcc=5967:0.40"

	if _, err := NovoComportamento(c); err == nil {
		t.Error("esperado erro: vies pode recusar mas nao ha codigos de recusa")
	}
}

func TestViesComTaxaZeroNaoExigeCodigos(t *testing.T) {
	c := configPadrao()
	c.TaxaAprovacao = 1
	c.DistRecusas = ""
	c.ViesRecusa = "mcc=5967:0"

	if _, err := NovoComportamento(c); err != nil {
		t.Errorf("vies que nunca recusa nao deveria exigir codigos: %v", err)
	}
}

// --- efeito do vies ---

// TestViesAlteraATaxaDoAtributoAlvo e o teste central do controle positivo.
//
// A taxa de recusa do atributo viesado precisa convergir para o valor
// declarado, e a dos demais para a taxa base. E essa diferenca que a analise
// de qui-quadrado deve detectar.
func TestViesAlteraATaxaDoAtributoAlvo(t *testing.T) {
	const (
		amostras = 40000
		base     = 0.15
		viesada  = 0.40
	)

	c := configPadrao()
	c.TaxaAprovacao = 1 - base
	c.ViesRecusa = fmt.Sprintf("mcc=5967:%v", viesada)
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	medir := func(mcc string) float64 {
		ler := leitorDe(map[string]string{"mcc": mcc})
		recusadas := 0
		for i := 0; i < amostras; i++ {
			if _, de39 := comp.Decidir(fmt.Sprintf("%06d", i), ler); de39 != aprovado {
				recusadas++
			}
		}
		return float64(recusadas) / amostras
	}

	if got := medir("5967"); math.Abs(got-viesada) > 0.01 {
		t.Errorf("taxa de recusa do MCC viesado = %.4f, esperada %.2f", got, viesada)
	}
	if got := medir("5411"); math.Abs(got-base) > 0.01 {
		t.Errorf("taxa de recusa do MCC nao viesado = %.4f, esperada %.2f", got, base)
	}
}

// TestSemViesOComportamentoEhIdentico protege a comparabilidade com as rodadas
// anteriores: sem vies configurado, o mapeamento entre valor sorteado e
// desfecho precisa ser exatamente o de antes.
func TestSemViesOComportamentoEhIdentico(t *testing.T) {
	c := configPadrao()
	c.TaxaAprovacao = 0.85
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	ler := leitorDe(map[string]string{"mcc": "5967"})
	for i := 0; i < 2000; i++ {
		stan := fmt.Sprintf("%06d", i)
		_, comLeitor := comp.Decidir(stan, ler)
		_, semLeitor := comp.Decidir(stan, nil)
		if comLeitor != semLeitor {
			t.Fatalf("STAN %s: %q com leitor, %q sem; sem vies o leitor nao deveria importar",
				stan, comLeitor, semLeitor)
		}
	}
}

// TestViesNaoAfetaALatencia confirma que o vies age apenas sobre o codigo de
// resposta. Se afetasse a latencia, o controle positivo misturaria dois
// sinais e a analise nao saberia a qual atribuir a diferenca.
func TestViesNaoAfetaALatencia(t *testing.T) {
	c := configPadrao()
	c.LatenciaBase = 10 * 1000 * 1000 // 10ms
	c.TaxaAprovacao = 0.85
	c.ViesRecusa = "mcc=5967:0.40"
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	viesado := leitorDe(map[string]string{"mcc": "5967"})
	normal := leitorDe(map[string]string{"mcc": "5411"})

	for i := 0; i < 1000; i++ {
		stan := fmt.Sprintf("%06d", i)
		latV, _ := comp.Decidir(stan, viesado)
		latN, _ := comp.Decidir(stan, normal)
		if latV != latN {
			t.Fatalf("STAN %s: latencia %v com vies, %v sem", stan, latV, latN)
		}
	}
}

// TestPrimeiroViesQueCasaPrevalece fixa a regra de desempate.
func TestPrimeiroViesQueCasaPrevalece(t *testing.T) {
	c := configPadrao()
	c.TaxaAprovacao = 0.85
	c.ViesRecusa = "mcc=5967:0.90,pos_entry_mode=810:0.10"
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	// transacao que casa com os dois vieses
	ambos := leitorDe(map[string]string{"mcc": "5967", "pos_entry_mode": "810"})

	const amostras = 20000
	recusadas := 0
	for i := 0; i < amostras; i++ {
		if _, de39 := comp.Decidir(fmt.Sprintf("%06d", i), ambos); de39 != aprovado {
			recusadas++
		}
	}

	if taxa := float64(recusadas) / amostras; math.Abs(taxa-0.90) > 0.01 {
		t.Errorf("taxa = %.4f, esperada 0.90 do primeiro vies declarado", taxa)
	}
}

// TestViesEhDeterministico confirma que a reprodutibilidade sobrevive ao vies.
func TestViesEhDeterministico(t *testing.T) {
	c := configPadrao()
	c.TaxaAprovacao = 0.85
	c.ViesRecusa = "mcc=5967:0.40"
	comp, err := NovoComportamento(c)
	if err != nil {
		t.Fatalf("NovoComportamento: %v", err)
	}

	ler := leitorDe(map[string]string{"mcc": "5967"})
	var primeira []string
	for i := 0; i < 500; i++ {
		_, de39 := comp.Decidir(fmt.Sprintf("%06d", i), ler)
		primeira = append(primeira, de39)
	}

	for i := 499; i >= 0; i-- {
		comp.Decidir("999999", ler) // intercala, para embaralhar qualquer estado
		_, de39 := comp.Decidir(fmt.Sprintf("%06d", i), ler)
		if de39 != primeira[i] {
			t.Fatalf("STAN %06d: %q na segunda chamada, %q na primeira", i, de39, primeira[i])
		}
	}
}
