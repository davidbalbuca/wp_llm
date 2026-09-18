// CÓMO SE LE NOMBRA AL CLIENTE EL LUGAR AL QUE VA SU GAS.
//
// Mandar el gas a otra casa es el error más caro del negocio, y el que más tarde se descubre: el
// cliente solo se enfrenta a él cuando el repartidor no llega. Así que cada vez que el bot habla
// de una ubicación tiene que decir CUÁL es, con palabras que el cliente reconozca.
//
// Pedido del dueño (18/09): "revisar lo del detalle de la última ubicación, que deba decir la
// dirección/calles que están registradas en el backend, o si ya tiene nombre decir eso".
//
// Al hacer el inventario aparecieron TRES formatos distintos para lo mismo, y dos perdían
// información:
//
//	confirmación del pedido  →  "Casa (Av. Solano 123)"   ✅ completo
//	LastOrder.Destino()      →  "Casa"                     ❌ pierde la calle
//	menú de dirección        →  "Av. Solano 123"           ❌ pierde el NOMBRE que puso el cliente
//
// El último era el peor: el cliente que guardó su ubicación como "Casa" recibía "¿te lo enviamos
// a Av. Solano 123?" y tenía que reconocer su propia dirección escrita por el geocodificador,
// cuando él le había puesto un nombre justamente para no tener que hacer eso.
//
// Ahora hay UNA sola función, y todos la usan. De dónde salen los datos ya estaba bien: la calle
// es la que resolvió el BACKEND (SavedDirection.Direccion de GetDirections), no el geocoding de
// Google, y el alias es el nombre que el cliente escribió.
package agent

import (
	"strings"

	"wp-llm-gas/internal/conversation"
)

// destinoLegible arma el destino en palabras que el cliente reconozca.
//
// Con alias y calle da las dos ("Casa (Av. Loja y Remigio Crespo)"): el alias solo no distingue
// si guardó mal la ubicación, y la calle sola no le dice cuál de sus direcciones es. Sin ninguna
// de las dos devuelve vacío — las coordenadas crudas no le sirven a nadie para saber a dónde va
// su gas, y es mejor no decir nada que decir "-2.897, -79.004".
func destinoLegible(alias, calle string) string {
	alias, calle = strings.TrimSpace(alias), strings.TrimSpace(calle)
	// El alias interno que el backend pone en cada pedido del bot no es un nombre que el cliente
	// eligió: no se le puede decir "te lo envío a WhatsApp".
	if strings.EqualFold(alias, aliasInternoWhatsApp) {
		alias = ""
	}
	// Ni el texto de respaldo que escribe el backend cuando no pudo resolver la calle.
	if esDireccionDeRespaldo(calle) {
		calle = ""
	}
	switch {
	case alias != "" && calle != "":
		return alias + " (" + calle + ")"
	case alias != "":
		return alias
	default:
		return calle
	}
}

// esDireccionDeRespaldo reconoce el texto que el backend guarda cuando no pudo resolver una calle
// de verdad ("Ubicación compartida por WhatsApp (-2.9, -79.0)"). Mostrárselo al cliente es peor
// que no decirle nada: son las coordenadas disfrazadas de dirección.
//
// La comprobación estaba repetida en tres sitios con el mismo literal; aquí queda una sola vez.
func esDireccionDeRespaldo(calle string) bool {
	return strings.HasPrefix(strings.TrimSpace(calle), prefijoDireccionRespaldo)
}

// prefijoDireccionRespaldo es el comienzo del texto que pone el backend cuando solo tiene el pin.
// Igual que el alias interno, vive en `conversation` para no tener dos literales que se
// desincronicen: LastOrder.Destino() filtra por el mismo prefijo.
const prefijoDireccionRespaldo = conversation.PrefijoDireccionRespaldo

// calleGuardada devuelve la calle que se conoce de la ubicación actual del cliente ("" si no hay,
// o si lo único guardado es el texto de respaldo con las coordenadas).
func (a *Agent) calleGuardada(from string) string {
	calle, hay := a.store.GetDireccionTexto(from)
	if !hay || esDireccionDeRespaldo(calle) {
		return ""
	}
	return strings.TrimSpace(calle)
}

// aliasDeLaUbicacionGuardada busca si la ubicación que el bot tiene guardada del cliente coincide
// con alguna de sus direcciones CON NOMBRE, y devuelve ese nombre ("Casa"). "" si no coincide con
// ninguna, o si no se pueden leer sus direcciones.
//
// Se compara por COORDENADAS (±150 m), no se toma la primera de la lista: el cliente puede tener
// Casa, Trabajo y la 'WhatsApp' que el bot pisa en cada pedido, y quedarse con cualquiera daría un
// nombre equivocado — que en este flujo significa que confirma un destino y el gas va a otro.
//
// Best-effort: ante cualquier fallo devuelve "" y el mensaje sale solo con la calle, como antes.
// Nunca rompe el flujo del pedido por no haber podido adornar un texto.
func (a *Agent) aliasDeLaUbicacionGuardada(from string) string {
	loc, hay := a.store.GetLocation(from)
	if !hay {
		return ""
	}
	for _, d := range a.direccionesConNombre(from) {
		if mismaUbicacion(d.Latitude, d.Longitude, loc.Latitude, loc.Longitude) {
			return strings.TrimSpace(d.Alias)
		}
	}
	return ""
}
