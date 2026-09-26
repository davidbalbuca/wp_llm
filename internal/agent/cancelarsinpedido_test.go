// "NO TIENES PEDIDO" NO ES UN ERROR TÉCNICO.
//
// INCIDENTE 26/09, ticket #56 (Carlos, 593987295556). Tras una conversación en la que el bot le
// preguntó cuatro veces la cantidad, escribió "Ya no quiero x incopetente no saben decir lo q
// estoy diciendo". El bot abrió este ticket:
//
//	"Error técnico: no se pudo cancelar el pedido del cliente porque el sistema no encuentra su
//	 cuenta. Carlos pidió cancelar su pedido de 5 cilindros amarillos… Está molesto, necesita que
//	 alguien revise y confirme la cancelación."
//
// Su estado real en la base del bot:
//
//	accounts          (nada)   <- nunca tuvo cuenta
//	active_pedido     (nada)   <- nunca tuvo pedido
//	scheduled_orders  (nada)   <- NO tenía entrega agendada
//
// No había 5 cilindros. No había nada que cancelar. El ticket mandaba a un operador a confirmar
// la cancelación de un pedido inexistente, describiendo un pedido que el modelo compuso de la
// conversación.
//
// Dos fallos encadenados, los dos cubiertos aquí:
//  1. el candado cedía el turno al no haber pedido, y el modelo llamaba a la herramienta igual;
//  2. la herramienta devolvía prosa PARA EL MODELO ("No encuentro la cuenta del cliente…") que el
//     modelo ascendió a diagnóstico de avería.
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

func TestIncidente56_SinCuentaNoEsErrorTecnico(t *testing.T) {
	const from = "593987295556" // el teléfono real de Carlos
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	// Su mensaje literal.
	msg, resuelto := ag.ResponderCancelacion(from, "ya no lo quiero")
	if !resuelto {
		t.Fatal("el código cedió el turno al modelo: ahí es donde nació el ticket #56")
	}

	bajo := strings.ToLower(msg)
	// No se le habla de averías a quien simplemente no tiene pedido.
	for _, prohibido := range []string{"error", "técnico", "tecnico", "problema", "disculpa"} {
		if strings.Contains(bajo, prohibido) {
			t.Errorf("el mensaje trata un estado normal como avería (%q): %q", prohibido, msg)
		}
	}
	// Y NO se promete ningún aviso al equipo, porque no hay nada que avisar.
	if afirmaAvisoAlEquipo(msg) {
		t.Errorf("se le promete un contacto del equipo sin motivo: %q", msg)
	}
	// Lo que sí: decirle la verdad y dejar la puerta abierta.
	if !strings.Contains(bajo, "no tienes") && !strings.Contains(bajo, "no hay") {
		t.Errorf("el mensaje no le dice con claridad que no hay pedido: %q", msg)
	}

	// LO MÁS IMPORTANTE: cero tickets. Nadie tiene que ir a cancelar nada.
	if n := len(store.ListTickets("abierto", 50)); n != 0 {
		t.Errorf("se abrieron %d tickets por un cliente que no tenía pedido", n)
	}
}

// El motivo estructurado distingue el estado normal de la avería. Es lo único que decide si se
// abre ticket, en vez de la redacción de una frase.
func TestMotivoCancelacionSeparaAveriaDeEstadoNormal(t *testing.T) {
	casos := []struct {
		m      motivoCancelacion
		averia bool
		vacio  bool
		nombre string
	}{
		{cancelacionHecha, false, false, "hecha"},
		{cancelacionSinCuenta, false, true, "sin_cuenta"},
		{cancelacionSinPedido, false, true, "sin_pedido"},
		{cancelacionFalloBackend, true, false, "backend_fallo"},
	}
	for _, c := range casos {
		if c.m.esAveria() != c.averia {
			t.Errorf("%s: esAveria()=%v, esperado %v", c.nombre, c.m.esAveria(), c.averia)
		}
		if c.m.nadaQueCancelar() != c.vacio {
			t.Errorf("%s: nadaQueCancelar()=%v, esperado %v", c.nombre, c.m.nadaQueCancelar(), c.vacio)
		}
		if c.m.String() != c.nombre {
			t.Errorf("String()=%q, esperado %q", c.m.String(), c.nombre)
		}
	}
}

// Solo el mensaje de avería promete el aviso al equipo; los otros no pueden hacerlo.
func TestSoloLaAveriaPrometeAvisoAlEquipo(t *testing.T) {
	if !afirmaAvisoAlEquipo(cancelacionFalloBackend.mensajeAlCliente()) {
		t.Error("el fallo de backend SÍ debe avisar al equipo (y el ticket se crea antes de decirlo)")
	}
	for _, m := range []motivoCancelacion{cancelacionHecha, cancelacionSinCuenta, cancelacionSinPedido} {
		if afirmaAvisoAlEquipo(m.mensajeAlCliente()) {
			t.Errorf("%s promete un aviso al equipo que nadie va a hacer: %q", m, m.mensajeAlCliente())
		}
	}
}

// Con una entrega agendada, "cancelar" es ambiguo y el turno SE CEDE: puede querer cancelar esa
// entrega, y eso lo atiende quien sabe hacerlo. El arreglo no puede comerse ese camino.
func TestConEntregaAgendadaSeCedeElTurno(t *testing.T) {
	const from = "593987295557"
	store := conversation.NewMemStore()
	store.CreateScheduled(conversation.ScheduledOrder{
		Phone: from, ColorNombre: "AMARILLO", Cantidad: 2, Estado: conversation.SchedulePendiente,
	})
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	if _, resuelto := ag.ResponderCancelacion(from, "cancelar"); resuelto {
		t.Error("con una entrega agendada el turno debe cederse para poder cancelarla")
	}
}

// EL TICKET NO PUEDE AFIRMAR LO QUE EL MODELO IMAGINÓ.
//
// El #56 decía "su pedido de 5 cilindros amarillos" y en la base no había ni cuenta ni pedido: el
// modelo compuso la cantidad de una conversación donde el cliente nunca la dijo. Quien atiende un
// ticket ACTÚA sobre esos datos, así que el resumen del modelo no puede viajar solo.
func TestElTicketLlevaElEstadoRealJuntoAlRelato(t *testing.T) {
	const from = "593987295556"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	// El resumen TAL CUAL lo escribió el modelo aquel día.
	inventado := "Carlos pidió cancelar su pedido de 5 cilindros amarillos por mala experiencia."
	ag.crearTicketSoporte(from, "Error técnico", inventado)

	tickets := store.ListTickets("abierto", 10)
	if len(tickets) != 1 {
		t.Fatalf("se esperaba 1 ticket, hay %d", len(tickets))
	}
	resumen := tickets[0].Resumen

	// El relato del modelo se conserva (describe la queja mejor que ningún campo)...
	if !strings.Contains(resumen, inventado) {
		t.Error("se perdió el relato del cliente; el operador necesita saber qué contó")
	}
	// ...pero etiquetado como SIN VERIFICAR, no como hecho del sistema.
	if !strings.Contains(strings.ToLower(resumen), "sin verificar") {
		t.Errorf("el relato no se marca como no verificado:\n%s", resumen)
	}
	// Y el estado real va SIEMPRE, contradiciendo al relato cuando toca.
	for _, debe := range []string{
		"Cuenta: NO tiene",         // nunca completó un pedido
		"Pedido en curso: NINGUNO", // los "5 cilindros" no existen
		"Entrega agendada: NO",
		from, // el teléfono, para poder llamarle
	} {
		if !strings.Contains(resumen, debe) {
			t.Errorf("el ticket no dice %q; así nadie ve que el relato no cuadra:\n%s", debe, resumen)
		}
	}
}

// Un dato que no está en la base se dice "no registrado": NUNCA se rellena.
func TestLoQueNoEstaEnLaBaseSeDiceQueNoEsta(t *testing.T) {
	const from = "593987295558"
	store := conversation.NewMemStore()
	// Ficha a medias EXACTA de Carlos: color sí, cantidad 0, hora que nadie pidió.
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "AMARILLO", Cantidad: 0})
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	estado := ag.estadoParaTicket(from)
	if !strings.Contains(estado, "cantidad=no registrada") {
		t.Errorf("una cantidad 0 debe salir como NO registrada, no como 0 cilindros:\n%s", estado)
	}
	if !strings.Contains(estado, "color=AMARILLO") {
		t.Errorf("el color sí estaba y debe aparecer:\n%s", estado)
	}
}
