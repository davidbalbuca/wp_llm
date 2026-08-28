package main

// Cierre amable de una conversación que quedó a medias.
//
// Un cliente empieza a pedir gas, el bot le pregunta la cédula y el cliente no vuelve. La
// conversación queda abierta para siempre: ni él sabe si tiene que contestar, ni nosotros
// sabemos si se cayó o se arrepintió. Pasó el 28/08 con 593984187615, que dio color, cantidad
// y ubicación y desapareció justo en el último dato.
//
// A los pocos minutos de silencio el bot se despide con calidez y deja la puerta abierta. No
// borra nada: si el cliente vuelve dentro de las 24 h, el historial sigue ahí y retoma el
// pedido donde lo dejó.

import (
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// mensajeCierre es el texto de despedida. Es NEUTRO a propósito: sirve igual para quien estaba
// pidiendo gas y para quien solo preguntó algo. Prometer "seguimos con tu pedido" a alguien que
// nunca pidió nada suena a error.
const mensajeCierre = "Parece que te ocupaste 😊 No te preocupes, aquí estoy.\n\n" +
	"Escríbeme cuando puedas y con gusto seguimos. ¡Te esperamos pronto! 🙌"

// mensajesMinimos es el recorrido que debe tener la conversación para merecer una despedida.
// A quien escribió "hola" y se fue no se le dice nada: sería hablarle a alguien que ni empezó.
// Cuatro son dos idas y vueltas.
const mensajesMinimos = 4

// cerrarConversacionesInactivas revisa cada minuto los chats callados y despide los que
// corresponde. Se llama una vez desde main, en su propia goroutine.
func cerrarConversacionesInactivas(cfg config.Config, store conversation.Store) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		revisarCierres(cfg, store)
	}
}

func revisarCierres(cfg config.Config, store conversation.Store) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cierre] panic recuperado: %v", r)
		}
	}()

	// Los chats con un ticket abierto quedan fuera: ese cliente esta esperando que le escriba
	// una persona, no que el bot se despida. Se consultan UNA vez por barrido.
	derivados := map[string]bool{}
	for _, t := range store.ListTickets(conversation.TicketAbierto, 200) {
		derivados[t.Phone] = true
	}

	ahora := time.Now()
	for _, chat := range store.ListConversations(100) {
		if derivados[chat.Phone] {
			continue
		}
		if !mereceCierre(store, chat, ahora, cfg.CierreInactividad, cfg.CierreVentanaMax) {
			continue
		}
		if err := avisarCliente(cfg, store, chat.Phone, mensajeCierre); err != nil {
			log.Printf("[cierre] no se pudo despedir a %s: %v", chat.Phone, err)
			continue
		}
		log.Printf("[cierre] conversación cerrada por inactividad: %s", chat.Phone)
	}
}

// mereceCierre decide si a esta conversación le toca la despedida. Todo lo que dice que NO está
// aquí junto, que es la parte delicada: mandar este mensaje donde no toca es peor que no
// mandarlo.
func mereceCierre(store conversation.Store, chat conversation.ConversationSummary,
	ahora time.Time, inactividad, ventanaMax time.Duration) bool {

	// Un humano tomó el chat: el bot no se mete.
	if chat.Mode != conversation.ChatModeBot {
		return false
	}

	silencio := ahora.Sub(time.Unix(chat.LastAt, 0))
	if silencio < inactividad {
		return false // todavía puede estar escribiendo
	}
	// TECHO. Sin esto, al reiniciar el bot (un deploy, por ejemplo) saldría a despedirse de
	// todas las conversaciones calladas del día a la vez. Recibir "parece que te ocupaste" seis
	// horas después de haber escrito es molesto y desconcierta: si ya pasó demasiado, se deja
	// morir la conversación en silencio.
	if silencio > ventanaMax {
		return false
	}

	// El último en hablar tiene que ser el bot: si quedó colgado un mensaje DEL CLIENTE sin
	// responder, lo que corresponde es contestarle, no despedirse.
	if chat.LastRole == "user" {
		return false
	}
	// Y el bot tiene que haber dejado una PREGUNTA sin responder. Si su último mensaje fue un
	// cierre normal -"¡Hasta pronto!", "ya avisé al dueño"- la conversación no quedó a medias:
	// termino. Sin esta condición pasaba lo que vio David con Juan Solano: el bot se despedía,
	// y siete minutos después soltaba "parece que te ocupaste" sobre una conversación que ya
	// estaba cerrada. En español la pregunta siempre trae "?", venga de un menú o de un texto.
	if !strings.Contains(chat.LastMessage, "?") {
		return false
	}
	// Ya se despidió antes. Como la despedida queda de último mensaje, con mirar ese basta y no
	// hace falta guardar ninguna marca aparte: si el cliente contesta, deja de ser el último.
	if strings.HasPrefix(chat.LastMessage, "Parece que te ocupaste") {
		return false
	}

	// Flujos que tienen sus propios avisos y sus propios tiempos. Despedirse en medio de
	// cualquiera de ellos se cruzaría con el mensaje que el sistema ya le va a mandar.
	if chat.Programado || chat.EnEspera {
		return false
	}
	if _, hay := store.GetPendingRating(chat.Phone); hay {
		return false // se le pidió que califique; puede contestar en cualquier momento
	}
	if _, hay := store.GetPendingVerification(chat.Phone); hay {
		return false // está por mandar su código de verificación
	}
	if _, hay := store.GetActivePedido(chat.Phone); hay {
		return false // tiene un pedido en curso; el chat sigue vivo aunque él no escriba
	}

	// Y que la conversación haya arrancado de verdad.
	return len(store.GetConversation(chat.Phone, mensajesMinimos)) >= mensajesMinimos
}
