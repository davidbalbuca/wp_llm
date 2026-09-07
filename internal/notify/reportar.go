package notify

import (
	"fmt"
	"log"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/escalation"
)

// ReportarFallo es el ÚNICO camino para reportar un fallo que el cliente no puede ver. Hace las
// cuatro cosas o ninguna a medias: ticket en el panel, marca en el chat, correo al equipo y aviso
// a Telegram. Antes esto estaba duplicado en dos funciones (reportarFallo en main y
// crearTicketSoporte en agent) y copiado a mano en otros dos sitios que se olvidaron de Telegram:
// el "Error técnico del agente" y el endpoint que usa el backend. Resultado: fallos que llegaban
// por correo pero no sonaban en el grupo. Con un solo camino eso no puede volver a pasar — si
// mañana se agrega un canal, se toca aquí y todos lo heredan.
//
// Best-effort de principio a fin: si el SMTP no está configurado, si Telegram falla o si el store
// no puede crear el ticket, cada parte se degrada por su lado y las demás siguen. Nunca devuelve
// error: quien llama está en un camino donde ya no hay nada que decidir. Devuelve el id del ticket
// (0 si no se pudo crear) para quien lo necesite.
//
// `nombre` puede venir vacío: se resuelve del perfil si el store lo tiene.
func ReportarFallo(cfg config.Config, store conversation.Store, phone, motivo, detalle string) int64 {
	log.Printf("[fallo] %s (%s): %s", motivo, phone, detalle)

	var tid int64
	yaAbierto := false
	if phone != "" {
		// Un problema que se repite es UN caso, no tres. Si ya hay un ticket abierto de este
		// cliente por este mismo motivo, el detalle nuevo se suma a la conversación y no se
		// abre otro: el 05/09 un mismo pedido generó los tickets #20, #21 y #22, y el equipo
		// tuvo que adivinar cuál mirar.
		if previo, hay := store.GetOpenTicket(phone, motivo); hay {
			tid, yaAbierto = previo.ID, true
			store.LogMessage(phone, "system", fmt.Sprintf("🎫 (ticket #%d ya abierto) %s", tid, detalle))
		} else {
			tid = store.CreateTicket(phone, motivo, detalle)
			if tid > 0 {
				// Queda en la conversación: al abrir el chat en el panel se ve DÓNDE se rompió.
				store.LogMessage(phone, "system", fmt.Sprintf("🎫 Ticket #%d — %s", tid, motivo))
			}
		}
	}

	// Si el caso ya estaba abierto no se repite el correo ni el aviso de Telegram: el equipo ya
	// fue avisado y lo único nuevo es el detalle, que quedó en el chat.
	if yaAbierto {
		return tid
	}

	// El correo es async (SMTP puede tardar) y no debe frenar el chat.
	go escalation.SendSupportEmail(cfg, tid, phone, motivo, detalle)

	// Telegram: Default es nil si no está configurado, y Fallo sobre nil no hace nada.
	Default.Fallo(phone, conversation.NombreDe(store, phone), motivo, detalle)
	return tid
}
