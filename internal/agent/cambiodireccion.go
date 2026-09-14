// Cambio de ubicación con pedido ya en ruta: transparente y en código.
//
// El caso que esto tapa: 12/09, 593959499118. El cliente tenía el pedido #241
// en camino y mandó una nueva ubicación (link corto + referencia "Santa Ana").
// El bot respondió "ya le avisé al repartidor" dos veces sin haber hecho nada:
// no existe herramienta para mover un pedido, así que el modelo improvisó. El
// repartidor salió a la dirección vieja y el pedido terminó fallido.
//
// La regla de negocio del dueño: si el cliente cambia la dirección con un
// pedido vivo, se cancela el anterior y se genera uno nuevo con la misma
// carga a la nueva ubicación. El cliente lo ve explícito: "cancelé el
// anterior y generé uno nuevo". Nada de "ya avisé al repartidor".
//
// Por qué va en código y no en el prompt: no hay tool que el modelo pueda
// llamar para mover un pedido; sin código, solo puede prometer y mentir.
// El paracaídas afirmaAvisoAlEquipo no cubría "le pasé al repartidor" y, aun
// si cubriera, no sabría qué hacer: no es una afirmación fantasma, es una
// ausencia de herramienta.
//
// Patrón tomado de los interceptores que sí funcionan (cancelacion.go,
// confirmacion.go, direccion.go):
//   - Se dispara ANTES del modelo, por estado durable (GetActivePedido),
//     no por el texto del modelo.
//   - Crea su propio t := &turno{} y entra por a.runTool, nunca directo.
//   - Decide por ESTADO (¿siguió vivo el pedido?) no por el string devuelto.
//   - Escribe AppendUser/AppendModel él mismo.
package agent

import (
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/notify"
)

// lineasDelPedidoVivo devuelve las líneas (color+cantidad) del pedido que el
// cliente tiene en curso. Fuente: LastOrder si ya está confirmado, o
// PendingWait si aún buscaba conductor. No se lee PedidoEnCurso: esa es la
// ficha del pedido NUEVO, no del vivo.
func (a *Agent) lineasDelPedidoVivo(from string) ([]conversation.ItemPedido, bool) {
	// Caso 1: pedido confirmado (LastOrder). Es lo normal: el cliente ya vio
	// "tu repartidor es X, placa Y" y luego manda otra ubicación.
	if last, ok := a.store.GetLastOrder(from); ok && last.Cantidad > 0 && last.Color != "" {
		lineas := last.ItemsDelPedido()
		if len(lineas) > 0 {
			return lineas, true
		}
	}
	// Caso 2: pedido en espera de conductor (PendingWait). Aún no hay
	// LastOrder, pero sí un PendingWait con las líneas ya resueltas.
	if w, ok := a.store.GetPendingWait(from); ok && w.IDProducto > 0 {
		items := w.Lineas()
		if len(items) > 0 {
			lineas := make([]conversation.ItemPedido, len(items))
			for i, it := range items {
				lineas[i] = conversation.ItemPedido{Color: it.ColorNombre, Cantidad: it.Cantidad}
			}
			return lineas, true
		}
	}
	return nil, false
}

// CambiarDireccionPorUbicacion resuelve, EN CÓDIGO, que el cliente comparta
// una NUEVA ubicación cuando ya tiene un pedido vivo. Cancela el anterior y
// re-registra con las mismas líneas en la nueva dirección.
//
// Se llama desde cmd/bot justo después de guardar la nueva ubicación y pasar
// el chequeo de cobertura (fueraDeCobertura). Si no hay pedido vivo, no hace
// nada y el mensaje sigue su curso normal (pedido nuevo).
//
// Devuelve (respuesta, true) si se hizo cargo del turno.
func (a *Agent) CambiarDireccionPorUbicacion(from string, lat, lng float64) (string, bool) {
	// Solo si hay pedido vivo (activo en backend o en espera de conductor).
	if !a.tienePedidoVivo(from) {
		return "", false
	}

	lineas, ok := a.lineasDelPedidoVivo(from)
	if !ok || len(lineas) == 0 {
		// Sin líneas no hay qué re-registrar: el pedido vivo no se toca. El
		// chequeo de cobertura previo ya pasó, así que la ubicación queda
		// guardada para el próximo intento, y el modelo explica la situación.
		log.Printf("[cambio-dir] %s tiene pedido vivo pero sin líneas; no se mueve", from)
		return "", false
	}

	// La cobertura de la NUEVA ubicación YA se verificó en el webhook antes de
	// llegar aquí (fueraDeCobertura). Si estábamos fuera, el webhook ya
	// respondió y nunca llama a esto. Doble chequeo por si se llama directo.
	if res, err := a.gr.CheckCoverage(lat, lng, from); err == nil && !res.Cubierto {
		log.Printf("[cambio-dir] %s: nueva ubicación fuera de cobertura; no se cancela el anterior", from)
		msg := "Revisé tu nueva ubicación y por ahora no llegamos a esa zona 😔. " +
			"Tu pedido anterior sigue en camino a la dirección original. Si tienes otra dirección dentro de nuestra zona, compártemela 📍"
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		a.store.AppendModel(from, msg)
		return msg, true
	}

	pedidoVivo, _ := a.store.GetActivePedido(from)
	log.Printf("[cambio-dir] %s tiene pedido #%d vivo; nueva ubicación %.6f,%.6f — se cancela y re-registra (%s)",
		from, pedidoVivo, lat, lng, describeItems(lineas))

	// La ubicación ya está guardada por el webhook (SetLocation) antes de
	// llamar aquí. Solo falta mover el pedido.

	// --- Paso 1: cancelar el anterior ---
	t1 := &turno{}
	salidaCancel := a.runTool(t1, from, "cancelar_pedido", map[string]any{})

	if _, sigue := a.store.GetActivePedido(from); sigue {
		// No se pudo cancelar el viejo: NO se intenta crear el nuevo. Dos
		// pedidos vivos serían peor que dejar uno en su dirección original.
		log.Printf("[cambio-dir] %s: no se pudo cancelar el pedido #%d; no se re-registra", from, pedidoVivo)
		a.crearTicketSoporte(from, "Cambio de dirección — no se pudo cancelar el pedido anterior",
			fmt.Sprintf("El cliente compartió una nueva ubicación (%.6f,%.6f) con el pedido #%d en ruta. "+
				"El intento de cancelar el anterior falló. El pedido SIGUE en su dirección original; hay que moverlo a mano. Detalle: %s",
				lat, lng, pedidoVivo, salidaCancel))
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		msg := "Tuve un problema al mover tu pedido a la nueva dirección 🙏. Ya avisé al equipo para que lo reubiquen enseguida. " +
			"Tu pedido sigue en camino a la dirección original mientras lo gestionamos."
		a.store.AppendModel(from, msg)
		return msg, true
	}
	// Si además había espera, limpiarla: ya no aplica.
	a.store.ClearPendingWait(from)

	// --- Paso 2: re-registrar con las mismas líneas en la nueva ubicación ---
	// La ubicación NUEVA ya está en el store (la guardó el webhook), así que
	// registrarPedido la toma sola. Solo hay que pasar las líneas.
	t2 := &turno{}
	salidaReg := a.runTool(t2, from, "registrar_pedido", argsDeLineas(lineas, nil))

	// t2.menuSent: si registrarPedido mandó un menú (confirmar dirección), ese
	// menú ya salió al cliente: no lo pisamos.
	if t2.menuSent {
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		return "", true
	}

	switch {
	case t2.ultimoPedido.ok:
		log.Printf("[cambio-dir] %s: pedido re-registrado #%d (antes #%d)", from, t2.ultimoPedido.IDPedido, pedidoVivo)
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		detalle := describeLineas(t2.ultimoPedido.Lineas)
		if detalle == "" {
			detalle = describeItems(lineas)
		}
		msg := fmt.Sprintf("¡Listo! Cancelé tu pedido anterior (#%d) y generé uno nuevo con %s a tu nueva dirección 🎉\n", pedidoVivo, detalle)
		if t2.ultimoPedido.Conductor != "" {
			msg += fmt.Sprintf("Tu nuevo repartidor es %s", t2.ultimoPedido.Conductor)
			if t2.ultimoPedido.Placa != "" {
				msg += fmt.Sprintf(" (placa %s)", t2.ultimoPedido.Placa)
			}
			msg += ". "
		}
		if t2.ultimoPedido.TotalPagar > 0 {
			msg += fmt.Sprintf("Valor a pagar: $%.2f. ", t2.ultimoPedido.TotalPagar)
		}
		if t2.ultimoPedido.Seguimiento != "" {
			msg += fmt.Sprintf("\n📍 Sigue a tu repartidor en vivo aquí:\n%s", t2.ultimoPedido.Seguimiento)
		}
		a.store.AppendModel(from, msg)
		return msg, true

	case t2.ultimoPedido.enEspera:
		log.Printf("[cambio-dir] %s: nuevo pedido en espera (sin conductor aún)", from)
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		msg := fmt.Sprintf("Cancelé tu pedido anterior (#%d) y generé uno nuevo con %s a tu nueva dirección 🙌. "+
			"En este momento los repartidores están un poco lejos, así que estoy buscando uno para ti 🚚. "+
			"En menos de 5 minutos te confirmo. No tienes que hacer nada.", pedidoVivo, describeItems(lineas))
		a.store.AppendModel(from, msg)
		return msg, true

	default:
		// Se canceló el viejo pero el nuevo no salió (cobertura, catálogo, sin
		// conductor alternativo, backend caído). El cliente se quedó SIN pedido:
		// hay que avisar al equipo con todo el contexto.
		log.Printf("[cambio-dir] %s: se canceló #%d pero el re-registro falló", from, pedidoVivo)
		notify.ReportarFallo(a.cfg, a.store, from, "Cambio de dirección — pedido cancelado pero no se pudo re-registrar",
			fmt.Sprintf("Se canceló el pedido #%d (%s) para moverlo a %.6f,%.6f, pero el nuevo no se pudo crear. "+
				"El cliente se quedó SIN pedido activo. Detalle: %s",
				pedidoVivo, describeItems(lineas), lat, lng, strings.TrimSpace(salidaReg)))
		a.store.AppendUser(from, fmt.Sprintf("📍 ubicación: %.6f, %.6f", lat, lng))
		msg := "Cancelé tu pedido anterior, pero tuve un problema al generarlo en la nueva dirección 🙏. " +
			"Ya avisé al equipo para que lo reubiquen enseguida. Te contactarán en breve."
		a.store.AppendModel(from, msg)
		return msg, true
	}
}
