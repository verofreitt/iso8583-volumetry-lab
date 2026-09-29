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
	"math/rand"
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

	embaralhar   bool
	sementeOrdem int64
	repouso      time.Duration

	argsAutorizador string
	nome            string

	sementeMassa  int64
	variarSemente bool
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
	flag.BoolVar(&o.embaralhar, "shuffle", true, "sorteia a ordem de execucao das rodadas, em vez de varrer os niveis em sequencia")
	flag.Int64Var(&o.sementeOrdem, "order-seed", 1, "semente do sorteio da ordem de execucao")
	flag.DurationVar(&o.repouso, "rest", 3*time.Second, "repouso entre rodadas")
	flag.StringVar(&o.argsAutorizador, "sut-args", "--echo-only", "argumentos do autorizador; o padrao calibra contra o alvo trivial")
	flag.StringVar(&o.nome, "nome", "calibracao", "prefixo da pasta de saida")
	flag.Int64Var(&o.sementeMassa, "massa-seed", 42, "semente base da ordem de consumo da massa")
	flag.BoolVar(&o.variarSemente, "vary-seed", true, "soma o numero da repeticao a semente da massa, tornando as repeticoes independentes tambem no desfecho de negocio")
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
	pasta := filepath.Join(o.destino, o.nome+"-"+inicio.Format("20060102T150405"))
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return fmt.Errorf("criando %s: %w", pasta, err)
	}

	binarios, err := compilar(pasta)
	if err != nil {
		return err
	}

	log.Printf("calibracao: %d niveis, %d repeticoes, %v por rodada", len(niveis), o.repeticoes, o.duracao)
	log.Printf("tempo estimado: %v", time.Duration(len(niveis)*o.repeticoes)*(o.duracao+3*time.Second))

	plano := planejar(niveis, o.repeticoes, o.embaralhar, o.sementeOrdem)
	if o.embaralhar {
		log.Printf("ordem de execucao sorteada com a semente %d", o.sementeOrdem)
	} else {
		log.Printf("ordem de execucao sequencial")
	}

	// resultados indexados por (taxa, repeticao), porque a execucao pode nao
	// seguir a ordem dos niveis
	coletado := map[float64]map[int]Repeticao{}
	for _, tps := range niveis {
		coletado[tps] = map[int]Repeticao{}
	}

	for i, e := range plano {
		log.Printf("execucao %d de %d: %g TPS, repeticao %d", i+1, len(plano), e.TPS, e.Repeticao)

		resumo, err := rodada(binarios, o, pasta, e.TPS, e.Repeticao, i+1)
		if err != nil {
			return fmt.Errorf("execucao %d (%g TPS, repeticao %d): %w", i+1, e.TPS, e.Repeticao, err)
		}
		coletado[e.TPS][e.Repeticao] = resumir(resumo)

		// repouso entre rodadas, para que uma nao herde o estado deixado pela
		// anterior. E declarado e fixo: um intervalo variavel seria mais uma
		// fonte de dispersao nao controlada.
		if o.repouso > 0 && i < len(plano)-1 {
			time.Sleep(o.repouso)
		}
	}

	var medidos []Nivel
	for _, tps := range niveis {
		n := Nivel{TPS: tps}
		for rep := 1; rep <= o.repeticoes; rep++ {
			n.Repeticoes = append(n.Repeticoes, coletado[tps][rep])
		}
		n.consolidar(o.limiarVazao)
		medidos = append(medidos, n)

		log.Printf("%g TPS: atraso medio %d us (intervalo %d us), vazao %.1f%%, p99 servico %d us -> %s",
			n.TPS, n.AtrasoMedioUS, n.IntervaloUS, n.PercentualDoAlvo, n.P99ServicoUS, situacao(n.Sustentado))
	}

	rel := Relatorio{
		Procedimento: Procedimento{
			Niveis:       niveis,
			Repeticoes:   o.repeticoes,
			Duracao:      o.duracao.String(),
			Warmup:       o.warmup.String(),
			Conexoes:     o.conexoes,
			LimiarVazao:  o.limiarVazao,
			Embaralhada:  o.embaralhar,
			SementeOrdem: o.sementeOrdem,
			Repouso:      o.repouso.String(),
			Ordem:        plano,
			Inicio:       inicio,
			Fim:          time.Now(),
			ModoAlvo:     o.argsAutorizador,
		},
		Niveis:     medidos,
		Ambiente:   ambiente(o),
		Calibracao: ehCalibracao(o.argsAutorizador),
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
func rodada(b caminhos, o opcoes, pasta string, tps float64, rep, sequencia int) (metrics.Resumo, error) {
	configAutorizador := filepath.Join(pasta, "autorizador.json")

	args := append(argsDoAutorizador(o.argsAutorizador), "--quiet", "--config-out", configAutorizador)
	aut := exec.Command(b.autorizador, args...)
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
		"-seed", strconv.FormatInt(sementeDaRodada(o.sementeMassa, rep, o.variarSemente), 10),
		"-seq", strconv.Itoa(sequencia),
		"-shuffle="+strconv.FormatBool(o.embaralhar),
		"-order-seed", strconv.FormatInt(o.sementeOrdem, 10),
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

// sementeDaRodada devolve a semente de consumo da massa de uma repeticao.
//
// Com --vary-seed, cada repeticao consome a massa em ordem diferente. Sem
// isso, as repeticoes de um nivel sao replicas independentes apenas para
// latencia: o desfecho de negocio de cada requisicao e funcao de (semente do
// mock, STAN), o STAN vem do indice da chegada e a ordem da massa vem da
// semente do injetor — com as tres fixas, as repeticoes recebem exatamente as
// mesmas decisoes.
//
// Foi o que aconteceu no experimento de 28/09: as cinco repeticoes de cada
// nivel produziram o mesmo conjunto de pares (transacao, codigo de resposta),
// byte a byte. Tratar as cinco como independentes numa analise de desfecho
// seria pseudorreplicacao.
//
// Fixar a semente continua util quando o desenho pede comparacao pareada, como
// no controle positivo da secao 9, em que a unica diferenca entre as condicoes
// precisa ser o vies injetado.
func sementeDaRodada(base int64, rep int, variar bool) int64 {
	if !variar {
		return base
	}
	return base + int64(rep)
}

// ehCalibracao informa se a varredura mede o aparato ou o sistema sob teste.
//
// Contra o alvo trivial, o que sobra de latencia e do aparato, e apurar pisos e
// teto faz sentido. Contra um alvo com latencia configurada, os mesmos numeros
// passam a descrever o alvo: reportar a mediana de servico de uma rodada de
// experimento como "piso de servico, ida e volta em loopback" seria
// simplesmente falso.
func ehCalibracao(argsAutorizador string) bool {
	for _, a := range strings.Fields(argsAutorizador) {
		if a == "--echo-only" || a == "-echo-only" {
			return true
		}
	}
	return false
}

// argsDoAutorizador separa a string de argumentos em campos.
//
// A separacao e por espaco, o que impede argumentos que contenham espacos.
// Nenhum dos parametros do autorizador precisa deles — duracoes, taxas e
// distribuicoes sao todos compactos — e a alternativa, um analisador de linha
// de comando completo, seria complexidade sem uso.
func argsDoAutorizador(spec string) []string {
	return strings.Fields(spec)
}

// planejar monta a lista de rodadas e, opcionalmente, sorteia sua ordem.
//
// Varrer os niveis em sequencia — todas as repeticoes de 100 TPS, depois todas
// as de 250, e assim por diante — deixa o nivel de carga perfeitamente
// confundido com a posicao na varredura. Se o estado da maquina derivar ao
// longo dos minutos que a varredura leva, a deriva aparece como se fosse
// efeito do nivel, e nenhuma analise dos dados consegue separar as duas coisas.
//
// Sortear a ordem quebra o confundimento. A ordem sorteada fica registrada no
// calibracao.json e a posicao de cada rodada vai para o summary.json dela.
func planejar(niveis []float64, repeticoes int, embaralhar bool, semente int64) []Execucao {
	plano := make([]Execucao, 0, len(niveis)*repeticoes)
	for _, tps := range niveis {
		for rep := 1; rep <= repeticoes; rep++ {
			plano = append(plano, Execucao{TPS: tps, Repeticao: rep})
		}
	}

	if embaralhar {
		// fonte explicita, nunca as funcoes globais do math/rand, pelas razoes
		// registradas em internal/massa
		r := rand.New(rand.NewSource(semente))
		r.Shuffle(len(plano), func(i, j int) { plano[i], plano[j] = plano[j], plano[i] })
	}

	return plano
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
