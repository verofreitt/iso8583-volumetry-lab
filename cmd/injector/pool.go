package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// pool mantem um conjunto de conexoes persistentes com o autorizador.
//
// Todas as conexoes sao abertas antes do inicio da rodada. O custo de
// estabelecer uma conexao TCP, ainda que em loopback, e da mesma ordem de
// grandeza do tempo de servico do mock; se a conexao fosse aberta dentro da
// requisicao, esse custo entraria na latencia medida e dominaria o resultado.
//
// Quando todas as conexoes estao ocupadas, a requisicao espera. Essa espera e
// legitima e precisa aparecer na latencia: e o que um cliente real com pool
// limitado observa. O modelo permanece aberto porque a chegada foi agendada
// pelo relogio, independentemente das conclusoes; apenas o atendimento e que
// enfileira, e o enfileiramento e medido a partir do instante de chegada
// pretendido.
type pool struct {
	endereco string
	prazo    time.Duration
	livres   chan net.Conn

	mu       sync.Mutex
	perdidas int
	fechadas bool
	abertas  []net.Conn
}

// novoPool abre n conexoes com o endereco informado.
func novoPool(endereco string, n int, prazo time.Duration) (*pool, error) {
	if n <= 0 {
		return nil, fmt.Errorf("numero de conexoes deve ser positivo, recebido %d", n)
	}

	p := &pool{
		endereco: endereco,
		prazo:    prazo,
		livres:   make(chan net.Conn, n),
	}

	for i := 0; i < n; i++ {
		conn, err := net.DialTimeout("tcp", endereco, prazo)
		if err != nil {
			p.fechar()
			return nil, fmt.Errorf("abrindo conexao %d de %d: %w", i+1, n, err)
		}
		p.abertas = append(p.abertas, conn)
		p.livres <- conn
	}

	return p, nil
}

// adquirir toma uma conexao livre, esperando se todas estiverem ocupadas.
func (p *pool) adquirir(ctx context.Context) (net.Conn, error) {
	select {
	case conn, ok := <-p.livres:
		if !ok {
			return nil, fmt.Errorf("pool fechado")
		}
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// devolver recoloca uma conexao sadia no pool.
func (p *pool) devolver(conn net.Conn) {
	p.mu.Lock()
	fechadas := p.fechadas
	p.mu.Unlock()

	if fechadas {
		conn.Close()
		return
	}

	select {
	case p.livres <- conn:
	default:
		// nao deveria ocorrer: o canal comporta todas as conexoes do pool
		conn.Close()
	}
}

// descartar fecha uma conexao defeituosa e tenta substitui-la, para que o pool
// nao encolha ao longo da rodada.
//
// Uma conexao que falhou no meio de uma troca nao pode voltar ao pool: o fluxo
// pode ter ficado dessincronizado, e a proxima requisicao a usa-la leria a
// resposta errada. Isso corromperia a correlacao por STAN em silencio.
func (p *pool) descartar(conn net.Conn) {
	conn.Close()

	p.mu.Lock()
	fechadas := p.fechadas
	p.mu.Unlock()
	if fechadas {
		return
	}

	nova, err := net.DialTimeout("tcp", p.endereco, p.prazo)
	if err != nil {
		p.mu.Lock()
		p.perdidas++
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	p.abertas = append(p.abertas, nova)
	p.mu.Unlock()

	p.devolver(nova)
}

// perdidasTotal devolve quantas conexoes nao puderam ser substituidas.
func (p *pool) perdidasTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.perdidas
}

// fechar encerra todas as conexoes abertas pelo pool.
func (p *pool) fechar() {
	p.mu.Lock()
	if p.fechadas {
		p.mu.Unlock()
		return
	}
	p.fechadas = true
	abertas := p.abertas
	p.abertas = nil
	p.mu.Unlock()

	for _, conn := range abertas {
		conn.Close()
	}
}
