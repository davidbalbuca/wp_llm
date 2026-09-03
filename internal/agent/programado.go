// Entregas PROGRAMADAS: agendar una entrega para más tarde (fuera de horario o a pedido del
// cliente), cancelarla, y las reglas de horario laboral. La confirmación a la hora pactada la
// maneja confirmacion.go; aquí vive el agendado y sus candados de hora.
package agent

import (
	"fmt"
	"log"
	"strings"
	"time"
	"wp-llm-gas/internal/conversation"
)

// registrarPedido ejecuta la secuencia real de georoutes: mapea el color a IDs de
// producto, asegura la cuenta del cliente, hace login, registra la dirección desde la
// ubicación de WhatsApp y crea el pedido. Devuelve un texto para que el modelo responda.
// programarEntrega agenda una entrega para una hora dentro del horario laboral y de la ventana
// de 24h de WhatsApp. El scheduler (main.go) le escribirá al cliente a esa hora para confirmar
// y ahí se registra el pedido real (registrar_pedido).
func (a *Agent) programarEntrega(from string, args map[string]any) string {
	// REGLA DURA, espejo de la de registrarPedido: DENTRO del horario no se programa salvo que
	// el cliente pida otra hora explicitamente (viene 'hora' en los argumentos). Sin esto, el
	// modelo programaba entregas para el dia siguiente estando el servicio activo.
	// CANDADO DURO: si el cliente esta CONFIRMANDO una entrega que ya tenia agendada, jamas se
	// vuelve a programar. Paso en produccion el 27/08 con una clienta real: confirmo a las 06:03
	// la entrega del dia anterior, no habia conductor, y el bot la reprogramo para el dia
	// siguiente. Ya habia esperado un dia entero; mandarla a esperar otro es el peor final
	// posible. Si no hay repartidor, se le ofrece ESPERAR o CANCELAR, nunca reagendar.
	if _, confirmando := a.store.GetConfirmingSchedule(from); confirmando {
		return "El cliente está CONFIRMANDO una entrega que ya tenía agendada: NO se puede volver " +
			"a programar, ya esperó su turno. Usa registrar_pedido. Si no hay repartidor disponible, " +
			"ofrécele ESPERAR o CANCELAR, pero NUNCA reagendar para otro día."
	}

	// Excepcion: si el pedido quedo en espera por FALTA DE CONDUCTORES, programar SI es valido
	// aunque estemos dentro del horario (lo pidio el jefe el 26/08): se puede reagendar para mas
	// tarde el mismo dia o para el dia siguiente, dentro del horario y de las 24 h.
	_, esperandoConductor := a.store.GetPendingWait(from)
	if a.dentroDeHorario(time.Now().In(zonaEcuador)) && strings.TrimSpace(str(args["hora"])) == "" &&
		!esperandoConductor {
		return "ESTAMOS DENTRO DEL HORARIO (" + a.cfg.BotHorarioInicio + " a " + a.cfg.BotHorarioFin + "): " +
			"no se programa nada porque el servicio está ACTIVO ahora. Usa registrar_pedido para tomar el " +
			"pedido de una vez. Solo programa si el cliente pidió EXPRESAMENTE otra hora."
	}
	cantidad := toInt(args["cantidad"])
	if cantidad <= 0 {
		return "Falta una cantidad válida de cilindros. Pregúntale al cliente cuántos desea."
	}
	loc, hasLoc := a.store.GetLocation(from)
	if !hasLoc {
		return "Aún no tengo la ubicación del cliente y es obligatoria para programar. Pídele que comparta su " +
			"ubicación de WhatsApp 📎."
	}

	identificacion := strings.TrimSpace(str(args["identificacion"]))
	nombres := strings.TrimSpace(str(args["nombres"]))
	if perfil, ok := a.store.GetProfile(from); ok {
		if identificacion == "" {
			identificacion = perfil.Identificacion
		}
		if nombres == "" {
			nombres = perfil.Nombres
		}
	}
	if identificacion == "" || nombres == "" {
		return "Para programar necesito la cédula y el nombre completo del cliente. Pídeselos."
	}

	// CANDADO DURO: la hora tiene que haberla dicho EL CLIENTE, no el modelo. El prompt ya pedía
	// "deja que el cliente escriba la hora", y aun así el 02/09 un cliente compartió su ubicación
	// a las 22:49 y el bot le agendó una entrega para las 06:00 que él nunca pidió: como era
	// cliente conocido, la cédula y el nombre salían del perfil, la cantidad y el color ya
	// estaban del menú, y la hora se la inventó el modelo. Todas las validaciones pasaban.
	// Al día siguiente le llegó la confirmación de una entrega fantasma y terminó escribiendo
	// "deberías arreglar esto porque generas confusión en las personas".
	//
	// Programar es un COMPROMISO: si el cliente no escribió la hora, no hay compromiso que honrar.
	if !a.clienteMencionoHora(from, str(args["hora"])) {
		return fmt.Sprintf("El cliente NO ha escrito a qué hora quiere su entrega, y sin eso no se "+
			"programa nada (no inventes una hora tú). Pregúntale primero si desea PROGRAMAR la entrega; "+
			"si acepta, dile que atendemos de %s a %s y deja que él escriba la hora (formato HH:MM).",
			a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	}

	mins := parseHoraHHMM(str(args["hora"]))
	if mins < 0 {
		return fmt.Sprintf("Aún no tengo una hora válida. Dile que atendemos de %s a %s y pídele que escriba "+
			"a qué hora quiere recibirlo (formato HH:MM). NO le ofrezcas horas como opciones.",
			a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	}
	ini := parseHoraHHMM(a.cfg.BotHorarioInicio)
	fin := parseHoraHHMM(a.cfg.BotHorarioFin)
	if mins < ini || mins >= fin {
		return fmt.Sprintf("Esa hora está fuera del horario de entregas (%s a %s). Pídele al cliente una hora "+
			"dentro del horario.", a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	}

	ahora := time.Now().In(zonaEcuador)
	target := time.Date(ahora.Year(), ahora.Month(), ahora.Day(), mins/60, mins%60, 0, 0, zonaEcuador)
	dia := strings.ToLower(strings.TrimSpace(str(args["dia"])))
	if dia == "manana" || dia == "mañana" {
		target = target.Add(24 * time.Hour)
	} else if !target.After(ahora) {
		// Esa hora YA PASO hoy. Antes se saltaba a mañana en silencio, y asi un cliente que
		// confirmaba a las 06:03 su entrega de las 06:00 terminaba reagendado para el dia
		// siguiente sin haberlo pedido. Ahora se le pregunta: el salto de dia lo decide el
		// cliente, no el codigo.
		return fmt.Sprintf("Las %s de HOY ya pasaron (son las %s). Preguntale al cliente si "+
			"quiere para mas tarde HOY (dile otra hora dentro del horario) o para MAÑANA a esa "+
			"misma hora. No decidas tu: que lo diga el.",
			target.Format("15:04"), ahora.Format("15:04"))
	}
	if target.Sub(ahora) > 24*time.Hour {
		return "Solo puedo programar entregas dentro de las PRÓXIMAS 24 HORAS. Dile eso al cliente con amabilidad " +
			"y pídele una hora más cercana."
	}

	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		return "No puedo consultar el catálogo en este momento. Pídele al cliente que intente más tarde."
	}
	colorNombre := strings.TrimSpace(str(args["color"]))
	producto, color, ok := findProductByColor(contexto.Products, colorNombre)
	if !ok {
		return fmt.Sprintf("El color/marca \"%s\" no está disponible. Colores disponibles: %s. "+
			"Pregúntale al cliente cuál desea.", colorNombre, availableColors(contexto.Products))
	}
	idtipopago, ok := defaultPaymentID(contexto.Payments)
	if !ok {
		return "No hay una forma de pago configurada en el sistema. Pídele al cliente que intente más tarde."
	}

	a.store.SetProfile(from, conversation.Profile{Identificacion: identificacion, Nombres: nombres})

	// El cliente se registra AL AGENDAR, no solo al confirmar. Antes sus datos vivian unicamente
	// en la base del bot hasta que confirmara: si algo fallaba en el camino (paso el 27/08 con
	// Ana Veronica Coronel), la persona habia dado cedula, nombre y ubicacion y NO figuraba en
	// ningun lado del sistema. Quien agenda ya es un cliente real aunque su entrega sea mañana.
	// WppGetOrCreateClient es get-or-create: si ya existe no lo duplica.
	if _, yaTiene := a.store.GetAccount(from); !yaTiene {
		if cuenta, err := a.gr.WppGetOrCreateClient(identificacion, nombres, from); err == nil && cuenta != nil {
			a.store.SetAccount(from, conversation.Account{Username: cuenta.Username, Password: cuenta.Password})
		} else if err != nil {
			// No se aborta la programacion por esto: la entrega igual queda agendada y al
			// confirmar se reintenta. Solo se deja constancia.
			log.Printf("[schedule] no se pudo registrar al cliente %s al agendar: %v", from, err)
		}
	}

	id := a.store.CreateScheduled(conversation.ScheduledOrder{
		Phone:          from,
		Identificacion: identificacion,
		Nombres:        nombres,
		IDCategoria:    producto.IDCategoria,
		IDProducto:     producto.IDProducto,
		IDColor:        color.ID,
		Cantidad:       cantidad,
		IDTipoPago:     idtipopago,
		ProductoNombre: producto.Nombre,
		ColorNombre:    color.Nombre,
		Latitude:       loc.Latitude,
		Longitude:      loc.Longitude,
		HoraPropuesta:  target.Unix(),
	})
	if id <= 0 {
		return "No se pudo guardar la programación. Discúlpate y pídele al cliente intentar de nuevo."
	}
	a.store.LogMessage(from, "system", fmt.Sprintf("📅 Entrega programada #%d: %d x %s %s para el %s",
		id, cantidad, producto.Nombre, color.Nombre, target.Format("02/01 a las 15:04")))
	// Si venia de una espera por falta de conductor, se cierra: el pedido ya quedo agendado y no
	// debe registrarse ademas como NO ASIGNADO cuando venza la espera.
	if esperandoConductor {
		a.store.ClearPendingWait(from)
	}

	etiquetaDia := "hoy"
	if target.Day() != ahora.Day() {
		etiquetaDia = "mañana"
	}
	return fmt.Sprintf("Entrega PROGRAMADA con éxito para %s a las %s. Confírmale al cliente que le escribiremos "+
		"a esa hora para confirmar y enviar su pedido de %d x %s color %s. Recuérdale estar atento al chat.",
		etiquetaDia, target.Format("15:04"), cantidad, producto.Nombre, color.Nombre)
}

// cancelarProgramacion borra la entrega agendada del cliente. Es la contraparte de
// programarEntrega: sin ella el modelo decia que cancelaba y la entrega seguia en pie.
func (a *Agent) cancelarProgramacion(from string) string {
	n := a.store.CancelScheduled(from)
	if n == 0 {
		return "El cliente no tiene ninguna entrega programada pendiente. Aclaraselo con amabilidad " +
			"y preguntale si quiere hacer un pedido ahora."
	}
	a.store.LogMessage(from, "system", "🗑️ Entrega programada cancelada por el cliente")
	return "Entrega programada CANCELADA. Confirmaselo al cliente y ofrecele hacer un pedido cuando lo necesite."
}

// dentroDeHorario indica si `t` cae dentro del horario laboral de entregas configurado.
func (a *Agent) dentroDeHorario(t time.Time) bool {
	ini := parseHoraHHMM(a.cfg.BotHorarioInicio)
	fin := parseHoraHHMM(a.cfg.BotHorarioFin)
	if ini < 0 || fin < 0 {
		return true // configuración inválida: no bloquear el servicio
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= ini && mins < fin
}

// clienteMencionoHora comprueba que la hora que el modelo quiere agendar la haya dicho EL
// CLIENTE, buscándola en lo que escribió en las últimas horas (el message_log, que es lo que
// de verdad se recibió por WhatsApp, no lo que el modelo recuerda).
//
// Acepta las formas en que la gente escribe una hora en un chat: "14:30", "2:30 pm", "a las 3",
// "15h". Basta con que coincida la HORA; los minutos exactos son cosa del modelo.
func (a *Agent) clienteMencionoHora(from, hora string) bool {
	mins := parseHoraHHMM(hora)
	if mins < 0 {
		return false // sin hora válida no hay nada que verificar
	}
	h := mins / 60

	// Solo mensajes DEL CLIENTE, y de la sesión en curso.
	var dichos []string
	for _, m := range a.store.GetConversation(from, 40) {
		if m.Role == "user" {
			dichos = append(dichos, strings.ToLower(m.Content))
		}
	}
	if len(dichos) == 0 {
		return false
	}
	texto := strings.Join(dichos, " ")

	// Formas equivalentes de la misma hora: 15:00, 15h, 3 pm, 3:00...
	formas := []string{
		fmt.Sprintf("%d:", h), fmt.Sprintf("%02d:", h), // 15:  /  09:
		fmt.Sprintf("%dh", h), fmt.Sprintf("%02dh", h), // 15h
		fmt.Sprintf("las %d", h), fmt.Sprintf("las %02d", h), // a las 15
	}
	if h > 12 { // 15:00 -> "3 pm", "3pm", "las 3"
		d := h - 12
		formas = append(formas, fmt.Sprintf("%d pm", d), fmt.Sprintf("%dpm", d),
			fmt.Sprintf("las %d", d), fmt.Sprintf("%d:", d))
	} else if h > 0 { // 09:00 -> "9 am"
		formas = append(formas, fmt.Sprintf("%d am", h), fmt.Sprintf("%dam", h))
	}
	for _, f := range formas {
		if strings.Contains(texto, f) {
			return true
		}
	}
	return false
}
