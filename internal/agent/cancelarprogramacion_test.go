package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// La CANCELACIÓN DE LA ENTREGA AGENDADA se resuelve en código, no por el modelo.
//
// De dónde sale esto: el inventario del 11/09 (cada herramienta contra sus protecciones) mostró
// que cancelar_programacion era la ÚNICA acción que cambia estado sin interceptor NI candado de
// texto — el gemelo exacto del bug de cancelar_pedido, pero sin nada. Y con la puerta abierta:
// pideCancelarPedido manda "cancela la programación" al modelo a propósito, por ser otra
// herramienta. Si el modelo no llamaba la tool, la entrega seguía viva y el cliente se enteraba
// cuando le tocaban la puerta a la hora que creía cancelada.

// clienteConProgramacionViva deja al cliente con una entrega agendada pendiente.
func clienteConProgramacionViva(t *testing.T, store conversation.Store, from string) {
	t.Helper()
	id := store.CreateScheduled(conversation.ScheduledOrder{
		Phone:         from,
		Cantidad:      1,
		HoraPropuesta: time.Now().Add(3 * time.Hour).Unix(),
		Estado:        conversation.SchedulePendiente,
	})
	if id == 0 {
		t.Fatal("no se pudo crear la entrega agendada de prueba")
	}
	if !store.TieneProgramacionViva(from) {
		t.Fatal("la entrega agendada de prueba no quedó viva")
	}
}

// EL CASO: el cliente pide cancelar su entrega agendada y el CÓDIGO la cancela — sin depender
// de que el modelo se acuerde de llamar la herramienta.
func TestLaCancelacionDeProgramacionNoDependeDelModelo(t *testing.T) {
	const from = "593999000800"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteConProgramacionViva(t, store, from)

	reply, manejado := ag.ResponderCancelarProgramacion(from, "cancelar la programación")
	if !manejado {
		t.Fatal("no se resolvió en código: la cancelación de la entrega agendada seguiría " +
			"dependiendo de que el modelo llame cancelar_programacion, que era la única " +
			"herramienta SIN interceptor ni candado (inventario 11/09)")
	}
	if store.TieneProgramacionViva(from) {
		t.Error("la entrega agendada SIGUE VIVA: al cliente le llegaría el pedido a su hora")
	}
	if !strings.Contains(reply, "cancelé tu entrega programada") {
		t.Errorf("no se le confirmó la cancelación al cliente: %q", reply)
	}
}

// Las formas en que la gente lo pide.
func TestCancelarProgramacionReconoceLasFormasDelCliente(t *testing.T) {
	for i, texto := range []string{
		"cancelar programación", "Cancela la programación", "cancelar mi entrega agendada",
		"anula la programación", "cancela la entrega programada",
		"cancélame por favor la programación", "ya no quiero la entrega programada",
		"cancelar el agendamiento",
	} {
		from := "59399900081" + string(rune('0'+i%10))
		store := conversation.NewMemStore()
		ag := agentDePrueba(nil, store)
		clienteConProgramacionViva(t, store, from)

		if _, manejado := ag.ResponderCancelarProgramacion(from, texto); !manejado {
			t.Errorf("%q no se resolvió en código", texto)
			continue
		}
		if store.TieneProgramacionViva(from) {
			t.Errorf("%q: la entrega agendada sigue viva", texto)
		}
	}
}

// REGLA DURA: lo ambiguo NO se cancela. Borrarle a alguien una entrega que sí quería es peor
// que dejar pasar el mensaje al modelo.
func TestCancelarProgramacionNoActuaSobreLoAmbiguo(t *testing.T) {
	const from = "593999000820"
	for _, texto := range []string{
		"¿puedo cancelar mi programación?",  // pregunta
		"¿me cancelas la entrega agendada?", // pregunta
		"cancelar",                          // suelto: puede ser el pedido, el menú de espera...
		"cancela mi pedido",                 // la OTRA herramienta
		"cancelar pedido",                   // la OTRA herramienta
		"quiero programar una entrega",      // lo contrario
		"hola",
		"quiero 2 blancos",
	} {
		store := conversation.NewMemStore()
		ag := agentDePrueba(nil, store)
		clienteConProgramacionViva(t, store, from)

		if _, manejado := ag.ResponderCancelarProgramacion(from, texto); manejado {
			t.Errorf("%q se tomó como orden de cancelar la programación; debía ir al modelo", texto)
		}
		if !store.TieneProgramacionViva(from) {
			t.Errorf("%q: se canceló una entrega agendada que el cliente no pidió cancelar", texto)
		}
	}
}

// Sin entrega agendada no se intercepta: "cancelar la programación" puede ser una confusión.
func TestSinProgramacionVivaVaAlModelo(t *testing.T) {
	const from = "593999000821"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	if _, manejado := ag.ResponderCancelarProgramacion(from, "cancelar programación"); manejado {
		t.Error("sin entrega agendada no hay nada que cancelar en código")
	}
}

// LOS DOS GEMELOS NO SE PISAN: quien pide cancelar el PEDIDO no pierde su entrega agendada, y
// quien pide cancelar la PROGRAMACIÓN no pierde su pedido vivo. Es el reparto que hace
// pideCancelarPedido al descartar "programacion/agendada" — y que dejaba sin red a la otra.
func TestLosDosGemelosNoSePisan(t *testing.T) {
	const from = "593999000822"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteConProgramacionViva(t, store, from)
	store.SetActivePedido(from, 777)

	// "cancela la programación" NO debe tocar el pedido inmediato.
	if _, manejado := ag.ResponderCancelacion(from, "cancela la programación"); manejado {
		t.Error("ResponderCancelacion se hizo cargo de una orden sobre la PROGRAMACIÓN")
	}
	if _, sigue := store.GetActivePedido(from); !sigue {
		t.Error("se canceló el pedido inmediato cuando el cliente habló de la programación")
	}

	// Y "cancelar pedido" NO debe tocar la entrega agendada.
	if _, manejado := ag.ResponderCancelarProgramacion(from, "cancelar pedido"); manejado {
		t.Error("ResponderCancelarProgramacion se hizo cargo de una orden sobre el PEDIDO")
	}
	if !store.TieneProgramacionViva(from) {
		t.Error("se canceló la entrega agendada cuando el cliente habló del pedido")
	}
}

// El PARACAÍDAS del camino conversacional: si el modelo afirma que canceló la entrega agendada
// sin llamar la herramienta, el código la cancela de verdad.
func TestCandadoDeProgramacionCanceladaDetectaLaAfirmacion(t *testing.T) {
	for _, texto := range []string{
		"Listo, tu entrega programada ha sido cancelada",
		"Ya cancelé tu programación",
		"He cancelado la entrega agendada",
		"Tu agendamiento fue cancelado",
	} {
		if !afirmaProgramacionCancelada(texto) {
			t.Errorf("no se detectó la afirmación: %q — el modelo mentiría sin que nadie lo note", texto)
		}
	}
	// Y lo que NO es una afirmación de haberlo hecho.
	for _, texto := range []string{
		"¿Quieres que cancele tu entrega programada?",
		"Tu entrega programada sigue activa",
		"¿Deseas que cancele la programación?",
	} {
		if afirmaProgramacionCancelada(texto) {
			t.Errorf("se tomó como afirmación algo que no lo es: %q", texto)
		}
	}
}

func TestElParacaidasCancelaLaProgramacionDeVerdad(t *testing.T) {
	const from = "593999000823"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteConProgramacionViva(t, store, from)

	reply, ok := ag.forzarCancelacionProgramadaSiHaceFalta(from)
	if !ok {
		t.Fatal("el paracaídas no se hizo cargo con una programación viva")
	}
	if store.TieneProgramacionViva(from) {
		t.Error("la entrega agendada sigue viva tras el rescate")
	}
	if !strings.Contains(reply, "cancelé tu entrega programada") {
		t.Errorf("texto inesperado para el cliente: %q", reply)
	}
}

// GUARD ESTRUCTURAL: el interceptor tiene que estar cableado en cmd/bot y ANTES del modelo.
// Sin esto, alguien borra la llamada y los tests de arriba siguen verdes mientras en producción
// la cancelación de la entrega agendada vuelve a depender del modelo.
func TestLaCancelacionDeProgramacionEstaCableadaEnElWebhook(t *testing.T) {
	src, err := os.ReadFile("../../cmd/bot/main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	cuerpo := string(src)
	if !strings.Contains(cuerpo, "ag.ResponderCancelarProgramacion(") {
		t.Fatal("cmd/bot NO llama a ResponderCancelarProgramacion: la entrega agendada volvería a " +
			"ser la única acción que cambia estado sin protección (inventario 11/09)")
	}
	iCancel := strings.Index(cuerpo, "ag.ResponderCancelarProgramacion(")
	iModelo := strings.Index(cuerpo, "ag.HandleMessage(")
	if iModelo > 0 && iCancel > iModelo {
		t.Error("ResponderCancelarProgramacion está cableado DESPUÉS de HandleMessage: el modelo contestaría primero")
	}
}

// BUG PREEXISTENTE, destapado por el inventario del 11/09: CANCELAR una entrega agendada no es
// HABERLA CREADO. "Ya cancelé tu entrega programada" contiene la secuencia {entrega, programada},
// así que afirmaProgramado lo leía como una programación recién hecha y el candado del fantasma
// respondía REGISTRANDO UN PEDIDO — lo contrario exacto de lo que el cliente pidió. Llevaba ahí
// desde que existe el candado; solo aparece al probar el camino real, nunca las funciones sueltas.
func TestCancelarUnaProgramacionNoEsHaberlaCreado(t *testing.T) {
	for _, texto := range []string{
		"¡Listo! Ya cancelé tu entrega programada 🙏",
		"He cancelado la entrega agendada",
		"Tu entrega programada ha sido cancelada",
	} {
		if afirmaProgramado(texto) {
			t.Errorf("se leyó como una programación CREADA: %q — el candado registraría un pedido "+
				"justo cuando el cliente pidió cancelar", texto)
		}
	}
	// Y lo que SÍ es una programación creada sigue detectándose.
	for _, texto := range []string{
		"Tu entrega quedó programada para mañana a las 10",
		"Listo, ya agendé tu entrega",
	} {
		if !afirmaProgramado(texto) {
			t.Errorf("dejó de detectarse una programación real: %q", texto)
		}
	}
}

// EL PARACAÍDAS, POR EL CAMINO REAL. Un turno completo de HandleMessage con un modelo que
// AFIRMA haber cancelado sin llamar la herramienta — que es exactamente lo que hace en
// producción. No basta con probar afirmaProgramacionCancelada por su cuenta ni con buscar el
// string en agent.go: un guard así sigue verde aunque el candado esté desactivado (se comprobó
// con un mutante `if false && ...`). Lo único que prueba que la red existe es atravesarla.
func TestIncidente_ElModeloDiceQueCancelaLaProgramacionYNoLaCancela(t *testing.T) {
	const from = "593999000824"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"¡Listo! Ya cancelé tu entrega programada 🙏",
	}}
	ag := agentIncidente(fake, store)
	clienteConProgramacionViva(t, store, from)

	// El texto NO pasa por el interceptor a propósito: es el camino conversacional, donde el
	// cliente lo pide con sus palabras y solo queda la red.
	res, err := ag.HandleMessage(context.Background(), from, "mejor déjalo, no me mandes nada mañana")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if store.TieneProgramacionViva(from) {
		t.Fatalf("el modelo afirmó cancelarla y la entrega agendada SIGUE VIVA: al cliente le "+
			"llegaría el pedido a su hora creyendo que lo canceló. Respuesta: %q", res.Texto)
	}
}

// Y el reverso: si NO hay nada agendado, el texto del modelo pasa tal cual. El candado no puede
// inventar una cancelación ni cambiarle la respuesta a quien habla de otra cosa.
func TestSinProgramacionElCandadoNoTocaLaRespuesta(t *testing.T) {
	const from = "593999000825"
	store := conversation.NewMemStore()
	const dicho = "Tu entrega programada ya fue cancelada la semana pasada"
	fake := &modeloQueDice{respuestas: []string{dicho}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "qué pasó con lo de la semana pasada")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto != dicho {
		t.Errorf("el candado alteró una respuesta que no tenía nada que rescatar:\n  dijo: %q\n  salió: %q", dicho, res.Texto)
	}
}
