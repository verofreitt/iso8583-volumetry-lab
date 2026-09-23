// Command massa gera a massa sintetica de transacoes do experimento.
//
// O CSV resultante e versionado no repositorio e e a fonte de verdade das
// rodadas. Este gerador existe para documentar o metodo, nao para ser
// executado antes de cada experimento.
//
// A distincao importa para a reprodutibilidade. Reproduzir a massa a partir da
// semente depende do comportamento do gerador pseudoaleatorio da biblioteca
// padrao, que mudou entre versoes do Go: a 1.20 passou a semear as funcoes
// globais automaticamente e depreciou rand.Seed, a 1.22 adotou o ChaCha8 como
// padrao dessas funcoes, e o math/rand/v2 removeu o gerador da Go 1 por
// inteiro. Quem replicar o trabalho pega o CSV commitado e obtem exatamente os
// mesmos bytes, sem depender de nada disso.
//
// Ainda assim o gerador usa fonte explicita, nunca as funcoes globais, para
// que reexecuta-lo na mesma versao do Go produza o mesmo arquivo.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/massa"
)

// Conjuntos declarados. Sao pequenos e fixos de proposito: o artigo precisa
// enunciar de onde cada campo sai, e um conjunto grande nao acrescentaria
// nada ao que o experimento investiga, que e desempenho sob volumetria.
var (
	// processing codes: compra a vista e saque. Primeiros dois digitos sao o
	// tipo de transacao, os quatro seguintes identificam as contas.
	processingCodes = []string{"000000", "010000"}

	// MCC segundo a ISO 18245, norma publica. Nenhum deles e especifico de
	// bandeira.
	mccs = []string{
		"5411", // supermercados
		"5812", // restaurantes
		"5541", // postos de combustivel
		"5912", // farmacias
		"5999", // varejo diverso
		"4111", // transporte de passageiros
	}

	// POS entry mode: os dois primeiros digitos sao a forma de captura, o
	// terceiro a capacidade de PIN.
	posEntryModes = []string{
		"010", // digitado
		"020", // tarja magnetica
		"051", // chip com PIN
		"071", // aproximacao
		"810", // comercio eletronico
	}

	// instituicoes adquirentes, identificadores sinteticos
	adquirentes = []string{"000001", "000002", "000003", "000004", "000005"}
)

const (
	// moeda: 986 e o real brasileiro na ISO 4217
	moeda = "986"

	// terminais distintos na massa
	terminais = 500

	// Parametros da distribuicao do valor da transacao, em centavos.
	//
	// Lognormal e a escolha usual para valor de transacao: positiva por
	// construcao e assimetrica a direita, com muitas compras pequenas e poucas
	// grandes. A mediana e exp(mu), entao mu = ln(5000) fixa a mediana em
	// R$ 50,00. Sigma de 1,2 produz um quartil superior proximo de R$ 112 e
	// uma cauda que alcanca alguns milhares de reais.
	//
	// Os valores sao truncados a faixa declarada, que representa os limites
	// praticos de uma autorizacao de varejo. O truncamento corta a cauda da
	// lognormal e precisa constar do artigo.
	valorMedianaCentavos = 5000
	valorSigma           = 1.2
	valorMinimoCentavos  = 100
	valorMaximoCentavos  = 1000000
)

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	semente := flag.Int64("seed", 1, "semente da geracao")
	linhas := flag.Int("linhas", 50000, "numero de transacoes")
	destino := flag.String("out", filepath.Join("data", "massa.csv"), "arquivo de saida")
	flag.Parse()

	if *linhas < 1 {
		log.Fatalf("linhas deve ser positivo, recebido %d", *linhas)
	}

	transacoes := gerar(*semente, *linhas)

	if err := os.MkdirAll(filepath.Dir(*destino), 0o755); err != nil {
		log.Fatalf("criando o diretorio de %s: %v", *destino, err)
	}
	f, err := os.Create(*destino)
	if err != nil {
		log.Fatalf("criando %s: %v", *destino, err)
	}
	if err := massa.Escrever(f, transacoes); err != nil {
		f.Close()
		log.Fatalf("%v", err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("fechando %s: %v", *destino, err)
	}

	resumir(os.Stdout, *destino, *semente, transacoes)
}

// gerar produz as transacoes a partir da semente.
func gerar(semente int64, linhas int) []massa.Transacao {
	r := rand.New(rand.NewSource(semente))

	transacoes := make([]massa.Transacao, 0, linhas)
	for i := 0; i < linhas; i++ {
		transacoes = append(transacoes, massa.Transacao{
			ID:             fmt.Sprintf("%d", i+1),
			PAN:            gerarPAN(r),
			ProcessingCode: processingCodes[r.Intn(len(processingCodes))],
			Valor:          massa.FormatarValor(gerarValor(r)),
			MCC:            mccs[r.Intn(len(mccs))],
			POSEntryMode:   posEntryModes[r.Intn(len(posEntryModes))],
			Adquirente:     adquirentes[r.Intn(len(adquirentes))],
			TerminalID:     fmt.Sprintf("TERM%04d", r.Intn(terminais)+1),
			Moeda:          moeda,
		})
	}
	return transacoes
}

// gerarPAN produz um PAN de 16 digitos valido por Luhn.
//
// O prefixo 9 vem do ISO/IEC 7812, que reserva o Major Industry Identifier 9
// para atribuicao nacional — faixa nao alocada a nenhum esquema internacional
// de cartoes. Isso garante que nenhum BIN real em uso seja emitido.
func gerarPAN(r *rand.Rand) string {
	digitos := make([]byte, 15)
	digitos[0] = '9'
	for i := 1; i < len(digitos); i++ {
		digitos[i] = byte('0' + r.Intn(10))
	}

	parcial := string(digitos)
	return parcial + fmt.Sprintf("%d", massa.DigitoLuhn(parcial))
}

// gerarValor sorteia o valor da transacao em centavos.
func gerarValor(r *rand.Rand) int64 {
	mu := math.Log(valorMedianaCentavos)

	// rejeita fora da faixa em vez de saturar nos extremos: saturar criaria
	// picos artificiais no minimo e no maximo, visiveis na analise
	for tentativas := 0; tentativas < 100; tentativas++ {
		v := int64(math.Round(math.Exp(mu + valorSigma*r.NormFloat64())))
		if v >= valorMinimoCentavos && v <= valorMaximoCentavos {
			return v
		}
	}
	return valorMedianaCentavos
}

// resumir descreve a massa gerada, para conferencia e para o artigo.
func resumir(w *os.File, destino string, semente int64, transacoes []massa.Transacao) {
	var (
		soma    int64
		minimo  int64 = math.MaxInt64
		maximo  int64
		valores = make([]int64, 0, len(transacoes))
		pans    = map[string]bool{}
	)
	for _, t := range transacoes {
		v, _ := massa.ValorEmCentavos(t.Valor)
		soma += v
		valores = append(valores, v)
		if v < minimo {
			minimo = v
		}
		if v > maximo {
			maximo = v
		}
		pans[t.PAN] = true
	}

	sort.Slice(valores, func(i, j int) bool { return valores[i] < valores[j] })
	mediana := valores[len(valores)/2]
	p95 := valores[int(float64(len(valores))*0.95)]

	fmt.Fprintf(w, "massa gravada em %s\n", destino)
	fmt.Fprintf(w, "semente          : %d\n", semente)
	fmt.Fprintf(w, "transacoes       : %d\n", len(transacoes))
	fmt.Fprintf(w, "PANs distintos   : %d\n", len(pans))
	fmt.Fprintf(w, "valor minimo     : R$ %.2f\n", float64(minimo)/100)
	fmt.Fprintf(w, "valor mediana    : R$ %.2f\n", float64(mediana)/100)
	fmt.Fprintf(w, "valor p95        : R$ %.2f\n", float64(p95)/100)
	fmt.Fprintf(w, "valor maximo     : R$ %.2f\n", float64(maximo)/100)
	fmt.Fprintf(w, "valor medio      : R$ %.2f\n", float64(soma)/float64(len(transacoes))/100)
}
