package metrics

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)

// registroEm monta um registro bem-sucedido com os deslocamentos informados,
// em milissegundos, a partir do instante agendado.
func registroEm(indice int, atrasoEnvioMS, servicoMS int) Registro {
	agendado := base.Add(time.Duration(indice) * 100 * time.Millisecond)
	envio := agendado.Add(time.Duration(atrasoEnvioMS) * time.Millisecond)
	return Registro{
		Indice:   indice,
		STAN:     leftPad(indice),
		MassaID:  fmt.Sprintf("m%03d", indice),
		Agendado: agendado,
		Envio:    envio,
		Resposta: envio.Add(time.Duration(servicoMS) * time.Millisecond),
		DE39:     "00",
	}
}

func leftPad(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

// TestLatenciasDistinguemOmissaoCoordenada e o teste central do pacote.
//
// Uma requisicao que deveria ter partido em t mas so partiu em t+40ms, e que
// levou 10ms de servico, tem 10ms de latencia de servico e 50ms de latencia de
// resposta. E a segunda que descreve o que o cliente observou; reportar apenas
// a primeira esconderia o atraso do injetor da medicao.
func TestLatenciasDistinguemOmissaoCoordenada(t *testing.T) {
	r := registroEm(0, 40, 10)

	if got, want := r.LatenciaServico(), 10*time.Millisecond; got != want {
		t.Errorf("LatenciaServico = %v, esperado %v", got, want)
	}
	if got, want := r.LatenciaResposta(), 50*time.Millisecond; got != want {
		t.Errorf("LatenciaResposta = %v, esperado %v", got, want)
	}
	if r.LatenciaResposta() <= r.LatenciaServico() {
		t.Error("com envio atrasado, a latencia de resposta deve superar a de servico")
	}
}

func TestSucessoExigeResposta(t *testing.T) {
	casos := map[string]struct {
		r        Registro
		esperado bool
	}{
		"completo":     {registroEm(0, 0, 5), true},
		"com erro":     {Registro{Agendado: base, Envio: base, Erro: "conexao fechada"}, false},
		"sem resposta": {Registro{Agendado: base, Envio: base}, false},
	}
	for nome, c := range casos {
		if got := c.r.Sucesso(); got != c.esperado {
			t.Errorf("%s: Sucesso() = %v, esperado %v", nome, got, c.esperado)
		}
	}
}

// TestColetorDescartaWarmup confirma que as chegadas iniciais saem da analise.
func TestColetorDescartaWarmup(t *testing.T) {
	c := NovoColetor(10, 3)
	for i := 0; i < 10; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}

	medidos := c.Medidos()
	if len(medidos) != 7 {
		t.Fatalf("%d registros medidos, esperado 7", len(medidos))
	}
	if medidos[0].Indice != 3 {
		t.Errorf("primeiro medido tem indice %d, esperado 3", medidos[0].Indice)
	}
	if got := c.ChegadasDescartadas(); got != 3 {
		t.Errorf("ChegadasDescartadas() = %d, esperado 3", got)
	}
}

// TestColetorIgnoraPosicoesNaoPreenchidas cobre a rodada interrompida: as
// chegadas que nunca ocorreram nao podem entrar na analise como latencia zero.
func TestColetorIgnoraPosicoesNaoPreenchidas(t *testing.T) {
	c := NovoColetor(10, 0)
	for i := 0; i < 4; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}

	if got := len(c.Medidos()); got != 4 {
		t.Errorf("%d registros medidos, esperado 4: posicoes vazias nao contam", got)
	}
}

func TestColetorWarmupMaiorQueRodada(t *testing.T) {
	c := NovoColetor(5, 10)
	for i := 0; i < 5; i++ {
		c.Registrar(registroEm(i, 0, 5))
	}
	if got := len(c.Medidos()); got != 0 {
		t.Errorf("%d registros medidos, esperado 0", got)
	}
}

func TestRegistrarIndiceForaDaFaixaEntraEmPanico(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("esperado panico ao gravar fora da faixa: gravar no lugar errado corromperia a medicao")
		}
	}()

	NovoColetor(3, 0).Registrar(registroEm(7, 0, 5))
}

// TestEscreverCSVLayout fixa as colunas e o formato do arquivo bruto.
func TestEscreverCSVLayout(t *testing.T) {
	c := NovoColetor(3, 1)
	c.Registrar(registroEm(0, 0, 5)) // warm-up, nao deve aparecer
	c.Registrar(registroEm(1, 40, 10))
	falha := Registro{
		Indice:   2,
		STAN:     "000002",
		MassaID:  "m002",
		Agendado: base.Add(200 * time.Millisecond),
		Envio:    base.Add(200 * time.Millisecond),
		Erro:     "lendo a resposta: EOF",
	}
	c.Registrar(falha)

	var buf bytes.Buffer
	if err := c.EscreverCSV(&buf); err != nil {
		t.Fatalf("EscreverCSV: %v", err)
	}

	linhas, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("relendo o CSV: %v", err)
	}

	cabecalho := []string{
		"stan", "massa_id", "ts_agendado", "ts_envio", "ts_resposta",
		"latencia_servico_us", "latencia_resposta_us", "de39", "erro_transporte",
	}
	if len(linhas) != 3 {
		t.Fatalf("%d linhas, esperado 3 (cabecalho + 2 registros); warm-up nao deve ser escrito", len(linhas))
	}
	for i, col := range cabecalho {
		if linhas[0][i] != col {
			t.Errorf("coluna %d = %q, esperado %q", i, linhas[0][i], col)
		}
	}

	// registro bem-sucedido, com envio atrasado em 40ms e servico de 10ms
	bem := linhas[1]
	if bem[0] != "000001" {
		t.Errorf("stan = %q, esperado 000001", bem[0])
	}
	if bem[1] != "m001" {
		t.Errorf("massa_id = %q, esperado m001: sem ele o raw.csv nao se liga a transacao", bem[1])
	}
	if bem[5] != "10000" {
		t.Errorf("latencia_servico_us = %q, esperado 10000", bem[5])
	}
	if bem[6] != "50000" {
		t.Errorf("latencia_resposta_us = %q, esperado 50000", bem[6])
	}
	if bem[7] != "00" {
		t.Errorf("de39 = %q, esperado 00", bem[7])
	}
	if bem[8] != "" {
		t.Errorf("erro_transporte = %q, esperado vazio", bem[8])
	}

	// registro com falha: latencias em branco, nunca zero
	ruim := linhas[2]
	if ruim[4] != "" || ruim[5] != "" || ruim[6] != "" {
		t.Errorf("linha com falha deveria ter resposta e latencias em branco, obtido %q", ruim)
	}
	if ruim[8] != "lendo a resposta: EOF" {
		t.Errorf("erro_transporte = %q", ruim[8])
	}
}

func TestEscreverCSVVazio(t *testing.T) {
	var buf bytes.Buffer
	if err := NovoColetor(0, 0).EscreverCSV(&buf); err != nil {
		t.Fatalf("EscreverCSV: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "stan,massa_id,ts_agendado") {
		t.Errorf("cabecalho ausente em coletor vazio: %q", buf.String())
	}
}

// TestMicrossegundosArredonda protege contra latencias submicrossegundo
// virarem zero no arquivo bruto, o que seria lido como medicao instantanea.
func TestMicrossegundosArredonda(t *testing.T) {
	casos := map[time.Duration]int64{
		0:                      0,
		400 * time.Nanosecond:  0,
		600 * time.Nanosecond:  1,
		1500 * time.Nanosecond: 2,
		time.Millisecond:       1000,
	}
	for d, esperado := range casos {
		if got := microssegundos(d); got != esperado {
			t.Errorf("microssegundos(%v) = %d, esperado %d", d, got, esperado)
		}
	}
}
