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

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/whatsapp"
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
	if err := whatsapp.SendMenu(a.cfg, from, cuerpo, opciones); err != nil {
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
		// Por runTool, como todos los interceptores: es quien crea el ticket si falla.
		t := &turno{}
		a.runTool(t, from, "registrar_pedido", map[string]any{
			"color": sw.ColorAlterno, "cantidad": sw.Cantidad,
		})
		if t.menuSent {
			return "", true // registrarPedido mandó otro menú (p. ej. confirmar dirección)
		}
		return a.mensajeDelPedido(t, from), true

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
		a.store.ClearPendingWait(from)
		a.store.ClearPedidoEnCurso(from)
		return fmt.Sprintf("Entendido 🙏 ¡Gracias por escribirnos! Cuando necesites tu gas %s "+
			"o cualquier otro, aquí estoy 😊", strings.ToLower(sw.ColorOriginal)), true
	}

	// Una pregunta, un matiz ("¿y cuánto cuesta el azul?"): la atiende el modelo.
	return "", false
}
