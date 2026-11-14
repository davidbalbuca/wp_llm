package agent

// Pedido del bot por ANILLOS (14/11). Ver el comentario BROADCAST en registrarPedido.

import (
	"log"

	"wp-llm-gas/internal/conversation"
)

// mensajeConfirmandoRepartidor: el pedido se les está ofreciendo AHORA a los repartidores
// cercanos y lo lleva el primero que acepte. A diferencia de mensajeBuscandoRepartidor no es que
// no haya nadie: hay, y en segundos uno lo toma. Nombre, placa y tiempo llegan cuando acepte
// (avisarRepartidorAsignado), así que no se le promete nada todavía.
const mensajeConfirmandoRepartidor = "IMPORTANTE: El pedido quedó registrado y se les está " +
	"ofreciendo AHORA a los repartidores más cercanos; el primero que acepte lo lleva. Dile al " +
	"cliente que estamos confirmando con el repartidor más cercano y que en un momento le avisamos " +
	"quién va y en cuánto llega, sin que tenga que hacer nada, y que si prefiere cancelar escriba " +
	"\"cancelar\". NO le des nombre de repartidor, placa ni tiempo de llegada todavía. NO uses " +
	"mostrar_menu. NO derives al dueño ni le pidas de nuevo los datos ni la ubicación."

// esperarBroadcast deja al cliente esperando la búsqueda por anillos que el backend ya abrió:
// la misma espera que cuando no hay repartidor en el acto, pero siguiendo ESA búsqueda en vez de
// abrir otra.
func (a *Agent) esperarBroadcast(t *turno, from string, idBusqueda int, lineas []lineaPedido,
	idtipopago int, identificacion, nombres string) string {
	w := esperaDe(lineas, idtipopago, identificacion, nombres)
	w.IDBusqueda = idBusqueda
	a.store.SetPendingWait(from, w)
	primera := lineas[0]
	t.ultimoPedido = resultadoPedido{
		enEspera: true,
		Producto: primera.Producto.Nombre,
		Color:    primera.Color.Nombre,
		Cantidad: primera.Cantidad,
		Lineas:   lineas,
	}
	log.Printf("[broadcast] %s: búsqueda %d por anillos; se espera a que un repartidor acepte", from, idBusqueda)
	a.startWaitForDriver(from, w)
	return mensajeConfirmandoRepartidor
}

// esperaDe arma la espera de repartidor de un pedido (de un color o de varios).
func esperaDe(lineas []lineaPedido, idtipopago int, identificacion, nombres string) conversation.PendingWait {
	var items []conversation.PendingWaitItem
	if len(lineas) > 1 {
		items = make([]conversation.PendingWaitItem, len(lineas))
		for i, l := range lineas {
			items[i] = conversation.PendingWaitItem{
				IDCategoria: l.Producto.IDCategoria, IDProducto: l.Producto.IDProducto,
				IDColor: l.Color.ID, Cantidad: l.Cantidad,
				ProductoNombre: l.Producto.Nombre, ColorNombre: l.Color.Nombre,
			}
		}
	}
	primera := lineas[0]
	return conversation.PendingWait{
		IDCategoria:    primera.Producto.IDCategoria,
		IDProducto:     primera.Producto.IDProducto,
		IDColor:        primera.Color.ID,
		Cantidad:       primera.Cantidad,
		IDTipoPago:     idtipopago,
		ProductoNombre: primera.Producto.Nombre,
		ColorNombre:    primera.Color.Nombre,
		Identificacion: identificacion,
		Nombres:        nombres,
		Items:          items,
	}
}
