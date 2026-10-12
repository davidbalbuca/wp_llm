// ANTES DE CANCELAR UN PEDIDO CON EL REPARTIDOR EN CAMINO, SE PREGUNTA.
//
// Edison (12/10): su pedido #442 se demoró (dos repartidores lo soltaron y lo llevó William, el
// camión de respaldo). A las 18:20 escribió "Aún no llega" y a las 18:25 "Disculpe ya no deseo
// nada": el bot canceló EN EL ACTO. William estaba a la vuelta de su casa; un operador tuvo que
// rehacer el pedido a mano tres minutos después. Dueño: "que diga nunca llegó no tiene por qué
// cancelar el pedido".
//
// La cancelación sigue siendo del cliente -- se le respeta --, pero con el dato que le falta: su
// repartidor ya va y puede estar por llegar. Se le pregunta UNA vez, con botones, y solo "Sí,
// cancelar" cancela. Si no hay pedido en camino (búsqueda, ficha a medio armar), no se pregunta
// nada: no hay a quién hacerle perder el viaje.
package agent

import (
	"log"
	"time"
)

const (
	BotonSiCancelar  = "Sí, cancelar"
	BotonNoLoEspero  = "No, lo espero"
	vigenciaPregunta = 15 * time.Minute
)

// preguntarAntesDeCancelar: si el cliente tiene un pedido EN CAMINO y todavía no confirmó, le
// manda el menú y devuelve (guía para el modelo, true). Si ya confirmó, consume la marca y deja
// pasar (false).
func (a *Agent) preguntarAntesDeCancelar(t *turno, from string) (string, bool) {
	if _, ok := a.cancelacionConfirmada.LoadAndDelete(from); ok {
		return "", false
	}
	if id, hay := a.store.GetActivePedido(from); !hay || id <= 0 {
		return "", false
	}
	if v, ok := a.cancelacionPorConfirmar.Load(from); ok && time.Since(v.(time.Time)) < vigenciaPregunta {
		// Ya se le preguntó y no contestó con un botón: no se le repite el menú.
		return "Ya se le preguntó al cliente si de verdad quiere cancelar porque su repartidor va en " +
			"camino. NO canceles ni digas que cancelaste: pídele que toque \"" + BotonSiCancelar +
			"\" o \"" + BotonNoLoEspero + "\".", true
	}
	cuerpo := TextoConfirmarCancelacion(a.store.GetSeguimientoActivo(from))
	if err := a.mandarMenu(from, cuerpo, []string{BotonSiCancelar, BotonNoLoEspero}); err != nil {
		log.Printf("[cancelacion] %s: no salió la pregunta (%v); se cancela como antes", from, err)
		return "", false
	}
	a.cancelacionPorConfirmar.Store(from, time.Now())
	t.menuSent = true
	// Al historial va como la pregunta del turno (lastMenuText): así el modelo sabe qué se preguntó.
	t.lastMenuText = a.preguntaDeCancelacion(from)
	a.store.LogMessage(from, "system", t.lastMenuText)
	log.Printf("[cancelacion] %s pidió cancelar con el repartidor en camino; se le pregunta primero", from)
	return "Se le preguntó al cliente si de verdad quiere cancelar, porque su repartidor ya va en " +
		"camino. NO canceles ni digas que cancelaste: espera su respuesta.", true
}

// preguntaDeCancelacion es la pregunta tal como queda en el historial (cuerpo + botones).
func (a *Agent) preguntaDeCancelacion(from string) string {
	return TextoConfirmarCancelacion(a.store.GetSeguimientoActivo(from)) + " [" + BotonSiCancelar + " / " +
		BotonNoLoEspero + "]"
}

// TextoConfirmarCancelacion: corto, con lo que el cliente no sabe (su repartidor ya va).
func TextoConfirmarCancelacion(enlace string) string {
	texto := "Tu repartidor ya va en camino con tu gas 🚚 y puede estar por llegar."
	if enlace != "" {
		texto += " Míralo en vivo aquí: " + enlace
	}
	return texto + "\n\n¿Igual quieres cancelar tu pedido?"
}

// ResponderConfirmacionCancelacion resuelve el "Sí, cancelar" / "No, lo espero" en código.
func (a *Agent) ResponderConfirmacionCancelacion(from, texto string) (string, bool) {
	v, ok := a.cancelacionPorConfirmar.Load(from)
	if !ok {
		return "", false
	}
	if time.Since(v.(time.Time)) >= vigenciaPregunta {
		a.cancelacionPorConfirmar.Delete(from)
		return "", false
	}
	r := normalizarRespuesta(texto)
	switch {
	case r == normalizarRespuesta(BotonSiCancelar) || respuestasAfirmativas[r] || pideCancelarPedido(texto):
		a.cancelacionPorConfirmar.Delete(from)
		a.cancelacionConfirmada.Store(from, true)
		log.Printf("[cancelacion] %s confirmó: se cancela", from)
		msg, hubo := a.cancelarPedidoVivoSiLoHay(from)
		a.cancelacionConfirmada.Delete(from)
		if !hubo {
			msg = "Tu pedido ya no estaba activo 🙏. Cuando necesites tu gas, aquí estoy 😊"
		}
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, msg)
		return msg, true
	case r == normalizarRespuesta(BotonNoLoEspero) || respuestasNegativas[r]:
		a.cancelacionPorConfirmar.Delete(from)
		msg := "¡Perfecto! 🙌 Tu repartidor sigue en camino; te aviso apenas esté llegando."
		if enlace := a.store.GetSeguimientoActivo(from); enlace != "" {
			msg += "\n\n📍 Míralo en vivo aquí: " + enlace
		}
		log.Printf("[cancelacion] %s decidió esperar a su repartidor", from)
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, msg)
		return msg, true
	}
	return "", false // otra cosa ("¿dónde está?"): la atiende el modelo, la pregunta sigue en pie
}
