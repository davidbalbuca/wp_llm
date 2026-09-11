// Cancelar el pedido resuelto EN CÓDIGO, sin pasar por el modelo.
//
// Por qué existe este archivo: el 11/09, revisando producción, el conteo fue demoledor —
// veces que el MODELO llamó cancelar_pedido: 0. Veces que el candado de frases tuvo que
// forzarlo: 6. Todas las cancelaciones de clientes reales las salvó el paracaídas.
//
// Y el paracaídas es frágil por construcción: se dispara leyendo el TEXTO que el modelo
// redacta ("he cancelado tu pedido") y comparándolo con una lista de secuencias. Si el modelo
// elige otras palabras, no salta nadie y el cliente se queda con un pedido vivo creyendo que
// lo canceló. Eso ya pasó el 09/09: el cliente tuvo que insistir tres veces hasta que el bot
// admitió que su pedido seguía activo. En aquel momento se arregló el DETECTOR (afirmaSecuencia
// tolerante); esto es lo que faltaba: que la cancelación no dependa del modelo en absoluto.
//
// "Cancelar mi pedido" no es una conversación: es un botón dicho con palabras. Mismo criterio
// que ConfirmarDireccion, ConfirmarProgramado, ResponderMenuEspera y ResponderRepetirPedido,
// que llevan meses funcionando. El candado de frases (forzarCancelacionSiHaceFalta) SE QUEDA
// como red de seguridad para el camino conversacional; deja de ser quien hace el trabajo.
package agent

import (
	"log"
	"strings"
)

// BotonCancelarPedido es el texto del botón de cancelar en los menús. Vive aquí para que el
// interceptor y quien arme el menú usen exactamente el mismo texto.
const BotonCancelarPedido = "Cancelar pedido"

// pideCancelarPedido dice si el mensaje es, inequívocamente, la orden de cancelar el pedido
// EN CURSO. Es deliberadamente estricto: cancelar el pedido de alguien que no lo pidió es
// peor que dejar pasar un mensaje ambiguo al modelo.
//
// Se compara el mensaje ENTERO normalizado contra una lista cerrada (mismo criterio que
// respuestasAfirmativas/Negativas), y además se aceptan unas pocas secuencias con la palabra
// "pedido" dentro. Lo que NO entra aquí: preguntas ("¿puedo cancelar?"), condicionales ("si
// no llega en 10 minutos cancela"), y cancelaciones de OTRA cosa ("cancela la programación",
// que tiene su propia herramienta).
func pideCancelarPedido(texto string) bool {
	// Una pregunta no es una orden: "¿me puedes cancelar el pedido?" quiere saber si se puede.
	if strings.ContainsAny(texto, "?¿") {
		return false
	}
	normalizado := normalizarRespuesta(texto)

	// La PROGRAMACIÓN tiene su propia herramienta (cancelar_programacion): si el cliente nombra
	// la entrega agendada, esto no es lo suyo y va al modelo.
	for _, otra := range []string{"programacion", "programada", "agendada", "agendamiento"} {
		if strings.Contains(normalizado, otra) {
			return false
		}
	}

	// Mensaje ENTERO igual a una de estas: el conjunto cerrado de "cancelar y punto".
	exactas := map[string]bool{
		"cancelar": true, "cancela": true, "cancelalo": true, "cancelar pedido": true,
		"cancela el pedido": true, "cancelar el pedido": true, "cancela mi pedido": true,
		"cancelar mi pedido": true, "cancelame el pedido": true, "anular pedido": true,
		"anula el pedido": true, "anular el pedido": true, "anula mi pedido": true,
		"anular mi pedido": true, "ya no lo quiero": true, "ya no lo necesito": true,
		"ya no quiero el pedido": true, "quiero cancelar": true, "quiero cancelar mi pedido": true,
		"deseo cancelar": true, "necesito cancelar": true, "mejor cancela": true,
		"mejor cancelalo": true, "cancelemos": true,
	}
	if exactas[normalizado] {
		return true
	}

	// Y las redacciones con palabras intercaladas ("cancélame POR FAVOR EL pedido" son 3).
	// Se exige la palabra PEDIDO: un "cancela" suelto ya está cubierto arriba, y sin esa ancla
	// una frase larga cualquiera con "cancelar" dentro entraría de más. La holgura 3 es la
	// misma que usan los demás candados de texto del proyecto.
	return afirmaSecuencia(texto, [][]string{
		{"cancelar", "pedido"}, {"cancela", "pedido"}, {"cancelame", "pedido"},
		{"anular", "pedido"}, {"anula", "pedido"},
		{"quiero", "cancelar", "pedido"}, {"ya", "no", "quiero", "pedido"},
	}, 3)
}

// ResponderCancelacion cancela el pedido del cliente EN CÓDIGO cuando lo pide sin ambigüedad.
// Devuelve el mensaje para el cliente y si se hizo cargo del turno; con false el mensaje sigue
// su camino normal hacia el modelo.
//
// Solo actúa si el cliente TIENE un pedido vivo: si no lo tiene, "cancelar" puede referirse a
// otra cosa (una programación, un pedido ya entregado, la conversación misma) y eso es
// conversación — el modelo la atiende mejor.
func (a *Agent) ResponderCancelacion(from, texto string) (string, bool) {
	if normalizarRespuesta(texto) != normalizarRespuesta(BotonCancelarPedido) && !pideCancelarPedido(texto) {
		return "", false
	}
	if _, hay := a.store.GetActivePedido(from); !hay {
		// Sin pedido activo no hay nada que cancelar aquí. Puede ser una programación o una
		// confusión: que el modelo se lo explique con amabilidad.
		return "", false
	}

	log.Printf("[cancelacion] %s pidió cancelar; se cancela en código sin pasar por el modelo", from)
	// Por runTool, igual que el resto de interceptores: es quien marca el turno y crea el
	// ticket si hace falta. cancelarPedido limpia el pedido activo y la ficha en todos los
	// caminos en que el pedido queda muerto (incluido "ya estaba cancelado").
	t := &turno{}
	salida := a.runTool(t, from, "cancelar_pedido", map[string]any{})

	// El turno queda en el HISTORIAL (igual que ConfirmarProgramado y repetir-pedido): sin esto
	// el modelo no se entera de que el cliente canceló y en el siguiente mensaje contesta como
	// si el pedido siguiera vivo.
	a.store.AppendUser(from, texto)

	// La verdad es el ESTADO, no el texto que devolvió la herramienta: si el pedido activo
	// sigue ahí, la cancelación no ocurrió por más que la salida suene bien.
	if _, sigue := a.store.GetActivePedido(from); sigue {
		log.Printf("[cancelacion] %s: el backend NO canceló el pedido; se avisa al equipo", from)
		a.crearTicketSoporte(from, "No se pudo cancelar el pedido",
			"El cliente pidió cancelar y el backend no lo canceló. El pedido SIGUE VIVO; hay que "+
				"cancelarlo a mano. Detalle: "+salida)
		msg := "Disculpa 🙏, tuve un problema al cancelar tu pedido. Ya avisé al equipo para que lo " +
			"cancele enseguida. Lamento la molestia."
		a.store.AppendModel(from, msg)
		return msg, true
	}

	msg := "Listo, cancelé tu pedido 🙏. Cuando necesites tu gas, aquí estoy para ayudarte 😊"
	a.store.AppendModel(from, msg)
	return msg, true
}

// --- La ENTREGA AGENDADA: el mismo problema, el mismo remedio ---
//
// El inventario del 11/09 (herramientas contra protecciones) destapó que cancelar_programacion
// era la ÚNICA acción que cambia estado sin interceptor NI candado de texto: el gemelo exacto
// del bug de cancelar_pedido, pero sin nada. Y peor por dos razones. Una: pideCancelarPedido
// manda a propósito "cancela la programación" al modelo —por ser otra herramienta—, así que se
// le abrió la puerta justo a la habitación sin red. Dos: si falla, el cliente no se entera hoy;
// se entera cuando le llega a la puerta, a la hora agendada, un pedido que creía cancelado.

// pideCancelarProgramacion reconoce la orden de cancelar la ENTREGA AGENDADA. Es más estricto
// que su gemelo: exige que el cliente NOMBRE la programación. Un "cancelar" suelto no entra —
// puede ser el menú de espera, un pedido vivo o la conversación misma, y equivocarse aquí es
// borrarle al cliente una entrega que sí quería. Lo ambiguo va al modelo, que para eso está.
func pideCancelarProgramacion(texto string) bool {
	if strings.ContainsAny(texto, "?¿") {
		return false
	}
	normalizado := normalizarRespuesta(texto)

	exactas := map[string]bool{
		"cancelar programacion": true, "cancela la programacion": true,
		"cancelar la programacion": true, "cancela mi programacion": true,
		"cancelar mi programacion": true, "anular programacion": true,
		"anula la programacion": true, "cancelar entrega programada": true,
		"cancela la entrega programada": true, "cancelar entrega agendada": true,
		"cancela la entrega agendada": true, "cancelar mi entrega agendada": true,
		"cancelar agendamiento": true, "ya no quiero la entrega programada": true,
		"ya no quiero la programacion": true, "cancelar el agendamiento": true,
	}
	if exactas[normalizado] {
		return true
	}

	// Redacciones con palabras intercaladas. Toda secuencia ancla en la palabra que nombra la
	// entrega agendada: sin esa ancla no se toca nada.
	return afirmaSecuencia(texto, [][]string{
		{"cancelar", "programacion"}, {"cancela", "programacion"},
		{"cancelame", "programacion"}, {"anular", "programacion"}, {"anula", "programacion"},
		{"cancelar", "entrega", "programada"}, {"cancela", "entrega", "programada"},
		{"cancelar", "entrega", "agendada"}, {"cancela", "entrega", "agendada"},
		{"cancelar", "agendamiento"}, {"cancela", "agendamiento"},
		{"ya", "no", "quiero", "programacion"}, {"ya", "no", "quiero", "entrega", "programada"},
		{"ya", "no", "quiero", "entrega", "agendada"},
	}, 3)
}

// ResponderCancelarProgramacion cancela la entrega agendada EN CÓDIGO. Misma forma que
// ResponderCancelacion: solo actúa si hay algo vivo que cancelar, ejecuta por runTool, y decide
// si le confirma al cliente MIRANDO EL ESTADO — nunca el texto que devolvió la herramienta.
func (a *Agent) ResponderCancelarProgramacion(from, texto string) (string, bool) {
	if !pideCancelarProgramacion(texto) {
		return "", false
	}
	if !a.store.TieneProgramacionViva(from) {
		// Sin entrega agendada no hay nada que cancelar aquí. Puede referirse a un pedido
		// inmediato o a una confusión: el modelo se lo explica mejor.
		return "", false
	}

	log.Printf("[cancelacion] %s pidió cancelar su entrega agendada; se cancela en código", from)
	t := &turno{}
	salida := a.runTool(t, from, "cancelar_programacion", map[string]any{})
	a.store.AppendUser(from, texto)

	// La verdad es el ESTADO. Si la programación sigue viva, al cliente le llegaría el pedido a
	// la hora agendada creyendo que lo canceló — el fallo más caro de los dos, porque no se
	// descubre hoy sino en su puerta.
	if a.store.TieneProgramacionViva(from) {
		log.Printf("[cancelacion] %s: la entrega agendada NO se canceló; se avisa al equipo", from)
		a.crearTicketSoporte(from, "No se pudo cancelar la entrega agendada",
			"El cliente pidió cancelar su entrega programada y sigue viva. Hay que cancelarla a "+
				"mano antes de la hora agendada. Detalle: "+salida)
		msg := "Disculpa 🙏, tuve un problema al cancelar tu entrega programada. Ya avisé al equipo " +
			"para que la cancele enseguida. Lamento la molestia."
		a.store.AppendModel(from, msg)
		return msg, true
	}

	msg := "Listo, cancelé tu entrega programada 🙏. Cuando necesites tu gas, aquí estoy para " +
		"ayudarte 😊"
	a.store.AppendModel(from, msg)
	return msg, true
}
