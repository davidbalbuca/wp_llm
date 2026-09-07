// Respuestas a MENÚS CERRADOS resueltas en código, sin pasar por el modelo.
//
// Cuando el cliente toca un botón, su respuesta es un conjunto cerrado: no hay nada que
// interpretar. Dejar que el modelo decida si llama a la herramienta correcta es la misma
// fragilidad que ya explotó tres veces (dirección, entrega agendada, OTP) y que hoy resuelven
// ConfirmarDireccion, ConfirmarProgramado y HandleVerification. Esto generaliza ese patrón a
// los menús que TODAVÍA dependen del modelo, antes de que fallen en producción.
//
// Regla común a todos los interceptores de este archivo: si el cliente responde algo que NO es
// una de las opciones (pregunta, reclamo, saludo, "esperar pero hasta las 6"), devuelven
// manejado=false y el mensaje sigue su curso normal hacia el modelo. Nunca interpretan de más.
package agent

import (
	"fmt"
	"log"
	"strconv"
	"strings"
)

// ResponderMenuEspera resuelve la respuesta del cliente al menú "¿Deseas esperar?" que se le
// muestra cuando no hay repartidor cerca. Es el más urgente de los menús sin resolver: si el
// modelo no llama a esperar_conductor, el cliente que contestó "Esperar" se queda sin búsqueda
// de repartidor y sin gas, creyendo que lo están atendiendo.
//
// "Programar" NO se resuelve aquí a propósito: necesita que el cliente diga una hora, así que
// es una conversación y le toca al modelo.
func (a *Agent) ResponderMenuEspera(from, texto string) (string, bool) {
	if _, hayEspera := a.store.GetPendingWait(from); !hayEspera {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)

	switch {
	case respuesta == "esperar" || respuestasAfirmativas[respuesta]:
		log.Printf("[menu-espera] %s aceptó esperar; se arranca la búsqueda en código", from)
		a.esperarConductor(from) // arranca startWaitForDriver; su texto es para el modelo
		return "¡Perfecto! 🚚 Ya estoy buscando un repartidor para ti. Te aviso por aquí apenas " +
			"se asigne (o si en unos minutos no hay ninguno disponible). Quédate atento 😊", true

	case respuesta == "cancelar" || respuestasNegativas[respuesta]:
		log.Printf("[menu-espera] %s no quiso esperar; se cancela la espera en código", from)
		a.cancelarEspera(from) // deja el pedido como NO ASIGNADO y avisa al grupo
		return fmt.Sprintf("Entendido 🙏. Si prefieres, puedo agendarte la entrega para más tarde: "+
			"atendemos de %s a %s, dime a qué hora te viene bien y la dejo lista. "+
			"Y si no, aquí estoy cuando me necesites 😊", a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin), true
	}

	// "Programar", una pregunta, o cualquier otra cosa: la atiende el modelo.
	return "", false
}

// ResponderCalificacion registra la calificación del repartidor cuando el cliente responde con
// un número del 1 al 5 y nada más. Si escribe algo más ("5 muy amable", "le pongo 4 pero llegó
// tarde"), se deja al modelo: ahí hay un comentario que vale la pena guardar.
func (a *Agent) ResponderCalificacion(from, texto string) (string, bool) {
	rating, hay := a.store.GetPendingRating(from)
	if !hay || rating.PedidoID <= 0 {
		return "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(texto))
	if err != nil || n < 1 || n > 5 {
		return "", false
	}

	log.Printf("[menu-calificacion] %s calificó con %d; se registra en código", from, n)
	// calificarConductor limpia el pendiente en todos los caminos y su texto está escrito para
	// el modelo; aquí redactamos el del cliente según haya salido bien o mal.
	salida := a.calificarConductor(from, map[string]any{"estrellas": n})
	if strings.Contains(salida, "registrada con éxito") {
		return fmt.Sprintf("¡Gracias por calificar a %s con %d/5! 🙌 Tu opinión nos ayuda muchísimo. "+
			"Cuando necesites tu gas, aquí estoy 😊", rating.Conductor, n), true
	}
	// No se pudo registrar (backend caído, cuenta sin credenciales): se agradece igual. El
	// cliente no tiene por qué enterarse de un problema nuestro que no le afecta.
	return "¡Gracias por tu calificación! 🙌 Cuando necesites tu gas, aquí estoy 😊", true
}
