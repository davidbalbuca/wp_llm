package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// EL BOTÓN "CANCELAR" DEL MENÚ DE ESPERA NO CANCELA — Y ANTES NO LO DECÍA.
//
// QA, 15/09: pulsó "Cancelar" a las 14:51 y el bot respondió "Entendido 🙏. Si prefieres, puedo
// agendarte la entrega para más tarde…". Suena a cancelado. No lo está: cancelarEspera deja el
// pedido en la cola de NO ASIGNADOS, que el equipo llama para ofrecérselo. En el panel seguía
// ahí dos horas y media después, entre otros 13 registros.
//
// El daño es que alguien del equipo llama a un cliente convencido de haber cancelado, para
// ofrecerle un pedido a una dirección que —por H-03— quizá tampoco eligió.
//
// Este flujo es DISTINTO de cancelar un pedido con conductor asignado (cancelarPedido → backend,
// ver cancelacion.go). Aquí no hay pedido que cancelar: hay una espera que se corta.

func agenteConEspera(t *testing.T, from string) (*Agent, conversation.Store) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}
	// El cliente está esperando repartidor: es la precondición del menú.
	store.SetPendingWait(from, conversation.PendingWait{
		IDProducto: 1, IDColor: 10, Cantidad: 2, IDTipoPago: 1,
	})
	return ag, store
}

// El mensaje no puede dar a entender que el pedido desapareció, porque no desapareció.
func TestCancelarLaEsperaNoDiceQueSeCancelo(t *testing.T) {
	const from = "593999500001"
	ag, _ := agenteConEspera(t, from)

	respuesta, manejado := ag.ResponderMenuEspera(from, "Cancelar")
	if !manejado {
		t.Fatal("el menú de espera no tomó el turno al pulsar Cancelar")
	}
	bajo := strings.ToLower(respuesta)

	// "Cancelé tu pedido" sería mentira: sigue en la cola de No asignados.
	for _, frase := range []string{"cancele tu pedido", "cancelé tu pedido", "he cancelado tu pedido"} {
		if strings.Contains(bajo, frase) {
			t.Errorf("el mensaje afirma una cancelación que no ocurrió: %q", respuesta)
		}
	}
	// Y sí tiene que decir qué pasó de verdad: que se dejó de buscar repartidor.
	if !strings.Contains(bajo, "repartidor") {
		t.Errorf("el mensaje no dice que se dejó de buscar repartidor, que es lo único que pasó: %q", respuesta)
	}
}

// La espera SÍ se corta: el estado local queda limpio para que no siga en el limbo.
func TestCancelarLaEsperaLimpiaElEstado(t *testing.T) {
	const from = "593999500002"
	ag, store := agenteConEspera(t, from)

	if _, hay := store.GetPendingWait(from); !hay {
		t.Fatal("el montaje del test no dejó al cliente en espera")
	}
	if _, manejado := ag.ResponderMenuEspera(from, "Cancelar"); !manejado {
		t.Fatal("el menú de espera no tomó el turno")
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la espera sigue viva tras cancelar: el cliente queda en el limbo y el menú " +
			"le volverá a aparecer")
	}
}

// EL TURNO TIENE QUE QUEDAR EN EL HISTORIAL.
//
// Este es el síntoma que QA reportó como "dos horas después el menú seguía ofreciendo esperar o
// programar": el interceptor respondía sin escribir nada en la memoria del modelo, así que para
// él ese turno nunca ocurrió. Al siguiente mensaje del cliente volvía a ofrecer lo mismo.
func TestElTurnoDelMenuDeEsperaQuedaEnElHistorial(t *testing.T) {
	casos := []struct {
		nombre    string
		respuesta string
	}{
		{"cancelar", "Cancelar"},
		{"esperar", "Esperar"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			from := "59399950001" + c.nombre[:1]
			ag, store := agenteConEspera(t, from)

			antes := len(store.History(from))
			if _, manejado := ag.ResponderMenuEspera(from, c.respuesta); !manejado {
				t.Fatalf("el menú de espera no tomó el turno con %q", c.respuesta)
			}
			despues := store.History(from)
			if len(despues) <= antes {
				t.Errorf("el turno no quedó en el historial (antes %d, después %d): el modelo no se "+
					"entera de que el cliente respondió el menú y se lo vuelve a ofrecer",
					antes, len(despues))
			}
		})
	}
}
