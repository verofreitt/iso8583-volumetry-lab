// Command calibrate descobre os limites do proprio aparato, antes de qualquer
// experimento.
//
// A secao 7.2 do CLAUDE.md exige esta etapa: se o injetor satura em 800 TPS, o
// experimento de 1000 TPS mede o injetor e nao o autorizador, e descobrir isso
// depois de rodar tudo e o pior cenario possivel.
//
// O procedimento varre uma faixa de taxas contra o autorizador em --echo-only,
// que responde imediatamente e sem sorteio. O que sobra de latencia e atraso e
// do aparato: injetor, pilha de rede local, escalonador do sistema operacional
// e coletor de lixo dos dois processos.
//
// Cada rodada usa processos novos, iniciados e encerrados pelo proprio
// programa, para que nada seja herdado da rodada anterior.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/metrics"
)

const endereco = "127.0.0.1:8583"

type opcoes struct {
	niveis      string
	repeticoes  int
	duracao     time.Duration
	warmup      time.Duration
	conexoes    int
	destino     string
	limiarVazao float64

	gomaxprocsInjetor     int
	gomaxprocsAutorizador int
	gogc                  string
}

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetOutput(os.Stderr)

	var o opcoes
	flag.StringVar(&o.niveis, "levels", "100,250,500,1000,1500,2000,3000,5000", "taxas a varrer, separadas por virgula")
	flag.IntVar(&o.repeticoes, "reps", 3, "repeticoes por nivel")
	flag.DurationVar(&o.duracao, "duration", 15*time.Second, "duracao de cada rodada, incluindo o warm-up")
	flag.DurationVar(&o.warmup, "warmup", 5*time.Second, "warm-up de cada rodada")
	flag.IntVar(&o.conexoes, "conns", 32, "conexoes do pool do injetor")
	flag.StringVar(&o.destino, "out", "results", "raiz onde a pasta da calibracao e criada")
	flag.Float64Var(&o.limiarVazao, "throughput-threshold", 99, "percentual do alvo exigido para considerar o nivel sustentado")
	flag.IntVar(&o.gomaxprocsInjetor, "gomaxprocs-injector", 0, "GOMAXPROCS do injetor; 0 mantem o padrao")
	flag.IntVar(&o.gomaxprocsAutorizador, "gomaxprocs-authorizer", 0, "GOMAXPROCS do autorizador; 0 mantem o padrao")
	flag.StringVar(&o.gogc, "gogc", "", "GOGC imposto aos dois processos; vazio mantem o padrao")
	flag.Parse()

	if err := executar(o); err != nil {
		log.Fatalf("%v", err)
	}
}

func executar(o opcoes) error {
	niveis, err := parsearNiveis(o.niveis)
	if err != nil {
		return err
	}
	if o.repeticoes < 1 {
		return fmt.Errorf("reps deve ser ao menos 1, recebido %d", o.repeticoes)
	}
	if o.warmup >= o.duracao {
		return fmt.Errorf("warmup (%v) deve ser menor que a duracao (%v)", o.warmup, o.duracao)
	}

	inicio := time.Now()
	pasta := filepath.Join(o.destino, "calibracao-"+inicio.Format("20060102T150405"))
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", pasta, err)
	}

	binarios, err := compilar(pasta)
	if err != nil {
		return err
	}

	log.Printf("calibracao: %d niveis, %d repeticoes, %v por rodada", len(niveis), o.repeticoes, o.duracao)
	log.Printf("tempo estimado: %v", time.Duration(len(niveis)*o.repeticoes)*(o.duracao+3*time.Second))

	var medidos []Nivel
	for _, tps := range niveis {
		n := Nivel{TPS: tps}
		for rep := 1; rep <= o.repeticoes; rep++ {
			log.Printf("nivel %g TPS, repeticao %d de %d", tps, rep, o.repeticoes)

			resumo, err := rodada(binarios, o, pasta, tps, rep)
			if err != nil {
				return fmt.Errorf("nivel %g TPS, repeticao %d: %w", tps, rep, err)
			}
			n.Repeticoes = append(n.Repeticoes, resumir(resumo))
		}
		n.consolidar(o.limiarVazao)
		medidos = append(medidos, n)

		log.Printf("  atraso medio %d us (intervalo %d us), vazao %.1f%%, p99 servico %d us -> %s",
			n.AtrasoMedioUS, n.IntervaloUS, n.PercentualDoAlvo, n.P99ServicoUS, situacao(n.Sustentado))
	}

	rel := Relatorio{
		Procedimento: Procedimento{
			Niveis:      niveis,
			Repeticoes:  o.repeticoes,
			Duracao:     o.duracao.String(),
			Warmup:      o.warmup.String(),
			Conexoes:    o.conexoes,
			LimiarVazao: o.limiarVazao,
			Inicio:      inicio,
			Fim:         time.Now(),
			ModoAlvo:    "--echo-only",
		},
		Niveis:   medidos,
		Ambiente: ambiente(o),
	}
	rel.concluir()

	if err := escrever(filepath.Join(pasta, "calibracao.json"), rel); err != nil {
		return err
	}

	rel.imprimir(os.Stdout)
	fmt.Fprintf(os.Stdout, "\nrelatorio em %s\n", filepath.Join(pasta, "calibracao.json"))
	return nil
}

func situacao(sustentado bool) string {
	if sustentado {
		return "sustentado"
	}
	return "SATURADO"
}

// --- execucao de uma rodada ---

type caminhos struct {
	autorizador string
	injetor     string
}

// compilar constroi os dois binarios a partir do codigo corrente.
//
// A calibracao precisa medir exatamente a versao em maos; usar binarios
// deixados por uma compilacao anterior abriria a chance de calibrar uma versao
// diferente da que sera usada nos experimentos.
func compilar(pasta string) (caminhos, error) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	c := caminhos{
		autorizador: filepath.Join(pasta, "authorizer"+ext),
		injetor:     filepath.Join(pasta, "injector"+ext),
	}

	for destino, pacote := range map[string]string{
		c.autorizador: "./cmd/authorizer",
		c.injetor:     "./cmd/injector",
	} {
		cmd := exec.Command("go", "build", "-o", destino, pacote)
		saida, err := cmd.CombinedOutput()
		if err != nil {
			return caminhos{}, fmt.Errorf("compilando %s: %w: %s", pacote, err, saida)
		}
	}

	log.Printf("binarios compilados em %s", pasta)
	return c, nil
}

// rodada sobe um autorizador em modo eco, executa o injetor contra ele e
// devolve o resumo consolidado.
func rodada(b caminhos, o opcoes, pasta string, tps float64, rep int) (metrics.Resumo, error) {
	configAutorizador := filepath.Join(pasta, "autorizador.json")

	aut := exec.Command(b.autorizador, "--echo-only", "--quiet", "--config-out", configAutorizador)
	aut.Env = ambienteProcesso(o.gomaxprocsAutorizador, o.gogc)
	aut.Stderr = io.Discard
	if err := aut.Start(); err != nil {
		return metrics.Resumo{}, fmt.Errorf("iniciando o autorizador: %w", err)
	}
	defer func() {
		_ = aut.Process.Kill()
		_, _ = aut.Process.Wait()
	}()

	if err := aguardarEscuta(5 * time.Second); err != nil {
		return metrics.Resumo{}, err
	}

	rodadas := filepath.Join(pasta, "rodadas")
	inj := exec.Command(b.injetor,
		"-tps", strconv.FormatFloat(tps, 'g', -1, 64),
		"-duration", o.duracao.String(),
		"-warmup", o.warmup.String(),
		"-conns", strconv.Itoa(o.conexoes),
		"-rep", strconv.Itoa(rep),
		"-results", rodadas,
		"-sut-config", configAutorizador,
	)
	inj.Env = ambienteProcesso(o.gomaxprocsInjetor, o.gogc)
	inj.Stdout = io.Discard
	inj.Stderr = io.Discard
	if err := inj.Run(); err != nil {
		return metrics.Resumo{}, fmt.Errorf("executando o injetor: %w", err)
	}

	return ultimoResumo(rodadas)
}

// ambienteProcesso monta o ambiente de um processo filho, permitindo fixar
// GOMAXPROCS e GOGC separadamente para cada um.
//
// A secao 7 do CLAUDE.md pede isso explicitamente: injetor e autorizador
// disputam CPU na mesma maquina, e as pausas do coletor de lixo afetam a cauda
// dos dois.
func ambienteProcesso(gomaxprocs int, gogc string) []string {
	env := os.Environ()
	if gomaxprocs > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(gomaxprocs))
	}
	if gogc != "" {
		env = append(env, "GOGC="+gogc)
	}
	return env
}

func aguardarEscuta(prazo time.Duration) error {
	limite := time.Now().Add(prazo)
	for time.Now().Before(limite) {
		conn, err := net.DialTimeout("tcp", endereco, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("autorizador nao comecou a escutar em %s dentro de %v", endereco, prazo)
}

// ultimoResumo le o summary.json da pasta de rodada mais recente.
func ultimoResumo(raiz string) (metrics.Resumo, error) {
	entradas, err := os.ReadDir(raiz)
	if err != nil {
		return metrics.Resumo{}, fmt.Errorf("lendo %s: %w", raiz, err)
	}

	var pastas []string
	for _, e := range entradas {
		if e.IsDir() {
			pastas = append(pastas, e.Name())
		}
	}
	if len(pastas) == 0 {
		return metrics.Resumo{}, fmt.Errorf("nenhuma rodada encontrada em %s", raiz)
	}
	sort.Strings(pastas)

	caminho := filepath.Join(raiz, pastas[len(pastas)-1], "summary.json")
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return metrics.Resumo{}, fmt.Errorf("lendo %s: %w", caminho, err)
	}

	var r metrics.Resumo
	if err := json.Unmarshal(dados, &r); err != nil {
		return metrics.Resumo{}, fmt.Errorf("interpretando %s: %w", caminho, err)
	}
	return r, nil
}

func parsearNiveis(spec string) ([]float64, error) {
	var niveis []float64
	for _, parte := range strings.Split(spec, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" {
			continue
		}
		v, err := strconv.ParseFloat(parte, 64)
		if err != nil {
			return nil, fmt.Errorf("nivel %q: %w", parte, err)
		}
		if v <= 0 {
			return nil, fmt.Errorf("nivel %v deve ser positivo", v)
		}
		niveis = append(niveis, v)
	}
	if len(niveis) == 0 {
		return nil, fmt.Errorf("nenhum nivel informado")
	}
	sort.Float64s(niveis)
	return niveis, nil
}

func escrever(caminho string, r Relatorio) error {
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
