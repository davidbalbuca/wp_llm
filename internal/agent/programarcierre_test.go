// LA HORA DE CIERRE (19:00) SE PUEDE AGENDAR: el bot la anuncia como válida, no puede negarla.
//
// C6 (task 45): el bot dice "atendemos de 07:00 a 19:00, apenas me digas una hora la agendo". El
// cliente dice "7 de la noche" (= 19:00 tras el arreglo C2). La validación era `mins >= fin`, así
// que 19:00 == 1140 >= 1140 se rechazaba por "fuera de horario": ofrecer un límite y negarlo al
// tomarlo. El horario de entregas es CERRADO en ambos extremos [07:00, 19:00].
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

func agentePruebaC6() (*Agent, string) {
	cfg := config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00", BotDiasLaborables: "1-7"}
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDCategoria: 5, IDProducto: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 3, Nombre: "Efectivo"}},
	})
	store := conversation.NewMemStore()
	const from = "593979444899"
	store.SetLocation(from, -2.898, -79.002)
	store.SetProfile(from, conversation.Profile{Identificacion: "0102030405", Nombres: "Ana Prueba"})
	// Cuenta ya presente: así programarEntrega NO llama a WppGetOrCreateClient (red) en el test.
	store.SetAccount(from, conversation.Account{Username: "0102030405_xX", Password: "p"})
	return &Agent{store: store, catalog: cat, cfg: cfg}, from
}

// argumentos que el modelo pasaría al tener todo: color, cantidad y la hora que dijo el cliente.
// Sin 'dia': que el código decida hoy/mañana según el reloj, para no chocar con la ventana de 24 h.
func argsProgramar(hora string) map[string]any {
	return map[string]any{"color": "BLANCO", "cantidad": 2, "hora": hora}
}

// El corazón de C6: las 19:00 NO se rechazan por HORARIO. Es robusto al reloj — el único rechazo
// que este test prohíbe es "fuera del horario"; los otros (24 h, "ya pasaron hoy") dependen de la
// hora en que corra la suite y no son el bug que arreglamos.
func TestLaHoraDeCierre1900NoSeRechazaPorHorario(t *testing.T) {
	a, from := agentePruebaC6()
	salida := a.programarEntrega(from, argsProgramar("19:00"))
	if strings.Contains(strings.ToLower(salida), "fuera del horario") {
		t.Fatalf("las 19:00 (hora de cierre que el bot ofrece) se rechazaron por horario: %q", salida)
	}
}

// Y que de verdad AGENDA: para no depender del reloj, se agenda para MAÑANA a una hora temprana
// (siempre dentro de la ventana de 24 h) pero se prueba el límite superior por separado arriba.
func TestUnaHoraValidaSeAgenda(t *testing.T) {
	a, from := agentePruebaC6()
	salida := a.programarEntrega(from, map[string]any{"color": "BLANCO", "cantidad": 2,
		"hora": "07:00", "dia": "manana"})
	if !strings.Contains(strings.ToLower(salida), "programada") {
		t.Fatalf("una hora válida no se agendó: %q", salida)
	}
	if n := a.store.CountScheduled(conversation.SchedulePendiente); n != 1 {
		t.Errorf("no se creó la entrega programada (agendadas=%d)", n)
	}
}

func TestUnMinutoDespuesDelCierreSiSeRechaza(t *testing.T) {
	a, from := agentePruebaC6()
	salida := a.programarEntrega(from, argsProgramar("19:01"))
	if !strings.Contains(strings.ToLower(salida), "fuera del horario") {
		t.Errorf("las 19:01 deberían quedar fuera del horario: %q", salida)
	}
}

func TestAntesDeAbrirSiSeRechaza(t *testing.T) {
	a, from := agentePruebaC6()
	salida := a.programarEntrega(from, argsProgramar("06:59"))
	if !strings.Contains(strings.ToLower(salida), "fuera del horario") {
		t.Errorf("las 06:59 deberían quedar fuera del horario: %q", salida)
	}
}

// El puente completo del caso real: "7 de la noche" -> extraerHora -> 19:00 -> se agenda.
func TestSieteDeLaNocheSeAgendaComoLas1900(t *testing.T) {
	if h := extraerHora("a las 7 de la noche"); h != "19:00" {
		t.Fatalf("extraerHora no dio 19:00: %q", h)
	}
	a, from := agentePruebaC6()
	salida := a.programarEntrega(from, argsProgramar("a las 7 de la noche"))
	if strings.Contains(strings.ToLower(salida), "fuera del horario") {
		t.Errorf("\"7 de la noche\" se rechazó por horario: %q", salida)
	}
}
