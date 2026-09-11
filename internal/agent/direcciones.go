// Usar las direcciones GUARDADAS del cliente para entregar, sin re-compartir ubicación
// (specs/direcciones-y-pedido-multicolor.md, Fase B.4).
//
// La mitad de la Fase B ya existía: guardar la ubicación con nombre ("Casa"). Esta es la otra
// mitad, la que faltaba: poder USARLA. Sin esto se le enseñó al cliente a nombrar sus lugares y
// luego el bot se negaba a entenderlos — David (10/09) escribió "Ubicación tienda", "Tienda",
// tres veces, y el bot le pidió el clip 📎 otra vez en cada una.
//
// REGLA DURA (spec B.4): jamás se acepta una dirección ESCRITA a mano. Solo cuentan dos cosas:
// una ubicación compartida por WhatsApp, o una dirección que el cliente YA guardó con nombre
// (coordenadas verificadas en el backend). "Tienda" solo vale si "Tienda" existe en su lista.
package agent

import (
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/georoutes"
)

// BotonOtraUbicacion es la opción del menú de direcciones para compartir una ubicación nueva.
const BotonOtraUbicacion = "Otra ubicación"

// direccionesConNombre devuelve las direcciones que el cliente guardó con SU nombre ("Casa",
// "Tienda"). La interna 'WhatsApp' no cuenta: la pisa el backend en cada pedido y el cliente
// nunca la eligió. Best-effort: sin cuenta o con el backend caído devuelve nada y el flujo
// sigue como siempre (pedir la ubicación).
func (a *Agent) direccionesConNombre(from string) []georoutes.SavedDirection {
	account, hay := a.store.GetAccount(from)
	if !hay || account.JWT == "" {
		return nil
	}
	dirs, err := a.gr.GetDirections(account.JWT)
	if err != nil {
		log.Printf("[dir-guardada] %s: no se pudieron leer las direcciones (%v)", from, err)
		return nil
	}
	conNombre := dirs[:0]
	for _, d := range dirs {
		if strings.TrimSpace(d.Alias) == "" || strings.EqualFold(d.Alias, aliasInternoWhatsApp) {
			continue
		}
		conNombre = append(conNombre, d)
	}
	return conNombre
}

// aliasEnTexto busca cuál de las direcciones guardadas nombró el cliente. El texto tiene que
// SER el alias (tocó el botón, o lo escribió: "Tienda", "ubicación tienda", "a la casa"), no
// contenerlo dentro de una frase: "la tienda queda lejos" no es elegir la tienda.
func aliasEnTexto(dirs []georoutes.SavedDirection, texto string) (georoutes.SavedDirection, bool) {
	limpio := normalizar(texto)
	// Palabras de relleno con las que la gente envuelve el nombre: "ubicación tienda",
	// "envíalo a la casa", "mi casa". Se quitan y lo que quede debe ser el alias exacto.
	campos := strings.Fields(limpio)
	filtradas := campos[:0]
	for _, c := range campos {
		switch c {
		case "ubicacion", "direccion", "a", "la", "el", "en", "mi", "envialo", "enviamelo",
			"mandalo", "mandamelo", "para", "de":
			continue
		default:
			filtradas = append(filtradas, c)
		}
	}
	restante := strings.Join(filtradas, " ")
	if restante == "" {
		return georoutes.SavedDirection{}, false
	}
	for _, d := range dirs {
		if normalizar(d.Alias) == restante {
			return d, true
		}
	}
	return georoutes.SavedDirection{}, false
}

// ResponderDireccionGuardada resuelve, EN CÓDIGO, que el cliente elija una de sus direcciones
// guardadas como destino del pedido: por el botón del menú o escribiendo su nombre. Devuelve
// (respuesta, true) si se hizo cargo del turno.
//
// Solo actúa cuando TODO esto se cumple; si no, el mensaje sigue al modelo:
//   - hay un pedido en curso COMPLETO (color y cantidad de cada línea): elegir destino sin
//     pedido armado es conversación, no un botón;
//   - NO hay una ubicación fresca: con una recién compartida no hay nada que resolver (y así
//     no se pisa el menú de "¿la guardo con un nombre?", que usa texto libre);
//   - el texto nombra una dirección que el cliente SÍ tiene guardada (o el botón de compartir
//     otra). Un texto cualquiera jamás se convierte en destino: regla dura de la spec.
func (a *Agent) ResponderDireccionGuardada(from, texto string) (string, bool) {
	p, hay := a.store.GetPedidoEnCurso(from)
	if !hay || !p.Completo() {
		return "", false
	}
	if a.ubicacionEsDeAhora(from) {
		return "", false
	}

	// El botón "Otra ubicación" pide el pin de siempre, sin tocar nada más.
	if normalizarRespuesta(texto) == normalizarRespuesta(BotonOtraUbicacion) {
		return "Perfecto 👍 Compárteme tu ubicación por WhatsApp 📎 (botón de adjuntar → " +
			"Ubicación) y te lo envío enseguida.", true
	}

	dirs := a.direccionesConNombre(from)
	if len(dirs) == 0 {
		return "", false
	}
	d, ok := aliasEnTexto(dirs, texto)
	if !ok {
		return "", false
	}

	log.Printf("[dir-guardada] %s eligió %q; se registra el pedido a esa dirección", from, d.Alias)
	// Las coordenadas GUARDADAS pasan a ser la ubicación del pedido, frescas: registrarPedido
	// las acepta sin re-confirmar (el cliente ACABA de elegir este destino) y destinoDelPedido
	// las mapea de vuelta al alias, así que el backend usa la dirección con nombre tal cual,
	// sin pisarla (WppOrderEnDireccion).
	a.store.SetLocation(from, d.Latitude, d.Longitude)
	if calle := strings.TrimSpace(d.Direccion); calle != "" && !strings.HasPrefix(calle, "Ubicación compartida") {
		a.store.SetDireccionTexto(from, calle)
	}

	// Mismo camino que repetir-pedido: se registra AQUÍ, por runTool (quien crea el ticket si
	// falla), y el turno queda en el historial para que el modelo sepa qué pasó.
	t := &turno{}
	a.store.AppendUser(from, texto)
	a.runTool(t, from, "registrar_pedido", argsDeLineas(p.Lineas(), map[string]any{
		"direccion_confirmada": true,
	}))
	if t.menuSent {
		return "", true
	}
	respuesta := a.mensajeDelPedido(t, from)
	if t.ultimoPedido.ok {
		respuesta = fmt.Sprintf("📍 Va a %s. ", d.Alias) + respuesta
	}
	a.store.AppendModel(from, respuesta)
	return respuesta, true
}
