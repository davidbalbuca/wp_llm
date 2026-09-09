// Guardar la ubicación del cliente con un nombre suyo ("Casa"), para que en el pedido
// siguiente el bot pueda decirle "¿te lo envío otra vez a Casa?" en vez de pedírsela de nuevo
// (specs/direcciones-y-pedido-multicolor.md, Fase B).
//
// Se le pregunta AL RECIBIR la ubicación, no después del pedido: así la dirección se crea UNA
// vez con su nombre ya puesto. Preguntar al final obligaría a crearla primero como 'WhatsApp' y
// renombrarla después, con un paso más y una forma más de fallar.
package agent

import (
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/whatsapp"
)

// Botones del menú de guardar ubicación. WhatsApp corta los títulos en 20 caracteres.
const (
	BotonGuardarCasa    = "Guardar como Casa"
	BotonGuardarTrabajo = "Guardar cmo Trabajo"
	BotonNoGuardar      = "No, gracias"
)

// maxLargoAlias es el tope del nombre que el cliente puede escribir. El modelo de la BD admite
// 50; se corta antes porque un alias es una etiqueta ("Casa de mi mamá"), y un texto más largo
// casi siempre es otra cosa —una frase, una pregunta— que no debería acabar de nombre.
const maxLargoAlias = 30

// OfrecerGuardarUbicacion le pregunta al cliente si quiere ponerle nombre a la ubicación que
// acaba de compartir. Devuelve true si el menú salió (y por tanto ya se le respondió algo).
//
// NO se ofrece -y devuelve false- cuando:
//   - esa misma ubicación ya está guardada con nombre (±150 m): preguntar en cada pedido
//     cansa y hace que deje de leer los menús;
//   - no se pueden leer sus direcciones: ante la duda, no se molesta al cliente.
//
// REGLA DURA: esto NO bloquea el pedido. Si el cliente ignora el menú y sigue pidiendo gas, el
// flujo continúa con normalidad y la oferta caduca sola.
func (a *Agent) OfrecerGuardarUbicacion(from string, lat, lng float64) bool {
	account, hay := a.store.GetAccount(from)
	if !hay || account.JWT == "" {
		// Cliente nuevo: aún no tiene cuenta ni direcciones. Se le ofrecerá tras su primer
		// pedido, cuando ya exista en el backend.
		return false
	}
	dirs, err := a.gr.GetDirections(account.JWT)
	if err != nil {
		log.Printf("[guardar-ubic] %s: no se pudieron leer las direcciones (%v); no se ofrece", from, err)
		return false
	}
	if nombre, ya := ubicacionYaNombrada(dirs, lat, lng); ya {
		log.Printf("[guardar-ubic] %s ya tiene esta ubicación como %q; no se repregunta", from, nombre)
		return false
	}

	cuerpo := "¿Quieres que guarde esta ubicación con un nombre? Así la próxima vez te lo " +
		"envío sin pedírtela de nuevo 😊"
	opciones := []string{BotonGuardarCasa, BotonGuardarTrabajo, BotonNoGuardar}
	if err := whatsapp.SendMenu(a.cfg, from, cuerpo, opciones); err != nil {
		log.Printf("[guardar-ubic] %s: el menú falló (%v); el pedido sigue igual", from, err)
		return false
	}

	a.store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{
		Latitude: lat, Longitude: lng,
	})
	a.store.AppendModel(from, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
	a.store.LogMessage(from, "bot", cuerpo)
	return true
}

// ubicacionYaNombrada dice si el cliente YA le puso nombre a esa ubicación, y cuál.
//
// La dirección interna 'WhatsApp' NO cuenta: el backend la reemplaza en cada pedido, así que
// estar ahí no significa que el cliente tenga guardado ese sitio. Si contara, a un cliente con
// pedidos previos nunca se le ofrecería nombrar su casa y la feature entera quedaría muerta.
func ubicacionYaNombrada(dirs []georoutes.SavedDirection, lat, lng float64) (string, bool) {
	for _, d := range dirs {
		if strings.EqualFold(d.Alias, aliasInternoWhatsApp) {
			continue
		}
		if mismaUbicacion(d.Latitude, d.Longitude, lat, lng) {
			return d.Alias, true
		}
	}
	return "", false
}

// ResponderGuardarUbicacion resuelve la respuesta al menú de guardar, en código (mismo patrón
// que el resto de los menús): el nombre se guarda AQUÍ, sin depender de que el modelo llame a
// nada. Devuelve (respuesta, true) si lo resolvió; (_, false) si le toca al modelo.
func (a *Agent) ResponderGuardarUbicacion(from, texto string) (string, bool) {
	pend, hay := a.store.GetPendingGuardarUbicacion(from)
	if !hay {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)

	switch {
	case respuesta == normalizarRespuesta(BotonNoGuardar) || respuestasNegativas[respuesta]:
		a.store.ClearPendingGuardarUbicacion(from)
		log.Printf("[guardar-ubic] %s no quiso guardar la ubicación", from)
		return "Sin problema 😊 Sigamos con tu pedido.", true

	case respuesta == normalizarRespuesta(BotonGuardarCasa):
		return a.guardarUbicacionComo(from, "Casa", pend)

	case respuesta == normalizarRespuesta(BotonGuardarTrabajo):
		return a.guardarUbicacionComo(from, "Trabajo", pend)
	}

	// Texto libre: el cliente escribió el nombre que quiere ("Casa de mi mamá"). Solo se acepta
	// si PARECE una etiqueta; si no, va al modelo, que es quien sabe leer una frase.
	if nombre, ok := aliasEscrito(texto); ok {
		return a.guardarUbicacionComo(from, nombre, pend)
	}
	return "", false
}

// guardarUbicacionComo crea la dirección con ese nombre y confirma. Si el backend la rechaza,
// se lo dice con naturalidad y sigue: el cliente vino a pedir gas, no a administrar direcciones.
func (a *Agent) guardarUbicacionComo(from, nombre string, pend conversation.PendingGuardarUbicacion) (string, bool) {
	a.store.ClearPendingGuardarUbicacion(from)

	account, hay := a.store.GetAccount(from)
	if !hay || account.JWT == "" {
		log.Printf("[guardar-ubic] %s: sin cuenta para guardar %q", from, nombre)
		return "Seguimos con tu pedido 😊", true
	}

	// La calle si se conoce; si no, una etiqueta con el nombre. El campo lo leen PERSONAS (el
	// panel, el conductor), así que nunca puede quedar vacío.
	direccion, _ := a.store.GetDireccionTexto(from)
	if strings.TrimSpace(direccion) == "" {
		direccion = "Ubicación de " + nombre + " (compartida por WhatsApp)"
	}

	if err := a.gr.CreateDirection(account.JWT, nombre, direccion, pend.Latitude, pend.Longitude); err != nil {
		log.Printf("[guardar-ubic] %s: el backend rechazó guardar %q (%v)", from, nombre, err)
		return "Seguimos con tu pedido 😊", true
	}

	log.Printf("[guardar-ubic] %s guardó su ubicación como %q", from, nombre)
	// El nombre entra en el último pedido si es la MISMA ubicación, para que "repetir" pueda
	// decir "a Casa" desde ya y no a partir del pedido siguiente.
	if last, hayLast := a.store.GetLastOrder(from); hayLast &&
		mismaUbicacion(last.Latitude, last.Longitude, pend.Latitude, pend.Longitude) {
		last.Alias = nombre
		a.store.SetLastOrder(from, last)
	}
	return fmt.Sprintf("¡Listo! La guardé como %s 🏠 La próxima vez solo dime que te lo envíe "+
		"ahí y no tendrás que compartir tu ubicación de nuevo.", nombre), true
}

// aliasEscrito decide si un texto libre es el NOMBRE que el cliente le está poniendo a la
// ubicación, o cualquier otra cosa que le toca contestar al modelo.
//
// Se acepta poco a propósito: guardar "cuánto cuesta el de 15 kilos" como nombre de una
// dirección es peor que no guardar nada —queda en su lista, se la ofrecemos en el pedido
// siguiente y no significa nada para él—.
func aliasEscrito(texto string) (string, bool) {
	limpio := strings.TrimSpace(texto)
	if limpio == "" || len([]rune(limpio)) > maxLargoAlias {
		return "", false
	}
	// Una etiqueta son una o dos palabras ("Casa", "Casa de mi mamá"); una frase de cinco no.
	if len(strings.Fields(limpio)) > 4 {
		return "", false
	}
	// Nada de dígitos: un teléfono, una cédula o una cantidad no son un nombre de dirección.
	// Y nada de signos de pregunta: eso es una consulta, no una etiqueta.
	if strings.ContainsAny(limpio, "0123456789?¿") {
		return "", false
	}
	return limpio, true
}
