package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/verofreitt/iso8583-volumetry-lab/internal/metrics"
)

// pastaDaRodada cria results/<timestamp>-<tps>-<rep>/ e devolve o caminho.
//
// O timestamp usa hora local em formato ordenavel, sem separadores invalidos
// em nome de arquivo no Windows. A taxa entra como inteiro quando for inteira,
// para que os nomes de rodadas comuns fiquem legiveis.
func pastaDaRodada(raiz string, inicio time.Time, tps float64, rep int) (string, error) {
	nome := fmt.Sprintf("%s-%s-%d", inicio.Format("20060102T150405"), formatarTPS(tps), rep)
	caminho := filepath.Join(raiz, nome)

	if err := os.MkdirAll(caminho, 0o755); err != nil {
		return "", fmt.Errorf("criando %s: %w", caminho, err)
	}
	return caminho, nil
}

func formatarTPS(tps float64) string {
	if tps == float64(int64(tps)) {
		return fmt.Sprintf("%dtps", int64(tps))
	}
	return fmt.Sprintf("%gtps", tps)
}

// escreverResultados grava raw.csv e summary.json na pasta da rodada.
//
// Os dois arquivos sao gravados juntos e qualquer falha aborta: um resultado
// pela metade e pior que nenhum, porque convida a ser analisado sem que a
// ausencia do par seja notada.
func escreverResultados(pasta string, c *metrics.Coletor, r metrics.Resumo) error {
	bruto := filepath.Join(pasta, "raw.csv")
	f, err := os.Create(bruto)
	if err != nil {
		return fmt.Errorf("criando %s: %w", bruto, err)
	}
	if err := c.EscreverCSV(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", bruto, err)
	}

	resumo := filepath.Join(pasta, "summary.json")
	g, err := os.Create(resumo)
	if err != nil {
		return fmt.Errorf("criando %s: %w", resumo, err)
	}
	if err := r.EscreverJSON(g); err != nil {
		g.Close()
		return err
	}
	if err := g.Close(); err != nil {
		return fmt.Errorf("fechando %s: %w", resumo, err)
	}

	return nil
}
