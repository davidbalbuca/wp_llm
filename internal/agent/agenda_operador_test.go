// ENTREGA AGENDADA A MANO DESDE EL CHAT DEL PANEL (16/10).
package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

func agenteAgenda(t *testing.T) (*Agent, string) {
	store := conversation.NewMemStore()
	const from = "593984000031"
	store.SetAccount(from, conversation.Account{Username: "wa593984000031", Password: "p"})
	store.SetProfile(from, conversation.Profile{Nombres: "Rosa Quizhpi"})
	store.SetLocation(from, -2.90, -79.00)
	store.LogMessage(from, "user", "tráemelo más tarde porfa") // su último mensaje: ahora
	a := &Agent{store: store, cfg: config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "20:30",
		BotDiasLaborables: "1-7"}}
	return a, from
}

// unaHoraQueSirve: la primera hora en punto, entre las 08:00 y las 20:00, que esté al menos 20 min
// adelante y dentro de las 24 h de WhatsApp (siempre hay una: no depende de a qué hora corra).
func unaHoraQueSirve() time.Time {
	ahora := time.Now().In(zonaEcuador)
	h := ahora.Truncate(time.Hour).Add(time.Hour)
	for !(h.Hour() >= 8 && h.Hour() <= 20 && h.Sub(ahora) >= 20*time.Minute) {
		h = h.Add(time.Hour)
	}
	return h
}

func agendaDePrueba(from string, cuando time.Time) AgendaDeOperador {
	return AgendaDeOperador{Phone: from, IDTipoPago: 1, FechaHora: cuando.Format("2006-01-02 15:04"), Operador: "nicole",
		Items: []conversation.PendingWaitItem{{IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 2,
			ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO"}}}
}

func TestOperadorAgendaYQuedaComoLasDelBot(t *testing.T) {
	a, from := agenteAgenda(t)
	cuando := unaHoraQueSirve()
	res := a.AgendarPedidoDeOperador(agendaDePrueba(from, cuando))
	if !res.OK {
		t.Fatalf("no agendó: %s", res.Motivo)
	}
	pendientes := a.store.ListScheduled(conversation.SchedulePendiente, 10)
	if len(pendientes) != 1 {
		t.Fatalf("pendientes = %d; quería 1", len(pendientes))
	}
	s := pendientes[0]
	if s.Phone != from || s.Cantidad != 2 || s.IDColor != 10 || s.HoraPropuesta != cuando.Unix() || s.Nombres != "Rosa Quizhpi" ||
		s.Latitude != -2.90 {
		t.Errorf("agendado = %+v", s)
	}
	// Queda en la conversación (lo ve el modelo y el panel), sin escribirle al cliente.
	ultimo := a.store.GetConversation(from, 5)
	if len(ultimo) == 0 || !strings.Contains(ultimo[len(ultimo)-1].Content, "Entrega programada #") ||
		!strings.Contains(ultimo[len(ultimo)-1].Content, "nicole") {
		t.Errorf("no quedó la nota en la conversación: %+v", ultimo)
	}
}

func TestOperadorNoAgendaLoQueNoSePuede(t *testing.T) {
	casos := []struct {
		nombre string
		cambia func(a *Agent, from string, p *AgendaDeOperador)
		motivo string
	}{
		{"muy pronto", func(a *Agent, from string, p *AgendaDeOperador) {
			p.FechaHora = time.Now().In(zonaEcuador).Add(5 * time.Minute).Format("2006-01-02 15:04")
		}, "muy cerca"},
		{"fuera de horario", func(a *Agent, from string, p *AgendaDeOperador) {
			c := unaHoraQueSirve()
			p.FechaHora = time.Date(c.Year(), c.Month(), c.Day(), 21, 30, 0, 0, zonaEcuador).Format("2006-01-02 15:04")
		}, "atendemos de 07:00 a 20:30"},
		{"pasadas las 24 h de WhatsApp", func(a *Agent, from string, p *AgendaDeOperador) {
			c := unaHoraQueSirve().Add(48 * time.Hour)
			p.FechaHora = c.Format("2006-01-02 15:04")
		}, "24 h"},
		{"dos colores", func(a *Agent, from string, p *AgendaDeOperador) {
			p.Items = append(p.Items, conversation.PendingWaitItem{IDProducto: 1, IDColor: 11, Cantidad: 1})
		}, "un solo color"},
		{"sin ubicación", func(a *Agent, from string, p *AgendaDeOperador) {
			a.store = conversation.NewMemStore()
			a.store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
			a.store.LogMessage(from, "user", "hola")
		}, "ubicación"},
		{"ya tiene una agendada", func(a *Agent, from string, p *AgendaDeOperador) {
			a.store.CreateScheduled(conversation.ScheduledOrder{Phone: from, Cantidad: 1,
				HoraPropuesta: unaHoraQueSirve().Unix()})
		}, "ya tiene una entrega agendada"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			a, from := agenteAgenda(t)
			p := agendaDePrueba(from, unaHoraQueSirve())
			c.cambia(a, from, &p)
			antes := len(a.store.ListScheduled(conversation.SchedulePendiente, 10))
			res := a.AgendarPedidoDeOperador(p)
			if res.OK || !strings.Contains(res.Motivo, c.motivo) {
				t.Errorf("resultado = %+v; quería el motivo %q", res, c.motivo)
			}
			if despues := len(a.store.ListScheduled(conversation.SchedulePendiente, 10)); despues != antes {
				t.Errorf("guardó una entrega aunque no correspondía (%d -> %d)", antes, despues)
			}
		})
	}
}
