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
