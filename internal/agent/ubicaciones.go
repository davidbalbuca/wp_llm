package agent

import (
	"wp-llm-gas/internal/conversation"
)

// aliasInternoWhatsApp es el alias con el que el BACKEND guarda la ubicación compartida por
// WhatsApp (whatsapp_selectors.py, WHATSAPP_ALIAS). Es interno: el cliente nunca lo eligió y no
// se le puede mostrar ("te lo envío a WhatsApp" no significa nada).
const aliasInternoWhatsApp = "WhatsApp"

// describirPedido arma el texto de un pedido pasado como lo ve el cliente: "2 Blanco a Casa",
// o "1 Blanco + 1 Amarillo a Casa" si fue multicolor.
//
// El destino solo aparece si se sabe: los pedidos anteriores a esta función no guardaron
// ubicación, y ahí el texto es "2 Blanco" a secas. Inventar un destino sería peor que omitirlo.
func describirPedido(last conversation.LastOrder) string {
	txt := describeItems(last.ItemsDelPedido())
	if destino := last.Destino(); destino != "" {
		txt += " a " + destino
	}
	return txt
}
