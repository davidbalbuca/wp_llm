package notify

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// Un problema que se repite es UN caso, no tres. El 05/09 un mismo pedido generó los tickets
// #20, #21 y #22 y el equipo tuvo que adivinar cuál mirar.
func TestReportarFalloNoDuplicaTicketDelMismoMotivo(t *testing.T) {
	store := conversation.NewMemStore()
	cfg := config.Config{}
	const phone, motivo = "593999000030", "Error técnico del agente"

	primero := ReportarFallo(cfg, store, phone, motivo, "la IA falló: timeout")
	if primero == 0 {
		t.Fatal("no se creó el primer ticket")
	}

	segundo := ReportarFallo(cfg, store, phone, motivo, "la IA falló otra vez: 429")
	if segundo != primero {
		t.Errorf("se abrió un ticket nuevo (#%d) por el mismo motivo; debía sumarse a #%d", segundo, primero)
	}
	if abiertos := store.ListTickets(conversation.TicketAbierto, 50); len(abiertos) != 1 {
		t.Errorf("hay %d tickets abiertos, esperaba 1", len(abiertos))
	}

	// El detalle nuevo NO se pierde: queda en la conversación para quien abra el caso.
	var visto bool
	for _, m := range store.GetConversation(phone, 50) {
		if strings.Contains(m.Content, "429") {
			visto = true
		}
	}
	if !visto {
		t.Error("el detalle del segundo fallo se perdió: el equipo no vería qué pasó la segunda vez")
	}
}

// Motivos DISTINTOS son problemas distintos: cada uno merece su caso.
func TestReportarFalloAbreTicketPorMotivoDistinto(t *testing.T) {
	store := conversation.NewMemStore()
	cfg := config.Config{}
	const phone = "593999000031"

	a := ReportarFallo(cfg, store, phone, "Error técnico del agente", "timeout")
	b := ReportarFallo(cfg, store, phone, "Pedido fantasma", "confirmó sin registrar")
	if a == b {
		t.Error("dos problemas distintos quedaron en el mismo ticket")
	}
}

// Clientes distintos con el mismo motivo son casos distintos.
func TestReportarFalloNoMezclaClientes(t *testing.T) {
	store := conversation.NewMemStore()
	cfg := config.Config{}
	const motivo = "Error técnico del agente"

	maria := ReportarFallo(cfg, store, "593999000032", motivo, "timeout")
	juan := ReportarFallo(cfg, store, "593999000033", motivo, "timeout")
	if maria == juan {
		t.Error("el fallo de un cliente se sumó al ticket de otro")
	}
}

// Un ticket CERRADO no bloquea abrir uno nuevo: el problema volvió a ocurrir.
func TestReportarFalloReabreSiElAnteriorSeCerro(t *testing.T) {
	store := conversation.NewMemStore()
	cfg := config.Config{}
	const phone, motivo = "593999000034", "Error técnico del agente"

	primero := ReportarFallo(cfg, store, phone, motivo, "timeout")
	store.CloseTicket(primero, "resuelto")

	segundo := ReportarFallo(cfg, store, phone, motivo, "volvió a pasar")
	if segundo == primero {
		t.Error("el problema volvió tras cerrar el caso y no se abrió uno nuevo: quedaría invisible")
	}
}
