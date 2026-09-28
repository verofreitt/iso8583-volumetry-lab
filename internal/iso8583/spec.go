// Package iso8583 define o subconjunto de ISO 8583:1987 usado no experimento,
// bem como a montagem, o parse e o enquadramento das mensagens em TCP.
//
// O subconjunto e deliberadamente minimo: sao os data elements necessarios para
// compor uma requisicao de autorizacao interoperavel, sem qualquer subelemento
// privado de bandeira. O criterio de selecao esta documentado em
// docs/experimento.md.
package iso8583

import (
	moov "github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
)

// Spec e o perfil ISO 8583:1987 em ASCII, com bitmap primario apenas.
//
// Nenhum campo declara padding. A decisao e deliberada: com Pad definido, a
// biblioteca preenche o valor no pack e o remove no unpack, de modo que uma
// data "0905" seria lida de volta como "905". Sem padding, o prefixador de
// tamanho fixo rejeita qualquer valor de largura incorreta no momento do pack,
// e o valor lido apos o unpack e byte a byte identico ao que trafegou na rede.
// Falha ruidosa em vez de corrupcao silenciosa do fluxo.
var Spec = &moov.MessageSpec{
	Name: "ISO 8583:1987 ASCII - subconjunto de autorizacao",
	Fields: map[int]field.Field{
		0: field.NewString(&field.Spec{
			Length:      4,
			Description: "MTI",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		1: field.NewBitmap(&field.Spec{
			// Length do bitmap e contado em bytes crus, nao em caracteres:
			// 8 bytes = 64 bits = bitmap primario apenas, emitido como 16
			// digitos hexadecimais ASCII. O valor 16, usado pelo Spec87ASCII
			// da propria biblioteca, produziria bitmap primario mais
			// secundario em toda mensagem.
			Length:      8,
			Description: "Bitmap",
			Enc:         encoding.BytesToASCIIHex,
			Pref:        prefix.Hex.Fixed,
		}),
		2: field.NewString(&field.Spec{
			Length:      19,
			Description: "PAN",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.LL,
		}),
		3: field.NewString(&field.Spec{
			Length:      6,
			Description: "Processing code",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		4: field.NewString(&field.Spec{
			Length:      12,
			Description: "Valor da transacao",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		7: field.NewString(&field.Spec{
			Length:      10,
			Description: "Data/hora de transmissao (MMDDhhmmss)",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		11: field.NewString(&field.Spec{
			Length:      6,
			Description: "STAN",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		12: field.NewString(&field.Spec{
			Length:      6,
			Description: "Hora local (hhmmss)",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		13: field.NewString(&field.Spec{
			Length:      4,
			Description: "Data local (MMDD)",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		18: field.NewString(&field.Spec{
			Length:      4,
			Description: "MCC",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		22: field.NewString(&field.Spec{
			Length:      3,
			Description: "POS entry mode",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		32: field.NewString(&field.Spec{
			Length:      11,
			Description: "ID da instituicao adquirente",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.LL,
		}),
		37: field.NewString(&field.Spec{
			Length:      12,
			Description: "RRN",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		39: field.NewString(&field.Spec{
			Length:      2,
			Description: "Codigo de resposta",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		41: field.NewString(&field.Spec{
			Length:      8,
			Description: "Terminal ID",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
		49: field.NewString(&field.Spec{
			Length:      3,
			Description: "Moeda",
			Enc:         encoding.ASCII,
			Pref:        prefix.ASCII.Fixed,
		}),
	},
}
