package notify

import (
	"fmt"
	"log"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/escalation"
)

// ReportarFallo es el ÚNICO camino para reportar un fallo invisible al cliente: ticket en el
// panel, marca en el chat, correo al equipo y aviso a Telegram. Centralizado a propósito (antes
// estaba duplicado y algunos sitios se olvidaban de Telegram).
//
// Best-effort de punta a punta: cada canal se degrada por su lado y nunca devuelve error. Devuelve
// el id del ticket (0 si no se pudo crear). `nombre` vacío se resuelve del perfil del store.
func ReportarFallo(cfg config.Config, store conversation.Store, phone, motivo, detalle string) int64 {
	log.Printf("[fallo] %s (%s): %s", motivo, phone, detalle)

	var tid int64
	yaAbierto := false
	if phone != "" {
		// Un problema que se repite es UN caso: si ya hay ticket abierto por este motivo, el
		// detalle nuevo se suma en vez de abrir otro. El 05/09 un mismo pedido abrió #20, #21 y #22.
		if previo, hay := store.GetOpenTicket(phone, motivo); hay {
			tid, yaAbierto = previo.ID, true
			store.LogMessage(phone, "system", fmt.Sprintf("🎫 (ticket #%d ya abierto) %s", tid, detalle))
		} else {
			tid = store.CreateTicket(phone, motivo, detalle)
			if tid > 0 {
				store.LogMessage(phone, "system", fmt.Sprintf("🎫 Ticket #%d — %s", tid, motivo))
			}
		}
	}

	// Caso ya abierto: no se repite correo ni Telegram (el equipo ya fue avisado; lo nuevo quedó
	// en el chat).
	if yaAbierto {
		return tid
	}

	// Correo async (SMTP puede tardar) para no frenar el chat.
	go escalation.SendSupportEmail(cfg, tid, phone, motivo, detalle)

	// Default nil (Telegram sin configurar) = Fallo no hace nada.
	Default.Fallo(phone, conversation.NombreDe(store, phone), motivo, detalle)
	return tid
}
