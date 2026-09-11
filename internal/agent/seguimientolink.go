// Candado del ENLACE DE SEGUIMIENTO: el bot no puede darle al cliente el enlace de un pedido
// que ya no existe.
//
// El caso real (David, 10/09, detectado en la revisión del informe del 11/09): tras registrar
// el pedido #234, el modelo le mandó el enlace del #233 — que el cliente ya había cancelado.
// El enlace queda escrito en el historial del chat, y para el modelo es solo texto anterior que
// puede repetir. El cliente terminó siguiendo un pedido muerto.
//
// Por qué no basta con el prompt: es exactamente el mismo pecado que el pedido fantasma —el
// modelo AFIRMA algo (aquí, "sigue tu pedido en este enlace") sin que sea verdad—, y la lección
// de este proyecto es que una regla escrita no lo impide. Así que se comprueba contra el estado
// DURABLE: el único enlace que puede salir es el del pedido que está vivo AHORA. Cualquier otro
// se borra del texto antes de enviarlo.
//
// La página del backend ya protege los datos (un pedido cancelado muestra "este pedido fue
// cancelado" y no el mapa ni el repartidor), así que esto no es un agujero de seguridad: es que
// el cliente no puede recibir un enlace que lo lleva a un pedido que no es el suyo de ahora.
package agent

import (
	"log"
	"regexp"
	"strings"
)

// enlaceSeguimientoRe encuentra cualquier URL de seguimiento dentro de un texto, sea cual sea el
// dominio: lo que lo identifica es la ruta /seguimiento/<token>/. Se corta en el primer espacio
// o salto porque el modelo suele dejarlo en su propia línea.
var enlaceSeguimientoRe = regexp.MustCompile(`https?://\S*?/seguimiento/[^\s)\]]+`)

// limpiarEnlaceDeSeguimiento quita del texto todo enlace de seguimiento que NO sea el del pedido
// vivo del cliente. Devuelve el texto corregido y si tuvo que intervenir.
//
// Casos:
//   - sin enlaces en el texto: no toca nada;
//   - enlace == el vigente: lo deja pasar (es verdad);
//   - enlace distinto, o sin pedido vivo: lo quita y deja el mensaje legible.
func limpiarEnlaceDeSeguimiento(texto, vigente string) (string, bool) {
	encontrados := enlaceSeguimientoRe.FindAllString(texto, -1)
	if len(encontrados) == 0 {
		return texto, false
	}
	intervino := false
	limpio := texto
	for _, enlace := range encontrados {
		if vigente != "" && strings.TrimRight(enlace, ".,;:") == vigente {
			continue // es el del pedido que está en camino: puede salir
		}
		limpio = strings.ReplaceAll(limpio, enlace, "")
		intervino = true
	}
	if !intervino {
		return texto, false
	}
	// Al quitar el enlace suele quedar la invitación colgando ("Sigue a tu repartidor aquí:")
	// con dos puntos y líneas en blanco. Se limpia para que el mensaje no quede a medias.
	limpio = regexp.MustCompile(`(?i)[^\n.!?]*sigue[^\n.!?]*(en vivo|repartidor)[^\n.!?]*:?\s*`).ReplaceAllString(limpio, "")
	limpio = regexp.MustCompile(`[ \t]+\n`).ReplaceAllString(limpio, "\n")
	limpio = regexp.MustCompile(`\n{3,}`).ReplaceAllString(limpio, "\n\n")
	return strings.TrimSpace(limpio), true
}

// revisarEnlaceDeSeguimiento aplica el candado sobre la respuesta del turno.
func (a *Agent) revisarEnlaceDeSeguimiento(from, reply string) string {
	limpio, intervino := limpiarEnlaceDeSeguimiento(reply, a.store.GetSeguimientoActivo(from))
	if intervino {
		log.Printf("[seguimiento] %s: se quitó un enlace de seguimiento que no es del pedido vivo", from)
	}
	return limpio
}
