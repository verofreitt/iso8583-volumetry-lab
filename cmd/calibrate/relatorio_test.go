package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// repeticaoBoa monta uma repeticao que passa em todos os criterios.
func repeticaoBoa() Repeticao {
	return Repeticao{
		AtrasoMedioUS:     500,
		AtrasoMaximoUS:    5000,
		PercentualDoAlvo:  100,
		InjetorSaturado:   false,
		MedianaServicoUS:  180,
		P99ServicoUS:      2500,
		MedianaRespostaUS: 700,
		P99RespostaUS:     4000,
	}
}

// repeticaoSaturada representa uma rodada em que o injetor nao sustentou a
// taxa: o atraso medio supera o intervalo entre chegadas.
func repeticaoSaturada() Repeticao {
	r := repeticaoBoa()
	r.InjetorSaturado = true
	r.AtrasoMedioUS = 18410
	return r
}

func nivelCom(tps float64, reps ...Repeticao) Nivel {
	n := Nivel{TPS: tps, Repeticoes: reps}
	n.consolidar(99)
	return n
}

// --- niveis ---

func TestParsearNiveis(t *testing.T) {
	niveis, err := parsearNiveis("500, 100,1000 ,2000")
	if err != nil {
		t.Fatalf("parsearNiveis: %v", err)
	}

	esperado := []float64{100, 500, 1000, 2000}
	if len(niveis) != len(esperado) {
		t.Fatalf("%d niveis, esperado %d", len(niveis), len(esperado))
	}
	for i := range esperado {
		if niveis[i] != esperado[i] {
			t.Errorf("niveis = %v, esperado ordenado %v", niveis, esperado)
			break
		}
	}
}

func TestParsearNiveisRejeitaInvalidos(t *testing.T) {
	for _, spec := range []string{"", "  ", "abc", "100,-5", "0"} {
		if _, err := parsearNiveis(spec); err == nil {
			t.Errorf("esperado erro para %q, obtido nil", spec)
		}
	}
}

// --- criterio de saturacao ---

// TestUmaRepeticaoRuimNaoDerrubaONivel e o teste que corrige o criterio
// original, que exigia unanimidade entre repeticoes.
//
// Na primeira calibracao desta maquina, uma repeticao isolada a 250 TPS
// registrou atraso medio de 18410 us contra 642 us das outras duas, e o teto
// declarado despencou de 2000 para 100 TPS. O numero nao descrevia o aparato:
// descrevia um engasgo transitorio da maquina durante uma rodada.
func TestUmaRepeticaoRuimNaoDerrubaONivel(t *testing.T) {
	n := nivelCom(250, repeticaoBoa(), repeticaoBoa(), repeticaoSaturada())

	if !n.Sustentado {
		t.Errorf("uma repeticao contaminada nao deveria derrubar o nivel: %q", n.MotivoSaturacao)
	}
	if n.RepeticoesFalhas != 1 {
		t.Errorf("RepeticoesFalhas = %d, esperado 1", n.RepeticoesFalhas)
	}
	// a repeticao reprovada continua registrada, para inspecao
	if !strings.Contains(n.MotivoSaturacao, "repeticao 3") {
		t.Errorf("a repeticao reprovada deveria ficar registrada: %q", n.MotivoSaturacao)
	}
}

// TestMaioriaRuimDerrubaONivel e o contraponto: falha sistematica reprova.
func TestMaioriaRuimDerrubaONivel(t *testing.T) {
	n := nivelCom(250, repeticaoBoa(), repeticaoSaturada(), repeticaoSaturada())

	if n.Sustentado {
		t.Error("duas de tres repeticoes reprovadas deveriam derrubar o nivel")
	}
	if n.RepeticoesFalhas != 2 {
		t.Errorf("RepeticoesFalhas = %d, esperado 2", n.RepeticoesFalhas)
	}
}

func TestNivelCriteriosDeSaturacao(t *testing.T) {
	casos := map[string]func(*Repeticao){
		"atraso acima do intervalo": func(r *Repeticao) { r.InjetorSaturado = true; r.AtrasoMedioUS = 9000 },
		"vazao abaixo do limiar":    func(r *Repeticao) { r.PercentualDoAlvo = 95 },
		"erro de transporte":        func(r *Repeticao) { r.ErrosTransporte = 1 },
		"timeout":                   func(r *Repeticao) { r.Timeouts = 1 },
	}
	for nome, quebrar := range casos {
		t.Run(nome, func(t *testing.T) {
			r := repeticaoBoa()
			quebrar(&r)
			// as tres repeticoes ruins: falha sistematica, nao ruido
			if n := nivelCom(500, r, r, r); n.Sustentado {
				t.Error("nivel deveria ter sido marcado como saturado")
			}
		})
	}
}

func TestNivelConsolidaPelaMediana(t *testing.T) {
	a, b, c := repeticaoBoa(), repeticaoBoa(), repeticaoBoa()
	a.AtrasoMedioUS, b.AtrasoMedioUS, c.AtrasoMedioUS = 100, 500, 9000

	n := nivelCom(1000, a, b, c)
	if n.AtrasoMedioUS != 500 {
		t.Errorf("AtrasoMedioUS = %d, esperado a mediana 500 e nao a media", n.AtrasoMedioUS)
	}
	if n.IntervaloUS != 1000 {
		t.Errorf("IntervaloUS = %d, esperado 1000 para 1000 TPS", n.IntervaloUS)
	}
}

// --- teto declarado ---

// TestTetoParaNaPrimeiraSaturacao: um nivel alto que volta a passar depois de
// um nivel saturado e coincidencia, nao capacidade.
func TestTetoParaNaPrimeiraSaturacao(t *testing.T) {
	s := repeticaoSaturada()

	r := Relatorio{Calibracao: true, Niveis: []Nivel{
		nivelCom(100, repeticaoBoa()),
		nivelCom(500, repeticaoBoa()),
		nivelCom(1000, repeticaoBoa()),
		nivelCom(2000, s, s, s),
		nivelCom(3000, repeticaoBoa()), // passou por acaso
	}}
	r.concluir()

	if r.Limites.TetoInjecaoTPS != 1000 {
		t.Errorf("TetoInjecaoTPS = %g, esperado 1000: o teto para na primeira saturacao",
			r.Limites.TetoInjecaoTPS)
	}
	if r.Limites.PrimeiraTaxaSaturadaTPS != 2000 {
		t.Errorf("PrimeiraTaxaSaturadaTPS = %g, esperado 2000", r.Limites.PrimeiraTaxaSaturadaTPS)
	}
}

func TestTetoQuandoNenhumNivelSatura(t *testing.T) {
	r := Relatorio{Calibracao: true, Niveis: []Nivel{
		nivelCom(100, repeticaoBoa()),
		nivelCom(500, repeticaoBoa()),
	}}
	r.concluir()

	if r.Limites.TetoInjecaoTPS != 500 {
		t.Errorf("TetoInjecaoTPS = %g, esperado 500", r.Limites.TetoInjecaoTPS)
	}
	if !contemObservacao(r, "acima da faixa varrida") {
		t.Errorf("faltou avisar que o teto nao foi determinado: %v", r.Limites.Observacoes)
	}
}

func TestTetoQuandoTodosOsNiveisSaturam(t *testing.T) {
	s := repeticaoSaturada()

	r := Relatorio{Calibracao: true, Niveis: []Nivel{
		nivelCom(100, s, s, s),
		nivelCom(500, s, s, s),
	}}
	r.concluir()

	if r.Limites.TetoInjecaoTPS != 0 {
		t.Errorf("TetoInjecaoTPS = %g, esperado 0", r.Limites.TetoInjecaoTPS)
	}
	if !contemObservacao(r, "comeca acima do teto") {
		t.Errorf("faltou avisar que a faixa comeca alta demais: %v", r.Limites.Observacoes)
	}
}

// --- pisos declarados ---

// TestPisosVemDoConjuntoDosNiveis corrige a versao original, que tirava os
// pisos do nivel mais baixo.
//
// Nesta classe de maquina o nivel menos carregado nao e o menos ruidoso: na
// primeira calibracao, o p99 a 100 TPS foi 9063 us contra 771 us a 1500 TPS.
// Tirar o piso do nivel mais baixo reportaria o pior caso como se fosse o
// melhor.
func TestPisosVemDoConjuntoDosNiveis(t *testing.T) {
	baixo := repeticaoBoa()
	baixo.AtrasoMedioUS = 900
	baixo.P99ServicoUS = 9000
	baixo.MedianaServicoUS = 230

	meio := repeticaoBoa()
	meio.AtrasoMedioUS = 400
	meio.P99ServicoUS = 800
	meio.MedianaServicoUS = 160

	alto := repeticaoBoa()
	alto.AtrasoMedioUS = 600
	alto.P99ServicoUS = 3500
	alto.MedianaServicoUS = 200

	r := Relatorio{Calibracao: true, Niveis: []Nivel{
		nivelCom(100, baixo), nivelCom(1500, meio), nivelCom(2000, alto),
	}}
	r.concluir()

	if r.Limites.PisoAtrasoUS != 400 {
		t.Errorf("PisoAtrasoUS = %d, esperado o minimo 400 e nao o do nivel mais baixo", r.Limites.PisoAtrasoUS)
	}
	if r.Limites.PisoServicoMedianaUS != 200 {
		t.Errorf("PisoServicoMedianaUS = %d, esperado a mediana entre niveis 200", r.Limites.PisoServicoMedianaUS)
	}
	if r.Limites.RuidoP99MinimoUS != 800 {
		t.Errorf("RuidoP99MinimoUS = %d, esperado 800", r.Limites.RuidoP99MinimoUS)
	}
	if r.Limites.RuidoP99MedianaUS != 3500 {
		t.Errorf("RuidoP99MedianaUS = %d, esperado 3500", r.Limites.RuidoP99MedianaUS)
	}
	if r.Limites.RuidoP99MaximoUS != 9000 {
		t.Errorf("RuidoP99MaximoUS = %d, esperado 9000: a dispersao e o proprio achado", r.Limites.RuidoP99MaximoUS)
	}
}

// --- apresentacao ---

func TestImprimirContemLimitesEMotivos(t *testing.T) {
	s := repeticaoSaturada()

	r := Relatorio{
		Calibracao:   true,
		Procedimento: Procedimento{Repeticoes: 3, Duracao: "15s", Conexoes: 32, ModoAlvo: "--echo-only"},
		Niveis: []Nivel{
			nivelCom(100, repeticaoBoa()),
			nivelCom(2000, s, s, s),
		},
	}
	r.concluir()

	var saida bytes.Buffer
	r.imprimir(&saida)
	texto := saida.String()

	for _, trecho := range []string{
		"teto de injecao          : 100 TPS",
		"primeira taxa saturada   : 2000 TPS",
		"piso de atraso",
		"ruido na cauda",
		"2000 TPS saturou",
		"SATURADO",
		"sustentado",
	} {
		if !strings.Contains(texto, trecho) {
			t.Errorf("saida nao contem %q:\n%s", trecho, texto)
		}
	}
}

// TestImprimirNaoRotulaSustentadoComoSaturado cobre um defeito de
// apresentacao: o campo de motivo carrega tanto a reprovacao quanto a ressalva
// de um nivel que passou pela mediana, e rotular os dois como "saturou"
// contradiz a coluna de situacao da tabela.
func TestImprimirNaoRotulaSustentadoComoSaturado(t *testing.T) {
	// nivel sustentado pela mediana, com uma repeticao reprovada
	n := nivelCom(1500, repeticaoBoa(), repeticaoBoa(), repeticaoSaturada())
	if !n.Sustentado {
		t.Fatalf("o nivel deveria ter sido sustentado: %q", n.MotivoSaturacao)
	}

	r := Relatorio{Calibracao: true, Niveis: []Nivel{n}}
	r.concluir()

	var saida bytes.Buffer
	r.imprimir(&saida)
	texto := saida.String()

	if strings.Contains(texto, "1500 TPS saturou") {
		t.Errorf("nivel sustentado rotulado como saturado:\n%s", texto)
	}
	if !strings.Contains(texto, "1500 TPS, ressalva") {
		t.Errorf("esperada a ressalva do nivel sustentado:\n%s", texto)
	}
}

func contemObservacao(r Relatorio, trecho string) bool {
	for _, o := range r.Limites.Observacoes {
		if strings.Contains(o, trecho) {
			return true
		}
	}
	return false
}

// --- auxiliares ---

func TestMedianas(t *testing.T) {
	if got := medianaInt([]int64{5, 1, 3}); got != 3 {
		t.Errorf("medianaInt = %d, esperado 3", got)
	}
	if got := medianaInt(nil); got != 0 {
		t.Errorf("medianaInt(nil) = %d, esperado 0", got)
	}
	if got := medianaFloat([]float64{100, 50, 99}); got != 99 {
		t.Errorf("medianaFloat = %v, esperado 99", got)
	}
	if got := medianaFloat(nil); got != 0 {
		t.Errorf("medianaFloat(nil) = %v, esperado 0", got)
	}
	if got := minimoInt([]int64{5, 1, 3}); got != 1 {
		t.Errorf("minimoInt = %d, esperado 1", got)
	}
	if got := minimoInt(nil); got != 0 {
		t.Errorf("minimoInt(nil) = %d, esperado 0", got)
	}
}

func TestAmbienteProcessoFixaGOMAXPROCSeGOGC(t *testing.T) {
	env := ambienteProcesso(2, "200")

	var temProcs, temGC bool
	for _, e := range env {
		if e == "GOMAXPROCS=2" {
			temProcs = true
		}
		if e == "GOGC=200" {
			temGC = true
		}
	}
	if !temProcs || !temGC {
		t.Error("GOMAXPROCS e GOGC deveriam ter sido impostos ao processo filho")
	}

	// sem valores, nada e acrescentado e o filho herda o ambiente como esta
	if got, want := len(ambienteProcesso(0, "")), len(os.Environ()); got != want {
		t.Errorf("ambiente com %d entradas, esperado %d: nada deveria ter sido imposto", got, want)
	}
}
