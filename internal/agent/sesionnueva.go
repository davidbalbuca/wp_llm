// LA CONVERSACIÓN ARRANCA LIMPIA CUANDO NO HAY NADA PENDIENTE.
//
// Pedido del dueño (18/09): "lo de poner clear al iniciar las conversaciones".
//
// Ya existía el cierre por ESTADO al terminar (cierreciclo.go): cancelar, entregar+calificar, y
// negar las políticas de datos. Pero un pedido puede morir sin pasar por ninguno de esos finales:
//
//   - el cliente eligió esperar repartidor, no llegó ninguno y se fue;
//   - preguntó el precio y desapareció;
//   - le entregaron el gas y nunca calificó (lo más común de todo).
//
// En esos casos el rastro vivía las 24 h de la ventana de WhatsApp, y el siguiente "hola" —que
// puede ser al día siguiente— arrastraba el color, la cantidad y la dirección del pedido anterior.
// El modelo mezclaba los dos y le confirmaba al cliente cosas que no había pedido hoy.
//
// POR QUÉ POR ESTADO Y NO POR RELOJ. Se consideraron tres opciones:
//
//	A. Bajar SessionGap (2-4 h en vez de 24)  → le borra el contexto al cliente que se calla
//	   media hora a mitad del pedido, que es normalísimo mientras busca su cédula o consulta
//	   con alguien de la casa.
//	B. Limpiar al detectar un saludo            → "hola" también aparece a mitad de una
//	   conversación, y el texto no es una señal fiable de nada (misma razón por la que los
//	   candados de este proyecto deciden por estado).
//	C. Limpiar cuando NO HAY NADA EN CURSO      → ✅ esta.
//
// C no puede equivocarse con el cliente que está a mitad de algo, porque justamente pregunta si
// está a mitad de algo. Si tiene un pedido vivo, una espera de repartidor, un menú sin responder o
// una entrega agendada, no se toca nada.
//
// SessionGap (24 h) SE QUEDA como red de seguridad para lo que esto no cubra — es una decisión de
// David (agosto) y esto no la reemplaza, la complementa.
package agent

import (
	"log"
	"time"
)

// VentanaConversacionSeguida es el silencio a partir del cual el próximo mensaje se considera una
// conversación nueva, SIEMPRE QUE no haya nada pendiente.
//
// Media hora es deliberadamente holgado: no es el tiempo tras el cual la conversación caduca (de
// eso se encarga SessionGap), sino el mínimo para estar seguros de que el cliente no está en
// medio de una frase. Alguien que escribe "hola" treinta minutos después de su último mensaje,
// sin ningún pedido abierto, viene a empezar algo nuevo.
const VentanaConversacionSeguida = 30 * time.Minute

// EmpezarConversacionSiCorresponde limpia el rastro de la conversación anterior cuando el cliente
// vuelve a escribir y no tiene NADA pendiente. Devuelve true si limpió.
//
// Se llama al recibir cada mensaje, antes de atenderlo.
func (a *Agent) EmpezarConversacionSiCorresponde(from string) bool {
	// Si escribió hace un momento, es la misma conversación: no hay nada que decidir.
	ultima, hay := a.store.LastActivity(from)
	if !hay || time.Since(ultima) < VentanaConversacionSeguida {
		return false
	}
	if motivo, ocupado := a.tienePendiente(from); ocupado {
		log.Printf("[sesion-nueva] %s volvió tras %s pero tiene %s; NO se limpia",
			from, time.Since(ultima).Round(time.Minute), motivo)
		return false
	}
	// Nada en curso y silencio largo: empieza de cero. Se reutiliza el mismo cierre de siempre,
	// que ya sabe qué borrar y qué conservar (cuenta, perfil y direcciones NO se tocan).
	a.cerrarCicloDeConversacion(from,
		"volvió tras "+time.Since(ultima).Round(time.Minute).String()+" sin nada pendiente")
	return true
}

// tienePendiente dice si el cliente está a mitad de algo, y qué. Es el corazón de la decisión: si
// esta lista se queda corta, se le borra el contexto a alguien que está pidiendo gas.
//
// Ante la duda, se devuelve OCUPADO. Arrastrar un poco de contexto de más es molesto; borrarle el
// pedido a quien está a mitad de hacerlo le cuesta la venta al negocio.
func (a *Agent) tienePendiente(from string) (string, bool) {
	if id, hay := a.store.GetActivePedido(from); hay && id > 0 {
		return "un pedido en camino", true
	}
	if _, hay := a.store.GetPendingWait(from); hay {
		return "una espera de repartidor", true
	}
	if _, hay := a.store.GetPedidoEnCurso(from); hay {
		return "un pedido a medio armar", true
	}
	if _, hay := a.store.GetPedidoEsperandoDireccion(from); hay {
		return "un pedido esperando que confirme la dirección", true
	}
	if _, hay := a.store.GetOrderDraft(from); hay {
		return "un pedido en pausa por verificación", true
	}
	if _, hay := a.store.GetPendingVerification(from); hay {
		return "una verificación pendiente", true
	}
	if _, hay := a.store.GetPendingRating(from); hay {
		return "una calificación pendiente", true
	}
	if _, hay := a.store.GetPendingColorSwap(from); hay {
		return "una oferta de color sin responder", true
	}
	if _, hay := a.store.GetPendingGuardarUbicacion(from); hay {
		return "una oferta de guardar su ubicación", true
	}
	if _, hay := a.store.GetConfirmingSchedule(from); hay {
		return "una entrega agendada por confirmar", true
	}
	if a.store.TieneProgramacionViva(from) {
		return "una entrega agendada", true
	}
	if a.store.ConsentimientoPendiente(from) {
		return "el consentimiento de datos sin responder", true
	}
	if a.store.EligiendoHora(from) {
		return "el menú de horas sin responder", true
	}
	return "", false
}
