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

	"wp-llm-gas/internal/conversation"
)

// Botones del menú de repetir pedido. Viven aquí (y no en el prompt) para que el interceptor
// y el modelo usen exactamente el mismo texto: si cambia, cambia en un solo sitio.
const (
	BotonRepetirPedido = "Repetir lo mismo"
	BotonCambiarPedido = "Cambiar el pedido"
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
	// Si el cliente ya eligió color y le acabamos de preguntar CUÁNTOS, ese "2" es la cantidad
	// de su pedido nuevo, no una nota al repartidor de hace horas (la calificación pendiente
	// vive 24h). Interceptarlo le respondía "¡gracias por calificar!" a quien pedía gas.
	if p, hay := a.store.GetPedidoEnCurso(from); hay && p.Color != "" && p.Cantidad < 1 {
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

// pideRepetirPedido detecta que el cliente pide su pedido de siempre ESCRIBIENDO, sin tocar el
// botón: "como el último pedido", "lo de siempre", "repite mi pedido". Nació del caso de David
// (10/09): canceló, escribió "Como el último pedido" tres veces, y como el interceptor solo
// aceptaba el texto exacto del botón, el mensaje fue al modelo — que afirmó el pedido sin
// registrarlo, y el rescate no tenía ficha (la cancelación la limpia) ni miraba el LastOrder.
// Resultado: cuatro "tuve un problema al registrar tu pedido" falsos seguidos.
//
// La intención de repetir es un conjunto casi cerrado, como un botón dicho con palabras: por
// eso se resuelve aquí y no en el modelo. Lo que NO es inequívoco sigue al modelo.
func pideRepetirPedido(texto string) bool {
	// Una PREGUNTA nunca es la orden de repetir: "¿cómo va mi último pedido?" consulta el
	// estado, y "¿me repites el precio?" pide información. Interceptarlas registraría un pedido
	// que nadie hizo.
	if strings.ContainsAny(texto, "?¿") {
		return false
	}
	// afirmaSecuencia tolera palabras intercaladas ("repíteme por favor el pedido") y descarta
	// negaciones ("ya no quiero lo mismo de siempre").
	return afirmaSecuencia(texto, [][]string{
		{"repetir", "pedido"}, {"repite", "pedido"}, {"repiteme", "pedido"},
		{"repetir", "lo", "mismo"}, {"repite", "lo", "mismo"},
		{"como", "el", "ultimo", "pedido"}, {"como", "mi", "ultimo", "pedido"},
		{"como", "la", "ultima", "vez"}, {"igual", "que", "la", "ultima", "vez"},
		{"lo", "mismo", "de", "la", "ultima"}, {"lo", "mismo", "que", "la", "ultima"},
		{"lo", "mismo", "de", "siempre"}, {"lo", "de", "siempre"},
		{"pedido", "de", "siempre"}, {"quiero", "lo", "mismo"},
		{"mandame", "lo", "mismo"}, {"enviame", "lo", "mismo"},
	}, 2)
}

// ResponderRepetirPedido resuelve el menú "¿Deseas lo mismo de la última vez?". Si el cliente
// acepta —tocando el botón O pidiéndolo con palabras (ver pideRepetirPedido)—, el pedido
// anterior se carga en la ficha EN CÓDIGO y solo se le pide la ubicación: no depende de que el
// modelo recuerde qué pidió la última vez ni de que lo transcriba bien.
//
// "Cambiar el pedido" NO se resuelve aquí: a partir de ahí es una conversación normal (qué
// color, cuántos) y le toca al modelo.
func (a *Agent) ResponderRepetirPedido(from, texto string) (string, bool) {
	if normalizarRespuesta(texto) != normalizarRespuesta(BotonRepetirPedido) && !pideRepetirPedido(texto) {
		return "", false
	}
	last, hay := a.store.GetLastOrder(from)
	if !hay || last.Cantidad < 1 || last.Color == "" {
		// No hay nada que repetir: que el modelo lo lleve por el flujo normal.
		return "", false
	}
	// Con un pedido VIVO no se repite nada en código: registrar chocaría con la idempotencia y
	// acabaría en un "inconveniente técnico" falso con ticket. Puede ser el cliente preguntando
	// por su pedido en curso o queriendo agregarle algo: eso es conversación y le toca al modelo.
	if a.tienePedidoVivo(from) {
		return "", false
	}

	// TODAS las líneas del pedido anterior, no solo la primera: un último pedido multicolor
	// (C1) se repite completo. La última línea queda como "en curso" y el resto en Items,
	// igual que si el cliente las hubiera dicho.
	lineas := last.ItemsDelPedido()
	ficha := conversation.PedidoEnCurso{Flujo: conversation.FlujoInmediato}
	ficha.Color, ficha.Cantidad = lineas[len(lineas)-1].Color, lineas[len(lineas)-1].Cantidad
	ficha.Items = append(ficha.Items, lineas[:len(lineas)-1]...)

	log.Printf("[menu-repetir] %s repite su último pedido en código: %s", from, describirPedido(last))
	a.store.SetPedidoEnCurso(from, ficha)

	if _, hayUbicacion := a.store.GetLocation(from); !hayUbicacion {
		// Sin ubicación en curso hay que pedírsela igual, aunque sepamos a dónde fue la última
		// vez: la guardada puede ser vieja y el cliente estar en otro lado. Se nombra el destino
		// anterior para que entienda por qué se la pedimos otra vez.
		texto := fmt.Sprintf("¡Listo! %s como la última vez 🙌 ", describeItems(lineas))
		if destino := last.Destino(); destino != "" {
			texto += fmt.Sprintf("La última vez te lo entregamos en %s. Compárteme tu ubicación "+
				"por WhatsApp 📎 y te lo envío enseguida.", destino)
		} else {
			texto += "Compárteme tu ubicación por WhatsApp 📎 y te lo envío enseguida."
		}
		return texto, true
	}

	// Con ubicación y ficha completa ya no falta nada: se registra AQUÍ MISMO. Decirle "ya te
	// lo gestiono" y confiar en que el modelo lo registre en otro turno dejaría al cliente
	// esperando un pedido que nadie creó: es el error que este refactor viene a eliminar.
	// Se entra por runTool, igual que ConfirmarDireccion: es quien crea el ticket si falla.
	t := &turno{}
	salida := a.runTool(t, from, "registrar_pedido", argsDeLineas(lineas, nil))
	// El turno queda en el HISTORIAL (igual que ConfirmarProgramado): sin esto el modelo no se
	// entera de que el cliente pidió repetir ni de qué pasó, y en el siguiente mensaje contesta
	// como si no hubiera pasado nada.
	a.store.AppendUser(from, texto)
	if t.menuSent {
		// registrarPedido mandó un menú (p. ej. confirmar la dirección): ese menú ya salió.
		return "", true
	}
	respuesta := a.mensajeDelPedido(t, from)
	// Si NO se pudo registrar, el texto que devuelve la herramienta explica el motivo real
	// (fuera de horario, falta la cédula, catálogo caído). Se le da al modelo en el historial
	// para que la próxima respuesta sea coherente, en vez de un "inconveniente técnico" genérico.
	if !t.ultimoPedido.ok && !t.ultimoPedido.enEspera {
		a.store.AppendModel(from, respuesta+" [motivo interno: "+conversation.Recortar(salida, 200)+"]")
	} else {
		a.store.AppendModel(from, respuesta)
	}
	return respuesta, true
}
