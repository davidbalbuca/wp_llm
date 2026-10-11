// Oferta de color alterno cuando el pedido falla por falta de conductor
// (specs/cobertura-y-color-alterno.md, Fase C).
//
// El flujo, decisión del dueño (08-sep): buscar lo que el cliente pidió → si no hay conductor,
// mirar las equivalencias configuradas en el panel → si alguna tiene conductor CON stock,
// ofrecer el cambio → sí = registrar en ese mismo turno → no = queda para gestión manual y se
// agradece. La oferta solo se hace si el backend confirmó un conductor real: ofrecer un color
// que también va a fallar es peor que no ofrecer nada.
package agent

import (
	"fmt"
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// ofrecerColorAlterno consulta las alternativas y, si hay, manda el menú de cambio. Devuelve
// (texto para el modelo, true) si la oferta salió; (_, false) si no hay alternativas o algo
// falló — en ese caso el llamador sigue con el flujo de espera de siempre.
func (a *Agent) ofrecerColorAlterno(t *turno, from string, lat, lng float64,
	producto georoutes.Product, color georoutes.Color, cantidad int) (string, bool) {

	alternativas, err := a.gr.CheckColorAlternatives(lat, lng, producto.IDProducto, color.ID, cantidad)
	if err != nil {
		// Best-effort: la consulta de alternativas jamás puede romper el flujo del pedido.
		log.Printf("[color-alterno] %s: no se pudo consultar alternativas (%v); sigue la espera", from, err)
		return "", false
	}
	if len(alternativas) == 0 {
		return "", false
	}

	// UNA sola alternativa, la primera: es una decisión simple de sí o no, no un menú de
	// colores. Encadenar ofertas ("¿y azul? ¿y amarillo?") marea al cliente.
	alt := alternativas[0]
	log.Printf("[color-alterno] %s: sin conductor para %s; se ofrece %s (%d conductores)",
		from, color.Nombre, alt.Color, alt.Conductores)

	cuerpo := fmt.Sprintf("No tengo cilindros %s disponibles cerca 😔, pero sí %s, que es el "+
		"mismo gas. ¿Te lo envío en %s?", color.Nombre, alt.Color, alt.Color)
	opciones := []string{botonSiAlterno(alt.Color), BotonNoCambioColor}
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[color-alterno] %s: el menú de cambio falló (%v); sigue la espera", from, err)
		return "", false
	}

	a.store.SetPendingColorSwap(from, conversation.PendingColorSwap{
		ColorOriginal: color.Nombre,
		ColorAlterno:  alt.Color,
		Cantidad:      cantidad,
	})
	t.menuSent = true
	t.lastMenuText = cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	a.store.AppendModel(from, t.lastMenuText)
	return "Se le ofreció al cliente cambiar al color " + alt.Color + " (sí tiene conductor). " +
		"ESPERA su respuesta.", true
}

// Botones del menú de cambio de color. El sí lleva el color (max ~20 chars en WhatsApp).
const BotonNoCambioColor = "No, gracias"

// BotonSeguirEsperando es el "no" de la oferta que sale DURANTE la espera: el cliente no quiere
// cambiar de color y sigue esperando el suyo.
const BotonSeguirEsperando = "No, espero el mío"

// chequeoColorAlterno es cada cuanto se vuelve a preguntar por colores equivalentes mientras el
// cliente espera. La busqueda se consulta cada 7 s; las alternativas no hace falta mirarlas tan
// seguido.
const chequeoColorAlterno = 30 * time.Second

// revisarColorAlternoEnEspera: el cliente espera repartidor para su color y, MIENTRAS, puede
// conectarse un conductor con un color equivalente. Antes eso no se miraba nunca: la oferta de
// cambio solo se hacia al registrar el pedido. Jaime (11/10) pidio amarillo a las 09:31 sin nadie
// cerca; a las 09:32 se conecto Juan Fajardo a 1,4 km con blanco (equivalente) y el bot siguio
// esperando un amarillo que no llegaba. Se ofrece UNA vez por espera, de un solo color, y nunca
// encima de otro menu que el cliente no haya contestado.
func (a *Agent) revisarColorAlternoEnEspera(from string, lat, lng float64, ahora time.Time) {
	w, ok := a.store.GetPendingWait(from)
	if !ok || w.ColorAlternoOfrecido || len(w.Items) > 1 || w.IDProducto == 0 || w.IDColor == 0 {
		return
	}
	if w.EsperandoRespuesta {
		return // tiene sin contestar el menu de esperar: no se le encima otro
	}
	if _, hay := a.store.GetPendingColorSwap(from); hay {
		return
	}
	if w.ChequeoColorAt > 0 && ahora.Sub(time.Unix(w.ChequeoColorAt, 0)) < chequeoColorAlterno {
		return
	}
	w.ChequeoColorAt = ahora.Unix()
	a.store.SetPendingWait(from, w)

	alternativas, err := a.gr.CheckColorAlternatives(lat, lng, w.IDProducto, w.IDColor, w.Cantidad)
	if err != nil {
		log.Printf("[color-alterno] %s: no se pudieron consultar alternativas durante la espera (%v)", from, err)
		return
	}
	if len(alternativas) == 0 {
		return
	}
	alt := alternativas[0]
	original := w.ColorNombre
	if original == "" {
		original = "que pediste"
	}
	cuerpo := fmt.Sprintf("Sigo buscando tu cilindro %s 🔎. Ahora sí tengo %s cerca, que es el "+
		"mismo gas. ¿Te lo envío en %s?", original, alt.Color, alt.Color)
	opciones := []string{botonSiAlterno(alt.Color), BotonSeguirEsperando}
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[color-alterno] %s: la oferta durante la espera no salió (%v)", from, err)
		return // no se marca: en el proximo chequeo se vuelve a intentar
	}
	a.store.SetPendingColorSwap(from, conversation.PendingColorSwap{
		ColorOriginal: w.ColorNombre,
		ColorAlterno:  alt.Color,
		Cantidad:      w.Cantidad,
		DuranteEspera: true,
	})
	if actual, sigue := a.store.GetPendingWait(from); sigue {
		actual.ColorAlternoOfrecido = true
		a.store.SetPendingWait(from, actual)
	}
	texto := cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	a.store.LogMessage(from, "system", texto)
	a.store.AppendModel(from, texto)
	log.Printf("[color-alterno] %s: durante la espera (busqueda %d) apareció %s con %d conductores; se ofrece",
		from, w.IDBusqueda, alt.Color, alt.Conductores)
}

func botonSiAlterno(color string) string {
	b := "Sí, " + strings.ToLower(color)
	if len(b) > 20 {
		b = b[:20]
	}
	return b
}

// ResponderCambioColor resuelve la respuesta al menú de cambio de color, en código (mismo
// patrón que el resto de menus.go): "sí" registra el pedido con el color alterno EN ESTE
// TURNO; "no" lo deja como no-asignado para gestión manual y se despide con cordialidad.
// Cualquier otra cosa va al modelo.
func (a *Agent) ResponderCambioColor(from, texto string) (string, bool) {
	sw, hay := a.store.GetPendingColorSwap(from)
	if !hay {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)

	switch {
	case respuesta == normalizarRespuesta(botonSiAlterno(sw.ColorAlterno)) || respuestasAfirmativas[respuesta]:
		a.store.ClearPendingColorSwap(from)
		log.Printf("[color-alterno] %s aceptó el cambio a %s; se registra en código", from, sw.ColorAlterno)
		if sw.DuranteEspera {
			// Se cierra la busqueda del color original ANTES de registrar: si no, quedarian dos
			// pedidos buscando conductor (el original y el del color nuevo).
			a.cerrarEsperaYBusqueda(from, "acepto el color "+sw.ColorAlterno)
		}
		// Por runTool, como todos los interceptores: es quien crea el ticket si falla.
		t := &turno{}
		a.runTool(t, from, "registrar_pedido", map[string]any{
			"color": sw.ColorAlterno, "cantidad": sw.Cantidad,
		})
		if t.menuSent {
			return "", true // registrarPedido mandó otro menú (p. ej. confirmar dirección)
		}
		return a.mensajeDelPedido(t, from), true

	case sw.DuranteEspera && (respuesta == normalizarRespuesta(BotonSeguirEsperando) ||
		respuesta == normalizarRespuesta(BotonNoCambioColor) || respuestasNegativas[respuesta]):
		// Durante la espera, "no" es "sigo esperando el mio": la busqueda sigue abierta.
		a.store.ClearPendingColorSwap(from)
		log.Printf("[color-alterno] %s no quiso el cambio a %s; sigue esperando su %s", from,
			sw.ColorAlterno, sw.ColorOriginal)
		return fmt.Sprintf("Perfecto 👍 Sigo buscando un repartidor con tu cilindro %s y te aviso "+
			"apenas lo tenga.", strings.ToLower(sw.ColorOriginal)), true

	case respuesta == normalizarRespuesta(BotonNoCambioColor) || respuestasNegativas[respuesta]:
		a.store.ClearPendingColorSwap(from)
		log.Printf("[color-alterno] %s no quiso el cambio a %s; queda en gestión manual", from, sw.ColorAlterno)
		// El pedido original (color sin conductor) queda registrado para gestión manual,
		// igual que cuando el cliente no quiere esperar.
		if w, ok := a.store.GetPendingWait(from); ok {
			a.avisarSinRepartidor(from, w, "Sin conductor con su color; NO aceptó el equivalente "+
				sw.ColorAlterno+". Quedó en No asignados.")
		}
		a.registrarNoAsignado(from)
		// Cierra tambien la busqueda del backend: si no, seguiria buscando repartidor para un
		// color que el cliente ya rechazo. Ver cerrarEsperaYBusqueda.
		a.cerrarEsperaYBusqueda(from, "rechazo el color alterno")
		a.store.ClearPedidoEnCurso(from)
		return fmt.Sprintf("Entendido 🙏 ¡Gracias por escribirnos! Cuando necesites tu gas %s "+
			"o cualquier otro, aquí estoy 😊", strings.ToLower(sw.ColorOriginal)), true
	}

	// Una pregunta, un matiz ("¿y cuánto cuesta el azul?"): la atiende el modelo.
	return "", false
}
