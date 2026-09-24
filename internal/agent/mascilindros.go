// "Más de 3": la salida del menú de cantidad para quien necesita más cilindros de los sugeridos.
//
// Pedido del dueño (24-sep): el menú de cantidad debe ser una LISTA desplegable, y para más de
// tres cilindros que el cliente escriba el número.
//
// El límite viene de WhatsApp: los botones tappables son 3 como máximo. Con [1/2/3] los tres
// botones estaban ocupados y el cliente que quería 6 —hay dos pedidos reales de 6 en producción—
// tenía que adivinar que podía escribirlo, sin que nada en el chat se lo dijera. Con cuatro
// opciones WhatsApp manda una lista desplegable y la salida cabe.
//
// Se resuelve en código, como el resto de los menús cerrados: la respuesta a un botón es un
// conjunto cerrado y no puede depender de que el modelo interprete bien la opción.
package agent

import (
	"log"
	"strings"
)

// ResponderMasCilindros atiende el toque en "Más de 3": le pide el número al cliente. No registra
// ni cambia nada; solo contesta la pregunta que él acaba de hacer al elegir esa opción.
//
// El número que escriba después NO necesita interceptor: anotarDelMensaje (encurso.go) ya lee un
// número suelto —en dígitos o en palabras— como la cantidad de la línea en curso, hasta 20. Es el
// mismo camino que si lo hubiera escrito sin pasar por el menú, y añadir un segundo sitio que
// decidiera lo mismo sería una fuente de discrepancias.
func (a *Agent) ResponderMasCilindros(from, texto string) (string, bool) {
	if normalizarRespuesta(texto) != normalizarRespuesta(BotonMasCilindros) {
		return "", false
	}
	// Solo tiene sentido si de verdad se está armando un pedido: si no hay color elegido, la
	// pregunta "¿cuántos?" no se ha hecho y esto sería una respuesta suelta sin contexto.
	p, _ := a.store.GetPedidoEnCurso(from)
	if len(p.Lineas()) == 0 {
		return "", false
	}

	log.Printf("[cantidad] %s eligió \"%s\": se le pide el número", from, BotonMasCilindros)
	return "¡Claro! 😊 Escríbeme cuántos cilindros necesitas (por ejemplo: 6) y lo anoto.", true
}

// esOpcionMasCilindros dice si un texto es esa opción del menú. Lo usa el candado del menú para
// no tratarla como una cantidad: sin esto, "Más de 3" caería en el parseo de números y el 3 se
// leería como la cantidad pedida — justo lo contrario de lo que el cliente quiso decir.
func esOpcionMasCilindros(texto string) bool {
	return normalizarRespuesta(texto) == normalizarRespuesta(BotonMasCilindros)
}

// contieneLaOpcionDeMas dice si una lista de opciones incluye la salida para cantidades mayores.
// Sirve a los tests para fijar que el menú de cantidad siempre la ofrezca.
func contieneLaOpcionDeMas(opciones []string) bool {
	for _, o := range opciones {
		if strings.EqualFold(o, BotonMasCilindros) {
			return true
		}
	}
	return false
}
