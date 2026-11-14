// Confirmar una entrega AGENDADA cuando el mundo cambió entre que se agendó y que se confirma.
//
// Caso real, 14/11 (Pao): agendó 2 BLANCO para las 07:00 del sábado y dijo "Gracias"; el modelo
// volvió a llamar programar_entrega y quedaron #64 y #65. Entre que agendó y que confirmó, alguien
// movió en el panel la apertura del sábado, así que su "Sí, enviar" de las 07:06 se rechazó por
// "FUERA DE HORARIO (hoy de 08:00...)" y se le dijo "inconveniente técnico". Un operador le creó
// el #1013 desde el panel, y su "Ok" siguiente se tomó como otro "sí" a la agendada: tercer
// "inconveniente técnico" con el gas ya en camino.
package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// agenteConAgendadaEnConfirmacion arma un cliente con una entrega agendada a la que ya se le
// mandó el aviso, y un horario del backend de `desde` a `hasta` todos los días.
func agenteConAgendadaEnConfirmacion(t *testing.T, desde, hasta string) (*Agent, conversation.Store, string, conversation.ScheduledOrder) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", BotDiasLaborables: "1-7"}
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDCategoria: 5, IDProducto: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 3, Nombre: "Efectivo"}},
		Horario:  &georoutes.HorarioSemanal{Dias: semanaCompleta(desde, hasta)},
	})
	const from = "593979000001"
	sch := conversation.ScheduledOrder{Phone: from, Nombres: "Pao", IDCategoria: 5, IDProducto: 1,
		IDColor: 10, Cantidad: 2, IDTipoPago: 3, ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO",
		Latitude: -2.912979, Longitude: -79.020942, HoraPropuesta: time.Now().Unix()}
	sch.ID = store.CreateScheduled(sch)
	store.MarkConfirmSent(sch.ID, time.Now().Unix())
	if _, hay := store.GetConfirmingSchedule(from); !hay {
		t.Fatal("la agendada no quedó en confirmación: el test no probaría nada")
	}
	return ag, store, from, sch
}

// horaDesplazada es "HH:MM" de ahora + `min` minutos. Los tests que la usan se saltan cerca de
// la medianoche, donde el horario daría la vuelta al día.
func horaDesplazada(t *testing.T, min int) string {
	t.Helper()
	ahora := time.Now().In(zonaEcuador)
	h := ahora.Add(time.Duration(min) * time.Minute)
	if h.Day() != ahora.Day() {
		t.Skip("cerca de la medianoche el horario cruzaría de día")
	}
	return h.Format("15:04")
}

func TestConElPedidoYaEnCursoLaConfirmacionNoEsUnFallo(t *testing.T) {
	ag, store, from, sch := agenteConAgendadaEnConfirmacion(t, "00:00", "23:59")
	store.SetActivePedido(from, 1013)

	salida := ag.confirmarYRegistrar(from, sch)

	if strings.Contains(salida, "inconveniente técnico") {
		t.Fatalf("con el #1013 ya en camino se le dijo que hubo un fallo: %q", salida)
	}
	if !strings.Contains(salida, "#1013") {
		t.Errorf("la respuesta no le dice cuál es su pedido: %q", salida)
	}
	if _, hay := store.GetConfirmingSchedule(from); hay {
		t.Error("la agendada sigue en confirmación: el próximo \"ok\" volvería a entrar aquí")
	}
	if n := len(store.ListTickets("", 10)); n != 0 {
		t.Errorf("se abrieron %d tickets por un pedido que está bien", n)
	}
}

func TestLaAgendadaRepetidaTambienSeDaPorAtendida(t *testing.T) {
	// #64 y #65: con las dos en confirmación, un solo "sí" con el pedido en curso cierra ambas.
	ag, store, from, sch := agenteConAgendadaEnConfirmacion(t, "00:00", "23:59")
	otra := sch
	otra.ID = 0
	otra.ID = store.CreateScheduled(otra)
	store.MarkConfirmSent(otra.ID, time.Now().Unix())
	store.SetActivePedido(from, 1013)

	ag.confirmarYRegistrar(from, sch)

	if _, hay := store.GetConfirmingSchedule(from); hay {
		t.Error("quedó una agendada en confirmación con el pedido ya en camino")
	}
}

func TestConfirmarAntesDeAbrirNoSeRechazaPorHorario(t *testing.T) {
	// La apertura se movió a DESPUÉS de ahora. La agendada se validó con el horario anterior.
	ag, store, from, _ := agenteConAgendadaEnConfirmacion(t, horaDesplazada(t, 60), horaDesplazada(t, 120))
	// El pedido en curso hace de sonda: si registrar_pedido llega hasta ese candado, pasó el de
	// horario, y el test no necesita backend.
	store.SetActivePedido(from, 1013)

	conAgendada := ag.registrarPedido(&turno{confirmandoAgendado: true}, from,
		map[string]any{"color": "BLANCO", "cantidad": 2})
	if strings.Contains(conAgendada, "FUERA DE HORARIO") {
		t.Fatalf("la confirmación de una agendada se rechazó por la hora de apertura: %q", conAgendada)
	}

	// Un pedido normal a la misma hora SÍ se frena: el candado sigue en pie para todo lo demás.
	normal := ag.registrarPedido(&turno{}, from, map[string]any{"color": "BLANCO", "cantidad": 2})
	if !strings.Contains(normal, "FUERA DE HORARIO") {
		t.Fatalf("un pedido normal antes de abrir pasó el candado de horario: %q", normal)
	}
}

func TestConfirmarDespuesDelCierreNoDiceInconvenienteTecnico(t *testing.T) {
	ag, store, from, sch := agenteConAgendadaEnConfirmacion(t, horaDesplazada(t, -180), horaDesplazada(t, -60))

	salida := ag.confirmarYRegistrar(from, sch)

	if strings.Contains(salida, "inconveniente técnico") {
		t.Fatalf("cerrado no es un fallo técnico y se le dijo que lo era: %q", salida)
	}
	if _, hay := store.GetConfirmingSchedule(from); hay {
		t.Error("la agendada sigue en confirmación: cada \"ok\" la volvería a intentar")
	}
}

func TestNoSeAgendaDosVecesLaMismaEntrega(t *testing.T) {
	a, from := agentePruebaC6()
	args := map[string]any{"color": "BLANCO", "cantidad": 2, "hora": "07:00", "dia": "manana"}

	if primera := a.programarEntrega(from, args); !strings.Contains(strings.ToLower(primera), "programada") {
		t.Fatalf("la primera no se agendó, el test no probaría nada: %q", primera)
	}
	segunda := a.programarEntrega(from, args)

	if !strings.Contains(segunda, "YA tiene una entrega programada") {
		t.Errorf("la segunda llamada no avisó que ya existe: %q", segunda)
	}
	vivas := 0
	for _, s := range a.store.ListScheduled(conversation.SchedulePendiente, 10) {
		if s.Phone == from {
			vivas++
		}
	}
	if vivas != 1 {
		t.Errorf("quedaron %d entregas agendadas; tiene que haber una", vivas)
	}
}
