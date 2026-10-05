package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// La invitación a la app sale en la primera entrega y después como mucho una vez cada 30 días.
func TestInvitarAppUnaVezCadaTreintaDias(t *testing.T) {
	const from = "593900800001"
	store := conversation.NewMemStore()
	ahora := time.Date(2026, 10, 5, 12, 0, 0, 0, zonaEcuador)

	if !TocaInvitarApp(store, from, ahora) {
		t.Fatal("en la primera entrega se le invita a la app")
	}
	store.MarcarInvitacionApp(from, ahora)
	if TocaInvitarApp(store, from, ahora.Add(7*24*time.Hour)) {
		t.Error("a la semana no se le repite: sería publicidad en cada entrega")
	}
	if !TocaInvitarApp(store, from, ahora.Add(30*24*time.Hour)) {
		t.Error("a los 30 días sí se le vuelve a invitar")
	}
}

func TestMensajeInvitarAppLlevaElLink(t *testing.T) {
	if !strings.Contains(MensajeInvitarApp(), "https://ubi.ec") {
		t.Errorf("la invitación no lleva el link de descarga: %q", MensajeInvitarApp())
	}
}
