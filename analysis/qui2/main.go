// Command qui2 testa a independencia entre recusa e um atributo da transacao.
//
// Cruza o raw.csv de uma rodada com a massa de entrada pela coluna massa_id,
// monta a tabela de contingencia recusa x atributo e aplica o teste de
// independencia de Pearson.
//
// E a analise do controle positivo descrito na secao 4.1 do CLAUDE.md: com o
// alvo em recusa uniforme, a independencia nao deve ser rejeitada; com
// --decline-bias, deve ser rejeitada e a taxa injetada deve ser recuperada
// dentro do intervalo de confianca.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/massa"
)

const alfa = 0.05

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	bruto := flag.String("raw", "", "raw.csv da rodada")
	entrada := flag.String("massa", "data/massa.csv", "CSV da massa de entrada")
	atributo := flag.String("atributo", "mcc", "atributo a testar")
	saida := flag.String("json", "", "grava o resultado tambem em JSON neste caminho")
	flag.Parse()

	if *bruto == "" {
		log.Fatalf("informe -raw com o raw.csv da rodada")
	}

	rel, err := analisar(*bruto, *entrada, *atributo)
	if err != nil {
		log.Fatalf("%v", err)
	}

	rel.imprimir(os.Stdout)

	if *saida != "" {
		if err := gravarJSON(*saida, rel); err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Fprintf(os.Stdout, "\nresultado em %s\n", *saida)
	}
}

// LinhaValor e o desfecho de um valor do atributo.
type LinhaValor struct {
	Valor      string  `json:"valor"`
	Total      int     `json:"total"`
	Recusas    int     `json:"recusas"`
	TaxaRecusa float64 `json:"taxa_recusa"`
	ICInferior float64 `json:"ic95_inferior"`
	ICSuperior float64 `json:"ic95_superior"`
}

// Relatorio e o resultado da analise.
type Relatorio struct {
	Bruto            string       `json:"raw"`
	Massa            string       `json:"massa"`
	Atributo         string       `json:"atributo"`
	Requisicoes      int          `json:"requisicoes"`
	Ignoradas        int          `json:"ignoradas_por_falha_de_transporte"`
	TaxaRecusaGeral  float64      `json:"taxa_recusa_geral"`
	Valores          []LinhaValor `json:"valores"`
	Estatistica      float64      `json:"qui2"`
	GrausDeLiberdade int          `json:"graus_de_liberdade"`
	PValor           float64      `json:"p_valor"`
	EsperadoMinimo   float64      `json:"frequencia_esperada_minima"`
	Alfa             float64      `json:"alfa"`
	Rejeitada        bool         `json:"independencia_rejeitada"`
	Avisos           []string     `json:"avisos,omitempty"`
}

func analisar(bruto, entrada, atributo string) (Relatorio, error) {
	m, err := massa.Ler(entrada)
	if err != nil {
		return Relatorio{}, err
	}

	porID, err := indexarMassa(entrada)
	if err != nil {
		return Relatorio{}, err
	}
	_ = m // a leitura acima valida a massa antes de qualquer cruzamento

	f, err := os.Open(bruto)
	if err != nil {
		return Relatorio{}, fmt.Errorf("abrindo %s: %w", bruto, err)
	}
	defer f.Close()

	leitor := csv.NewReader(f)
	cabecalho, err := leitor.Read()
	if err != nil {
		return Relatorio{}, fmt.Errorf("lendo o cabecalho de %s: %w", bruto, err)
	}

	col := map[string]int{}
	for i, c := range cabecalho {
		col[c] = i
	}
	for _, exigida := range []string{"massa_id", "de39", "erro_transporte"} {
		if _, ok := col[exigida]; !ok {
			return Relatorio{}, fmt.Errorf("%s nao tem a coluna %q", bruto, exigida)
		}
	}

	recusas := map[string]int{}
	totais := map[string]int{}
	var requisicoes, ignoradas int

	for {
		reg, err := leitor.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Relatorio{}, fmt.Errorf("lendo %s: %w", bruto, err)
		}

		// falhas de transporte nao sao desfecho de negocio e nao entram na
		// tabela: misturar as duas coisas invalidaria a analise
		if reg[col["erro_transporte"]] != "" {
			ignoradas++
			continue
		}

		t, ok := porID[reg[col["massa_id"]]]
		if !ok {
			return Relatorio{}, fmt.Errorf("massa_id %q do raw.csv nao existe em %s",
				reg[col["massa_id"]], entrada)
		}

		valor, ok := valorDoAtributo(t, atributo)
		if !ok {
			return Relatorio{}, fmt.Errorf("atributo %q nao existe na massa", atributo)
		}

		requisicoes++
		totais[valor]++
		if reg[col["de39"]] != "00" {
			recusas[valor]++
		}
	}

	if requisicoes == 0 {
		return Relatorio{}, fmt.Errorf("nenhuma requisicao com desfecho de negocio em %s", bruto)
	}

	valores := make([]string, 0, len(totais))
	for v := range totais {
		valores = append(valores, v)
	}
	sort.Strings(valores)

	tab := Tabela{Valores: valores}
	rel := Relatorio{
		Bruto: bruto, Massa: entrada, Atributo: atributo,
		Requisicoes: requisicoes, Ignoradas: ignoradas, Alfa: alfa,
	}

	var totalRecusas int
	for _, v := range valores {
		r, n := recusas[v], totais[v]
		totalRecusas += r

		inf, sup := Wilson(r, n)
		rel.Valores = append(rel.Valores, LinhaValor{
			Valor: v, Total: n, Recusas: r,
			TaxaRecusa: float64(r) / float64(n),
			ICInferior: inf, ICSuperior: sup,
		})

		tab.Recusas = append(tab.Recusas, r)
		tab.Aprovadas = append(tab.Aprovadas, n-r)
	}
	rel.TaxaRecusaGeral = float64(totalRecusas) / float64(requisicoes)

	res, err := Qui2(tab)
	if err != nil {
		return Relatorio{}, err
	}
	rel.Estatistica = res.Estatistica
	rel.GrausDeLiberdade = res.GrausDeLiberdade
	rel.PValor = res.PValor
	rel.EsperadoMinimo = res.EsperadoMinimo
	rel.Rejeitada = res.PValor < alfa

	if res.EsperadoMinimo < 5 {
		rel.Avisos = append(rel.Avisos, fmt.Sprintf(
			"frequencia esperada minima de %.1f, abaixo de 5: o p-valor perde confiabilidade",
			res.EsperadoMinimo))
	}

	return rel, nil
}

// indexarMassa carrega a massa em um mapa por id.
func indexarMassa(caminho string) (map[string]massa.Transacao, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, fmt.Errorf("abrindo %s: %w", caminho, err)
	}
	defer f.Close()

	leitor := csv.NewReader(f)
	registros, err := leitor.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("lendo %s: %w", caminho, err)
	}

	porID := make(map[string]massa.Transacao, len(registros))
	for _, reg := range registros[1:] {
		porID[reg[0]] = massa.Transacao{
			ID: reg[0], PAN: reg[1], ProcessingCode: reg[2], Valor: reg[3],
			MCC: reg[4], POSEntryMode: reg[5], Adquirente: reg[6],
			TerminalID: reg[7], Moeda: reg[8],
		}
	}
	return porID, nil
}

func valorDoAtributo(t massa.Transacao, atributo string) (string, bool) {
	switch atributo {
	case "mcc":
		return t.MCC, true
	case "pos_entry_mode":
		return t.POSEntryMode, true
	case "acquirer_id":
		return t.Adquirente, true
	case "terminal_id":
		return t.TerminalID, true
	case "processing_code":
		return t.ProcessingCode, true
	case "currency":
		return t.Moeda, true
	case "pan":
		return t.PAN, true
	default:
		return "", false
	}
}

func (r Relatorio) imprimir(w io.Writer) {
	fmt.Fprintf(w, "\n=== independencia entre recusa e %s ===\n\n", r.Atributo)
	fmt.Fprintf(w, "rodada     : %s\n", r.Bruto)
	fmt.Fprintf(w, "requisicoes: %d com desfecho de negocio", r.Requisicoes)
	if r.Ignoradas > 0 {
		fmt.Fprintf(w, ", %d ignoradas por falha de transporte", r.Ignoradas)
	}
	fmt.Fprintf(w, "\ntaxa geral : %.4f\n\n", r.TaxaRecusaGeral)

	fmt.Fprintf(w, "%-16s %8s %8s %10s %20s\n", r.Atributo, "n", "recusas", "taxa", "IC 95%")
	for _, v := range r.Valores {
		fmt.Fprintf(w, "%-16s %8d %8d %10.4f   [%.4f, %.4f]\n",
			v.Valor, v.Total, v.Recusas, v.TaxaRecusa, v.ICInferior, v.ICSuperior)
	}

	fmt.Fprintf(w, "\nqui-quadrado : %.4f com %d graus de liberdade\n", r.Estatistica, r.GrausDeLiberdade)
	fmt.Fprintf(w, "p-valor      : %s\n", formatarP(r.PValor))
	fmt.Fprintf(w, "alfa         : %.2f\n", r.Alfa)

	fmt.Fprintf(w, "\nconclusao    : ")
	if r.Rejeitada {
		fmt.Fprintf(w, "independencia REJEITADA\n")
		fmt.Fprintf(w, "               a taxa de recusa depende de %s\n", r.Atributo)
	} else {
		fmt.Fprintf(w, "independencia NAO rejeitada\n")
		fmt.Fprintf(w, "               nao ha evidencia de que a recusa dependa de %s\n", r.Atributo)
	}

	for _, aviso := range r.Avisos {
		fmt.Fprintf(w, "\nAVISO: %s\n", aviso)
	}
}

func formatarP(p float64) string {
	if p < 1e-12 {
		return "< 1e-12"
	}
	return fmt.Sprintf("%.6g", p)
}

func gravarJSON(caminho string, r Relatorio) error {
	f, err := os.Create(caminho)
	if err != nil {
		return fmt.Errorf("criando %s: %w", caminho, err)
	}
	defer f.Close()

	c := json.NewEncoder(f)
	c.SetIndent("", "  ")
	if err := c.Encode(r); err != nil {
		return fmt.Errorf("gravando %s: %w", caminho, err)
	}
	return f.Close()
}
