package main

import (
	"log"

	"wp-llm-gas/internal/agent"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/notify"
)

// QUÉ SE HACE CUANDO ALGUIEN NO VIENE A PEDIR GAS.
//
// La detección vive en internal/agent/abuso.go; aquí está lo que ocurre después, que son cuatro
// cosas y ninguna es opcional:
//
//  1. TICKET + aviso inmediato a soporte (correo y Telegram). Hasta hoy el bot rechazaba bien
//     estos mensajes pero nadie se enteraba: si mañana alguien encuentra la frase que sí
//     funciona, nos enteramos por el daño.
//  2. El mensaje COMPLETO queda en el ticket. Es la única forma de ver si un intento suelto se
//     vuelve un patrón, y de saber qué probaron exactamente.
//  3. El chat pasa a un OPERADOR. El bot deja de contestar: si alguien está buscando la grieta,
//     lo último que conviene es darle mil intentos gratis contra el modelo.
//  4. Al cliente se le avisa que lo atenderá una persona, SIN acusarlo de nada.
//
// El punto 4 no es cortesía: puede ser un cliente real que escribió algo raro. Decirle "se
// detectó un intento de ataque" sería ofensivo con él y, con el que sí está sondeando, le
// estaría enseñando qué detectamos.

// atenderPosibleAtaque registra el intento, avisa a soporte y le pasa el chat a un operador.
func atenderPosibleAtaque(cfg config.Config, store conversation.Store, phone, texto string) {
	log.Printf("[abuso] %s: posible ataque; se abre ticket y pasa a un operador: %q", phone, texto)

	// El mensaje entra al historial como cualquier otro: si un operador abre el chat, tiene que
	// ver lo que el cliente escribió, no un hueco.
	store.LogMessage(phone, "user", texto)

	// ReportarFallo hace ticket + correo + Telegram, y AGRUPA por motivo: quien manda diez
	// intentos seguidos genera UN ticket con todo dentro, no diez que nadie lee.
	notify.ReportarFallo(cfg, store, phone, agent.MotivoAbuso,
		"El cliente escribió algo que no es un pedido y parece un intento de usar el bot para "+
			"otra cosa (inyección de instrucciones, SQL, código o comandos). El bot dejó de "+
			"responder y el chat quedó en manos de un operador.\n\nMensaje recibido:\n"+texto)

	// El bot se calla. Mismo mecanismo que cuando escribe una persona del equipo (takeover.go):
	// el control vuelve solo tras HUMAN_TAKEOVER_TIMEOUT_MIN de silencio, así que un cliente que
	// solo escribió algo raro no queda encerrado para siempre.
	tomarChatAlEscribir(store, phone)

	if err := avisarCliente(cfg, store, phone, agent.MensajeAbusoDetectado()); err != nil {
		log.Printf("[abuso] no se pudo avisar a %s: %v", phone, err)
	}
}
