package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Atributos que aceitam vies de recusa.
//
// Os nomes sao os das colunas de data/massa.csv, para que o vocabulario da
// flag, da massa e da analise seja o mesmo. Cada um mapeia para o data element
// correspondente da requisicao.
var atributosViesaveis = map[string]int{
	"processing_code": 3,
	"mcc":             18,
	"pos_entry_mode":  22,
	"acquirer_id":     32,
	"terminal_id":     41,
	"currency":        49,
}

// DEDoAtributo devolve o data element de um atributo viesavel.
func DEDoAtributo(atributo string) (int, bool) {
	de, ok := atributosViesaveis[atributo]
	return de, ok
}

// Vies e uma regra estatica que substitui a taxa de recusa base para as
// transacoes cujo atributo tem o valor informado.
//
// A regra nao e logica de negocio: e fixa, declarada de antemao, nao guarda
// estado entre requisicoes e nao muda com a carga. Ver secao 4.1 do CLAUDE.md.
//
// Existe para servir de verdade fundamental num controle positivo. Sem um
// sinal conhecido no alvo, uma medicao que nao encontra padrao nao distingue
// "nao ha padrao" de "o instrumento nao detecta padrao".
type Vies struct {
	Atributo string
	Valor    string
	Taxa     float64
}

// parsearVieses interpreta "mcc=5967:0.40,pos_entry_mode=810:0.25".
//
// A ordem de declaracao e preservada: o primeiro vies que casar com a
// transacao prevalece. Manter a ordem torna o resultado independente de
// iteracao de mapa, que em Go nao e deterministica.
func parsearVieses(spec string) ([]Vies, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	var vieses []Vies
	vistos := map[string]bool{}

	for _, parte := range strings.Split(spec, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" {
			continue
		}

		atributo, resto, ok := strings.Cut(parte, "=")
		if !ok {
			return nil, fmt.Errorf("entrada %q de decline-bias deve ter a forma atributo=valor:taxa", parte)
		}
		valor, taxaTexto, ok := strings.Cut(resto, ":")
		if !ok {
			return nil, fmt.Errorf("entrada %q de decline-bias deve ter a forma atributo=valor:taxa", parte)
		}

		atributo = strings.TrimSpace(atributo)
		if _, conhecido := atributosViesaveis[atributo]; !conhecido {
			return nil, fmt.Errorf("atributo %q nao aceita vies; os aceitos sao %s",
				atributo, strings.Join(atributosAceitos(), ", "))
		}

		valor = strings.TrimSpace(valor)
		if valor == "" {
			return nil, fmt.Errorf("entrada %q de decline-bias tem valor vazio", parte)
		}

		chave := atributo + "=" + valor
		if vistos[chave] {
			return nil, fmt.Errorf("vies repetido para %s", chave)
		}
		vistos[chave] = true

		taxa, err := strconv.ParseFloat(strings.TrimSpace(taxaTexto), 64)
		if err != nil {
			return nil, fmt.Errorf("taxa de %s em decline-bias: %w", chave, err)
		}
		if taxa < 0 || taxa > 1 {
			return nil, fmt.Errorf("taxa de %s deve estar entre 0 e 1, recebida %v", chave, taxa)
		}

		vieses = append(vieses, Vies{Atributo: atributo, Valor: valor, Taxa: taxa})
	}

	return vieses, nil
}

func atributosAceitos() []string {
	nomes := make([]string, 0, len(atributosViesaveis))
	for n := range atributosViesaveis {
		nomes = append(nomes, n)
	}
	// ordem estavel para que a mensagem de erro nao varie entre execucoes
	for i := 1; i < len(nomes); i++ {
		for j := i; j > 0 && nomes[j] < nomes[j-1]; j-- {
			nomes[j], nomes[j-1] = nomes[j-1], nomes[j]
		}
	}
	return nomes
}

// LeitorAtributo devolve o valor de um atributo da requisicao corrente.
//
// E consultado apenas para os atributos que tem vies configurado: sem vieses,
// nenhuma leitura ocorre e o caminho critico fica inalterado.
type LeitorAtributo func(atributo string) string

// taxaDeRecusa devolve a taxa aplicavel a requisicao corrente.
func (c *Comportamento) taxaDeRecusa(ler LeitorAtributo) float64 {
	base := 1 - c.aprovacao
	if len(c.vieses) == 0 || ler == nil {
		return base
	}

	for _, v := range c.vieses {
		if ler(v.Atributo) == v.Valor {
			return v.Taxa
		}
	}
	return base
}
