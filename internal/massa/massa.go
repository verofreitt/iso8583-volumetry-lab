// Package massa le e valida a massa sintetica de transacoes.
//
// A massa e um unico CSV, uma linha por transacao, com todas as colunas. Nao
// ha normalizacao em tabelas separadas por duas razoes.
//
// A primeira e de desempenho: normalizar exigiria juncao em tempo de execucao,
// o que poria trabalho extra no caminho critico do injetor — justamente onde o
// aparato ja esta no limite por temporizacao.
//
// A segunda e que a variedade combinatoria se obtem gerando linhas distintas.
// Nao e preciso tres tabelas para isso.
//
// STAN, RRN e os campos de data e hora NAO fazem parte da massa: sao gerados
// por requisicao no instante do envio. O STAN precisa ser unico dentro da
// rodada para correlacionar requisicao e resposta, e os campos temporais
// precisam refletir o instante de chegada pretendido.
package massa

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"strings"
)

// Colunas e o cabecalho do CSV de massa, na ordem em que aparece no arquivo.
var Colunas = []string{
	"id",
	"pan",
	"processing_code",
	"amount",
	"mcc",
	"pos_entry_mode",
	"acquirer_id",
	"terminal_id",
	"currency",
}

// Transacao e uma linha da massa.
//
// Os campos de largura fixa ja vem na largura exata: o spec ISO 8583 do
// experimento nao declara padding, e um valor de largura incorreta falharia no
// Pack no meio da rodada. A validacao ocorre na leitura, antes de qualquer
// medicao.
type Transacao struct {
	ID             string // identificador da linha, para rastrear ate o CSV
	PAN            string // DE 2, LLVAR ate 19
	ProcessingCode string // DE 3, exatamente 6
	Valor          string // DE 4, exatamente 12
	MCC            string // DE 18, exatamente 4
	POSEntryMode   string // DE 22, exatamente 3
	Adquirente     string // DE 32, LLVAR ate 11
	TerminalID     string // DE 41, exatamente 8
	Moeda          string // DE 49, exatamente 3
}

func (t Transacao) linha() []string {
	return []string{
		t.ID, t.PAN, t.ProcessingCode, t.Valor,
		t.MCC, t.POSEntryMode, t.Adquirente, t.TerminalID, t.Moeda,
	}
}

// Validar confere larguras e conteudo de uma transacao.
//
// A validacao e feita na carga, e nao no envio, porque uma falha no meio da
// rodada custaria a medicao inteira e so apareceria como erro de transporte.
func (t Transacao) Validar() error {
	fixos := []struct {
		nome    string
		valor   string
		largura int
	}{
		{"processing_code", t.ProcessingCode, 6},
		{"amount", t.Valor, 12},
		{"mcc", t.MCC, 4},
		{"pos_entry_mode", t.POSEntryMode, 3},
		{"terminal_id", t.TerminalID, 8},
		{"currency", t.Moeda, 3},
	}
	for _, c := range fixos {
		if len(c.valor) != c.largura {
			return fmt.Errorf("%s = %q tem %d caracteres, esperado exatamente %d",
				c.nome, c.valor, len(c.valor), c.largura)
		}
	}

	variaveis := []struct {
		nome   string
		valor  string
		maximo int
	}{
		{"pan", t.PAN, 19},
		{"acquirer_id", t.Adquirente, 11},
	}
	for _, c := range variaveis {
		if len(c.valor) == 0 || len(c.valor) > c.maximo {
			return fmt.Errorf("%s = %q tem %d caracteres, esperado entre 1 e %d",
				c.nome, c.valor, len(c.valor), c.maximo)
		}
	}

	numericos := []struct {
		nome  string
		valor string
	}{
		{"pan", t.PAN}, {"processing_code", t.ProcessingCode}, {"amount", t.Valor},
		{"mcc", t.MCC}, {"pos_entry_mode", t.POSEntryMode},
		{"acquirer_id", t.Adquirente}, {"currency", t.Moeda},
	}
	for _, c := range numericos {
		if !somenteDigitos(c.valor) {
			return fmt.Errorf("%s = %q deveria conter apenas digitos", c.nome, c.valor)
		}
	}

	if !ValidoLuhn(t.PAN) {
		return fmt.Errorf("pan = %q nao e valido por Luhn", t.PAN)
	}
	if !strings.HasPrefix(t.PAN, "9") {
		return fmt.Errorf("pan = %q deveria comecar por 9: o ISO/IEC 7812 reserva o MII 9 "+
			"para atribuicao nacional, faixa nao alocada a esquemas internacionais de cartoes", t.PAN)
	}

	return nil
}

func somenteDigitos(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// DigitoLuhn calcula o digito verificador de um numero parcial.
func DigitoLuhn(parcial string) int {
	soma, dobrar := 0, true
	for i := len(parcial) - 1; i >= 0; i-- {
		d := int(parcial[i] - '0')
		if dobrar {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		soma += d
		dobrar = !dobrar
	}
	return (10 - soma%10) % 10
}

// ValidoLuhn confere o digito verificador de um numero completo.
func ValidoLuhn(numero string) bool {
	if len(numero) < 2 || !somenteDigitos(numero) {
		return false
	}
	return DigitoLuhn(numero[:len(numero)-1]) == int(numero[len(numero)-1]-'0')
}

// Massa e o conjunto de transacoes carregado, com uma ordem de consumo.
type Massa struct {
	transacoes []Transacao
	ordem      []int
}

// Ler carrega e valida o CSV de massa.
func Ler(caminho string) (*Massa, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, fmt.Errorf("abrindo a massa em %s: %w "+
			"(gere-a com: go run ./cmd/massa)", caminho, err)
	}
	defer f.Close()

	return LerDe(f)
}

// LerDe carrega e valida a massa a partir de um leitor.
func LerDe(r io.Reader) (*Massa, error) {
	leitor := csv.NewReader(r)
	leitor.FieldsPerRecord = len(Colunas)

	registros, err := leitor.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("lendo o CSV de massa: %w", err)
	}
	if len(registros) == 0 {
		return nil, fmt.Errorf("CSV de massa vazio")
	}

	for i, coluna := range Colunas {
		if registros[0][i] != coluna {
			return nil, fmt.Errorf("coluna %d = %q, esperado %q", i, registros[0][i], coluna)
		}
	}
	if len(registros) == 1 {
		return nil, fmt.Errorf("CSV de massa sem linhas de dados")
	}

	transacoes := make([]Transacao, 0, len(registros)-1)
	for n, reg := range registros[1:] {
		t := Transacao{
			ID: reg[0], PAN: reg[1], ProcessingCode: reg[2], Valor: reg[3],
			MCC: reg[4], POSEntryMode: reg[5], Adquirente: reg[6],
			TerminalID: reg[7], Moeda: reg[8],
		}
		if err := t.Validar(); err != nil {
			return nil, fmt.Errorf("linha %d da massa: %w", n+2, err)
		}
		transacoes = append(transacoes, t)
	}

	m := &Massa{transacoes: transacoes, ordem: make([]int, len(transacoes))}
	for i := range m.ordem {
		m.ordem[i] = i
	}
	return m, nil
}

// Embaralhar define a ordem de consumo a partir da semente.
//
// A semente governa apenas a ordem: a massa em si e fixa e versionada no
// repositorio. Duas rodadas com a mesma semente consomem as mesmas transacoes
// na mesma sequencia.
//
// O gerador e explicito, nunca as funcoes globais do math/rand. As globais
// mudaram de comportamento entre versoes do Go — a partir da 1.20 passaram a
// ser semeadas automaticamente e rand.Seed foi depreciada, e a 1.22 adotou o
// ChaCha8 como gerador padrao. Depender delas quebraria a reprodutibilidade em
// silencio numa troca de toolchain.
func (m *Massa) Embaralhar(semente int64) {
	r := rand.New(rand.NewSource(semente))
	r.Shuffle(len(m.ordem), func(i, j int) {
		m.ordem[i], m.ordem[j] = m.ordem[j], m.ordem[i]
	})
}

// Em devolve a transacao da i-esima chegada.
//
// O consumo e ciclico: uma rodada mais longa que a massa reaproveita as
// transacoes na mesma ordem. O reuso e normal e fica declarado no artigo.
func (m *Massa) Em(i int) Transacao {
	if len(m.transacoes) == 0 {
		return Transacao{}
	}
	if i < 0 {
		i = -i
	}
	return m.transacoes[m.ordem[i%len(m.ordem)]]
}

// Tamanho devolve quantas transacoes a massa contem.
func (m *Massa) Tamanho() int {
	return len(m.transacoes)
}

// Escrever grava a massa em formato CSV, com cabecalho.
func Escrever(w io.Writer, transacoes []Transacao) error {
	escritor := csv.NewWriter(w)

	if err := escritor.Write(Colunas); err != nil {
		return fmt.Errorf("escrevendo cabecalho: %w", err)
	}
	for i, t := range transacoes {
		if err := escritor.Write(t.linha()); err != nil {
			return fmt.Errorf("escrevendo linha %d: %w", i+2, err)
		}
	}

	escritor.Flush()
	return escritor.Error()
}

// FormatarValor converte centavos no formato n12 do DE 4.
func FormatarValor(centavos int64) string {
	return fmt.Sprintf("%012d", centavos)
}

// ValorEmCentavos interpreta o DE 4.
func ValorEmCentavos(valor string) (int64, error) {
	return strconv.ParseInt(valor, 10, 64)
}
