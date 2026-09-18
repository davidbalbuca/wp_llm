// PROGRAMAR LA ENTREGA TOCANDO UNA HORA, no escribiéndola.
//
// Pedido del dueño (18/09): sugerir opciones para que el cliente solo dé click, en vez de hacerle
// escribir.
//
// Era el último punto grande donde había que escribir, y el que peor lo llevaba: la gente pone
// "6h30", "18:30 pm", "a las siete", y cada formato es una oportunidad de no entenderse. El 05/09
// una clienta dijo "6h30" y luego "18:30 pm"; el bot le pidió confirmar la hora TRES veces y la
// programación no llegó a crearse.
//
// Dos piezas:
//
//  1. OfrecerHorasParaProgramar — cuando el cliente elige "Programar" en el menú de espera, se le
//     mandan las próximas horas disponibles como botones (calculadas en código, ver horasmenu.go).
//  2. ResponderHoraProgramada — resuelve el botón que tocó, sin pasar por el modelo.
//
// Lo que NO cambia: el candado duro de programar_entrega (la hora tiene que venir del cliente) y
// todas sus validaciones. Aquí la hora viene de un botón que el cliente PULSÓ, que es la forma más
// inequívoca que existe de que la haya dicho él.
//
// Si el cliente escribe en vez de tocar, funciona como siempre: el parser tolerante sigue ahí y el
// turno se va al modelo.
package agent

import (
	"log"
	"strings"
	"time"
)

// OfrecerHorasParaProgramar le manda al cliente las horas disponibles como botones y deja
// anotado que está eligiendo hora. Devuelve el texto para el cliente y si se hizo cargo.
//
// Si no hay horas que ofrecer (configuración rara, o no cabe ninguna ni hoy ni mañana), devuelve
// false y el turno sigue al modelo, que le pedirá la hora por escrito como antes.
func (a *Agent) OfrecerHorasParaProgramar(from string) (string, bool) {
	opciones := a.OpcionesDeHora(time.Now().In(zonaEcuador))
	if len(opciones) == 0 {
		log.Printf("[programar-menu] %s: no hay horas que sugerir; que se la pida el modelo", from)
		return "", false
	}
	cuerpo := "¡Claro! 😊 ¿A qué hora te viene bien que te lo llevemos?"
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[programar-menu] %s: el menú de horas falló (%v); lo atiende el modelo", from, err)
		return "", false
	}
	a.store.SetEligiendoHora(from)
	// El menú al historial, para que el modelo sepa qué se preguntó si el cliente escribe algo
	// distinto y el turno acaba llegándole.
	a.store.AppendUser(from, "Programar")
	a.store.AppendModel(from, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
	return "", true // el menú YA salió: el llamador no manda texto además
}

// ResponderHoraProgramada resuelve la hora que el cliente tocó y agenda la entrega, sin pasar por
// el modelo. Solo actúa si se le mandó el menú de horas y respondió con uno de sus botones.
func (a *Agent) ResponderHoraProgramada(from, texto string) (string, bool) {
	if !a.store.EligiendoHora(from) {
		return "", false
	}
	// "Otra hora": el cliente quiere una que no estaba. Se sale del menú y se le pide escrita,
	// que es exactamente el flujo de siempre. La salida existe para que el menú no sea una jaula.
	if normalizarRespuesta(texto) == normalizarRespuesta(BotonOtraHora) {
		a.store.ClearEligiendoHora(from)
		log.Printf("[programar-menu] %s pidió otra hora; se le pide escrita", from)
		respuesta := "¡Sin problema! Atendemos de " + a.cfg.BotHorarioInicio + " a " +
			a.cfg.BotHorarioFin + " ⏰ Dime a qué hora te viene bien y te la agendo 😊"
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, respuesta)
		return respuesta, true
	}

	hora, esManana, ok := horaDeBoton(texto)
	if !ok {
		// Escribió otra cosa (una pregunta, una hora suelta): NO se interpreta aquí. La espera
		// sigue en pie y lo atiende el modelo, que sabe leer "6h30" y llamará a la herramienta.
		return "", false
	}
	a.store.ClearEligiendoHora(from)
	log.Printf("[programar-menu] %s eligió %s (mañana=%v); se agenda en código", from, hora, esManana)

	// Se entra por runTool, igual que el resto de los interceptores: es quien crea el ticket si
	// falla. La hora va como la eligió el cliente; las validaciones de programarEntrega (horario,
	// 24 h, hora pasada) siguen mandando.
	args := map[string]any{"hora": hora}
	if esManana {
		args["dia"] = "mañana"
	}
	if w, hay := a.store.GetPendingWait(from); hay {
		// Lo que ya se sabe del pedido, para no volver a preguntárselo.
		args["cantidad"] = w.Cantidad
		args["color"] = w.ColorNombre
		if w.Identificacion != "" {
			args["identificacion"] = w.Identificacion
		}
		if w.Nombres != "" {
			args["nombres"] = w.Nombres
		}
	}
	t := &turno{}
	a.store.AppendUser(from, texto)
	salida := a.runTool(t, from, "programar_entrega", args)

	// La verdad es el ESTADO, no el texto de la herramienta (misma regla que cancelar).
	if !a.store.TieneProgramacionViva(from) {
		log.Printf("[programar-menu] %s: la programación NO quedó viva; lo atiende el modelo. Detalle: %s",
			from, salida)
		// No se le responde nada inventado: el modelo tiene el resultado en el historial y sabrá
		// explicarle el motivo exacto (hora fuera de horario, ya pasó, etc.).
		return "", false
	}
	cuando := "hoy"
	if esManana {
		cuando = "mañana"
	}
	respuesta := "¡Listo! 📅 Tu entrega quedó agendada para " + cuando + " a las " + hora +
		". Te escribo a esa hora para confirmarte antes de enviarla 😊"
	a.store.AppendModel(from, respuesta)
	return respuesta, true
}
