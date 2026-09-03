package notify

import (
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// El camino único NO puede depender de que Telegram esté configurado: aunque Default sea nil (sin
// credenciales), el ticket TIENE que crearse igual. Esto es lo que garantiza que un .env sin
// Telegram no se trague los tickets — el bug que motivó la unificación fue justo lo contrario:
// sitios que reportaban por unos canales y no por otros.
func TestReportarFalloCreaTicketSinTelegram(t *testing.T) {
	Default = nil // sin Telegram configurado
	store := conversation.NewMemStore()
	cfg := config.Config{} // sin SMTP: el correo se degrada solo, el ticket no

	id := ReportarFallo(cfg, store, "593999", "Motivo de prueba", "detalle")
	if id == 0 {
		t.Fatal("el ticket debe crearse aunque Telegram y SMTP estén apagados")
	}

	// El ticket quedó en el panel...
	tickets := store.ListTickets(conversation.TicketAbierto, 10)
	if len(tickets) != 1 || tickets[0].Motivo != "Motivo de prueba" {
		t.Fatalf("el ticket no quedó registrado: %+v", tickets)
	}
	// ...y con una marca en el chat, para verlo al abrir la conversación.
	var marca bool
	for _, m := range store.GetConversation("593999", 10) {
		if m.Role == "system" && len(m.Content) > 0 {
			marca = true
		}
	}
	if !marca {
		t.Fatal("falta la marca 🎫 en el chat")
	}
}

// Sin teléfono no hay chat al que asociar el ticket, pero la función no debe romper: solo se
// salta el ticket y sigue (útil para fallos de proceso sin cliente).
func TestReportarFalloSinTelefono(t *testing.T) {
	Default = nil
	store := conversation.NewMemStore()
	if id := ReportarFallo(config.Config{}, store, "", "Fallo del scheduler", "detalle"); id != 0 {
		t.Fatalf("sin teléfono no debería crear ticket, devolvió id=%d", id)
	}
}
