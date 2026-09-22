// Package clock fornece um relogio monotonico de alta resolucao para a
// medicao de latencia.
//
// O time.Now da biblioteca padrao nao serve ao experimento no Windows. Ali o
// runtime do Go le o tempo de interrupcao do sistema, cuja granularidade e a
// do tique do temporizador: medida nesta maquina, o menor passo nao-nulo entre
// duas leituras sucessivas ficou entre 331 e 534 microssegundos, e o relogio
// de parede avanca em degraus de cerca de 30 milissegundos.
//
// Uma troca de mensagens em loopback e mais rapida que isso. Com time.Now, a
// diferenca entre o instante de envio e o de resposta le zero, e a coluna de
// latencia de servico do raw.csv vira ficcao. O efeito foi observado antes de
// qualquer experimento: numa rodada de 200 TPS, os campos ts_envio e
// ts_resposta saiam identicos ate o ultimo digito, e a mediana da latencia de
// servico saia em zero.
//
// Este pacote resolve o problema ancorando uma unica leitura de parede a um
// contador monotonico de alta resolucao. Todos os instantes seguintes sao
// derivados dessa ancora mais o deslocamento monotonico, de modo que a
// diferenca entre dois instantes tem a precisao do contador, e nao a do tique
// do sistema. A exatidao absoluta em relacao ao horario civil continua sendo a
// da ancora, o que e irrelevante para medir duracoes.
//
// No Windows o contador e o QueryPerformanceCounter. Nos demais sistemas o
// relogio monotonico da biblioteca padrao ja tem resolucao suficiente e e
// usado diretamente.
package clock

import "time"

// padrao e o relogio do processo, ancorado uma unica vez na inicializacao.
var padrao = novo()

type relogio struct {
	parede time.Time
	origem int64 // leitura monotonica correspondente a ancora, em nanossegundos
}

func novo() *relogio {
	return &relogio{
		parede: time.Now(),
		origem: lerMonotonico(),
	}
}

// Agora devolve o instante corrente.
//
// O valor nao carrega leitura monotonica propria do runtime: a precisao vem do
// deslocamento ja embutido. Subtrair dois instantes devolvidos por esta funcao
// produz a duracao com a resolucao do contador subjacente.
func Agora() time.Time {
	return padrao.parede.Add(time.Duration(lerMonotonico() - padrao.origem))
}

// Desde devolve quanto se passou desde o instante informado.
func Desde(t time.Time) time.Duration {
	return Agora().Sub(t)
}

// Ate devolve quanto falta para o instante informado, negativo se ja passou.
//
// Substitui time.Until, que compara com time.Now e portanto mistura duas bases
// de tempo diferentes: o erro seria a deriva entre elas.
func Ate(t time.Time) time.Duration {
	return t.Sub(Agora())
}

// Resolucao devolve o menor passo distinguivel pelo relogio.
//
// O valor e registrado no bloco de ambiente do summary.json: uma latencia da
// ordem da resolucao do relogio nao e mensuravel, e o artigo precisa declarar
// esse piso em vez de apresentar numeros abaixo dele.
func Resolucao() time.Duration {
	return resolucao()
}

// Fonte nomeia o contador em uso, para registro no summary.json.
func Fonte() string {
	return fonte()
}
