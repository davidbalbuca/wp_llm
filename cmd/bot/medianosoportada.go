package main

// UN AUDIO NO ES UNA ENTRADA INVÁLIDA: ES ALGUIEN QUE NECESITA ALGO.
//
// AUDITORÍA 28/09 (skill bot-log-forensics, patrón P4): "Por ahora solo puedo leer mensajes de
// texto y ubicaciones" es el SEGUNDO mensaje del ranking de abandono. Lo recibieron 15 clientes y
// OCHO no volvieron a escribir nunca.
//
// Lo que hace caro el fallo es CUÁNDO llega, medido en el corpus:
//
//	tras "Ahora compárteme tu ubicación"          -> estaba explicando dónde vive
//	tras "🛵 El conductor llegó a tu ubicación"    -> EL REPARTIDOR ESTÁ EN SU PUERTA
//	tras "Tu pedido está confirmado / Repartidor" -> una duda sobre su pedido en curso
//	tras "¡Hola, Carlos! 👋 ¿Cómo estás?"          -> se fue en el primer turno
//
// El de "el conductor llegó" es el peor: hay una persona esperando abajo y el cliente no puede
// comunicarse por el medio que eligió. Contestarle con una limitación técnica y cortar ahí es
// perderlo justo cuando más falta hacía atenderlo.
//
// DOS COSAS CAMBIAN. Primero, el mensaje OFRECE la salida en vez de describir el problema —y si lo
// que hacía falta era la dirección, manda el botón de ubicación, que es un toque en vez de
// escribir—. Segundo, con un pedido VIVO no se despacha con un texto: se abre un caso en la cola
// humana, porque eso ya no es una entrada no soportada sino un cliente con un problema en curso.

import (
	"log"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// textoPideTexto es la respuesta cuando el cliente manda algo que el bot no sabe leer.
//
// Ofrece las DOS salidas concretas: escribirlo, o —si era la dirección— el pin. Antes solo decía
// "no puedo" y el cliente tenía que adivinar qué hacer.
const textoPideTexto = "Todavía no puedo escuchar audios ni ver imágenes 🙏\n\n" +
	"¿Me lo escribes en un mensaje? Y si es tu dirección, mándame el pin: " +
	"adjuntar 📎 → Ubicación → Enviar ubicación actual 📍"

// textoPideTextoConPedido es para quien YA tiene un pedido en camino. Ahí la prioridad no es
// explicarle una limitación: es que alguien lo atienda. Se le dice la verdad —que una persona va a
// escribirle— y el ticket se crea ANTES de prometerlo (ver crearTicketSoporte).
const textoPideTextoConPedido = "Todavía no puedo escuchar audios 🙏 Como tienes un pedido en " +
	"curso, le paso tu mensaje a una persona del equipo para que te escriba enseguida.\n\n" +
	"Si prefieres, escríbeme aquí lo que necesitas y te ayudo al instante 😊"

// responderMediaNoSoportada contesta un mensaje que el bot no puede leer (audio, imagen, sticker).
//
// Devuelve el texto que se le mandó, para que el llamador lo registre igual que cualquier otra
// respuesta.
func responderMediaNoSoportada(cfg config.Config, store conversation.Store, phone string) {
	// ¿Tiene algo en curso? Eso decide si es una molestia menor o un cliente desatendido.
	_, hayPedido := store.GetActivePedido(phone)
	_, hayEntrega := store.GetPendingDeliveryCheck(phone)
	urgente := hayPedido || hayEntrega

	if urgente {
		// El ticket PRIMERO: la frase promete que una persona va a escribir, y eso solo puede
		// decirse si el caso existe de verdad (ver specs/afirmaciones-respaldadas-por-estado.md).
		reportarFallo(cfg, store, phone, "Cliente con pedido en curso mandó un audio",
			"El cliente tiene un pedido/entrega en curso y escribió por un medio que el bot no "+
				"puede leer (audio, imagen o sticker). Hay que escribirle: no pudo comunicarse por "+
				"el canal que eligió. En el corpus del 28/09 uno de estos audios llegó justo "+
				"cuando el conductor ya estaba en su puerta.")
		log.Printf("[media] %s mandó audio/imagen CON pedido en curso; se deriva a una persona", phone)
		_ = replyClient(cfg, store, phone, textoPideTextoConPedido)
		return
	}
	log.Printf("[media] %s mandó algo que no se puede leer; se le ofrece escribir o mandar el pin", phone)
	_ = replyClient(cfg, store, phone, textoPideTexto)
}
