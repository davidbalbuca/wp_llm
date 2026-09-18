package agent

import (
	"wp-llm-gas/internal/conversation"
)

// aliasInternoWhatsApp es el alias con el que el BACKEND guarda la ubicación compartida por
// WhatsApp (whatsapp_selectors.py, WHATSAPP_ALIAS). Es interno: el cliente nunca lo eligió y no
// se le puede mostrar ("te lo envío a WhatsApp" no significa nada).
//
// Vive en `conversation` porque LastOrder.Destino() también lo necesita para filtrarlo, y dos
// copias del mismo literal en paquetes distintos es una que se queda atrás el día que cambie.
const aliasInternoWhatsApp = conversation.AliasInternoWhatsApp

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
