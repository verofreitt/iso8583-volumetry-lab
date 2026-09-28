package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// Config reune os parametros do autorizador mock.
//
// Os nomes em JSON sao os das flags, para que o bloco gravado no summary.json
// do injetor seja lido sem traducao.
type Config struct {
	LatenciaBase   time.Duration `json:"latency-base"`
	LatenciaJitter time.Duration `json:"latency-jitter"`
	LatenciaDist   string        `json:"latency-dist"`
	TaxaAprovacao  float64       `json:"approval-rate"`
	DistRecusas    string        `json:"decline-dist"`
	ViesRecusa     string        `json:"decline-bias"`
	MaxConns       int           `json:"max-conns"`
	Semente        int64         `json:"seed"`
	EcoApenas      bool          `json:"echo-only"`
}

// Ambiente do processo autorizador.
//
// A secao 7 do CLAUDE.md exige poder fixar GOMAXPROCS de cada processo
// separadamente, porque injetor e autorizador disputam CPU na mesma maquina.
// O valor efetivo precisa constar do resultado.
type Ambiente struct {
	VersaoGo       string `json:"versao_go"`
	GOMAXPROCS     int    `json:"gomaxprocs"`
	NumCPU         int    `json:"num_cpu"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	GOGC           string `json:"gogc"`
	LinhaDeComando string `json:"linha_de_comando"`
}

// Registro e o conteudo do arquivo escrito por --config-out.
type Registro struct {
	Config   Config   `json:"config"`
	Ambiente Ambiente `json:"ambiente"`
}

// MarshalJSON escreve as duracoes de forma legivel, em vez dos nanossegundos
// que o encoding/json usaria para time.Duration.
func (c Config) MarshalJSON() ([]byte, error) {
	type alias Config
	return json.Marshal(struct {
		alias
		LatenciaBase   string `json:"latency-base"`
		LatenciaJitter string `json:"latency-jitter"`
	}{
		alias:          alias(c),
		LatenciaBase:   c.LatenciaBase.String(),
		LatenciaJitter: c.LatenciaJitter.String(),
	})
}

func capturarAmbiente() Ambiente {
	gogc := os.Getenv("GOGC")
	if gogc == "" {
		gogc = "100 (padrao, nao definido no ambiente)"
	}

	return Ambiente{
		VersaoGo:       runtime.Version(),
		GOMAXPROCS:     runtime.GOMAXPROCS(0),
		NumCPU:         runtime.NumCPU(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		GOGC:           gogc,
		LinhaDeComando: strings.Join(os.Args, " "),
	}
}

// escreverConfig grava a configuracao e o ambiente do autorizador.
//
// O arquivo existe para que o injetor possa embutir a configuracao do sistema
// sob teste no summary.json sem que ninguem precise transcrever flags a mao. A
// secao 5.4 do CLAUDE.md exige as flags dos dois processos no resultado, e uma
// transcricao manual seria a parte mais fragil da cadeia de auditoria.
//
// A troca passa por arquivo, e nao pela rede: consultar o autorizador durante a
// rodada acrescentaria um caminho de codigo ao sistema sob teste, e o
// experimento depende de esse caminho ser o mais simples possivel.
func escreverConfig(caminho string, c Config) error {
	f, err := os.Create(caminho)
	if err != nil {
		return fmt.Errorf("criando %s: %w", caminho, err)
	}
	defer f.Close()

	codificador := json.NewEncoder(f)
	codificador.SetIndent("", "  ")
	if err := codificador.Encode(Registro{Config: c, Ambiente: capturarAmbiente()}); err != nil {
		return fmt.Errorf("gravando %s: %w", caminho, err)
	}

	return f.Close()
}
