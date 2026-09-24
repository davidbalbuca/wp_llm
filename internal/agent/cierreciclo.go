// CIERRE DE CICLO: dejar la conversación como nueva cuando el pedido terminó.
//
// Hasta ahora la memoria del bot duraba la ventana completa de 24 h de WhatsApp y NINGÚN evento
// la borraba — solo el paso del tiempo (conversation.SessionGap). Esa regla sirve DENTRO de un
// pedido: si el cliente se calla diez minutos a mitad del flujo, no queremos perder su color, su
// cantidad ni su dirección.
//
// Pero cuando el ciclo se cierra de verdad —el pedido se canceló, o se entregó y el cliente ya
// calificó— esa misma memoria estorba: el siguiente "hola" arrastra el color, la cantidad y la
// dirección del pedido anterior, y el modelo mezcla los dos. Es el "empieza de nuevo" que pidió
// el dueño, y el equivalente a un /clear pero disparado por el ESTADO del negocio, no por un
// comando ni por un reloj.
//
// Lo que NO se borra: la cuenta del cliente (credenciales georoutes), su perfil (cédula, nombre)
// y sus direcciones guardadas. Eso es lo que hace que un cliente conocido no tenga que repetir
// sus datos, y no tiene nada que ver con el pedido que acaba de terminar. Tampoco se toca el
// registro de auditoría (LogMessage), que el panel necesita íntegro.
package agent

import (
	"log"
)

// cerrarCicloDeConversacion deja al cliente como recién llegado: sin historial de chat, sin
// ficha de pedido a medias y sin banderas de flujo. Se llama cuando el ciclo terminó.
//
// motivo es para el log: sirve para distinguir en producción si la conversación se reinició por
// una cancelación o por una entrega calificada.
func (a *Agent) cerrarCicloDeConversacion(from, motivo string) {
	log.Printf("[cierre-ciclo] %s: %s; la próxima conversación arranca limpia", from, motivo)

	// El historial del modelo: es lo que hace que mezcle el pedido viejo con el nuevo.
	a.store.ClearHistory(from)

	// La ficha del pedido a medias (color, cantidad, líneas). Si quedó algo suelto, no puede
	// reaparecer en el pedido siguiente.
	a.store.ClearPedidoEnCurso(from)

	// Banderas de flujo que solo tienen sentido dentro del pedido que acaba de cerrarse.
	a.cerrarEsperaYBusqueda(from, "se cerro el ciclo de la conversacion") // espera + busqueda del backend
	a.store.ClearOrderDraft(from)                                         // pedido en pausa por OTP
	a.store.ClearPendingGuardarUbicacion(from)                            // "¿la guardo como Casa?"
	a.store.ClearEligiendoHora(from)                                      // menú de horas de una programación que ya no toca
	a.store.LimpiarFueraDeCobertura(from)                                 // un rechazo de zona no sigue vigente

	// La espera del menú de protección de datos SIN RESPONDER. Se limpia porque mientras está
	// viva la compuerta bloquea los pedidos de ese cliente: arrastrarla al ciclo siguiente lo
	// dejaría sin poder comprar por un menú que ya no está en pantalla.
	//
	// OJO CON LA DIFERENCIA: esto borra la ESPERA, no la RESPUESTA. El consentimiento que el
	// cliente llegó a dar (o a negar) vive en su propia tabla y sobrevive a propósito, para no
	// volver a preguntarle lo que ya contestó.
	a.store.ClearConsentimientoPendiente(from)

	// NO se tocan: Account, Profile ni las direcciones guardadas. Un cliente conocido tiene que
	// seguir siéndolo en su próximo pedido: volver a pedirle la cédula sería un retroceso.
}
