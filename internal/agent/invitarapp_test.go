package agent

import (
	"strings"
	"testing"
)

func TestMensajeInvitarAppLlevaElLink(t *testing.T) {
	if !strings.Contains(MensajeInvitarApp(), "https://ubi.ec/app.html") {
		t.Errorf("la invitación no lleva el link de descarga: %q", MensajeInvitarApp())
	}
}

// La invitación da una razón para descargarla, no solo el link.
func TestMensajeInvitarAppDaUnValor(t *testing.T) {
	if !strings.Contains(MensajeInvitarApp(), "consumo") {
		t.Errorf("la invitación no dice qué gana con la app: %q", MensajeInvitarApp())
	}
}

// "Vender la app por todo lado, pero con texto no tan cargado" (dueño, 12/10): cada invitación es
// UNA línea.
func TestInvitacionesAppSonUnaLinea(t *testing.T) {
	for _, linea := range []string{LineaApp(), LineaAppEnCamino(), MensajeInvitarApp()} {
		if strings.Contains(linea, "\n") || len([]rune(linea)) > 110 {
			t.Errorf("la invitación es larga o tiene más de una línea: %q", linea)
		}
	}
}

// Con el pedido en camino se le invita a seguirlo en vivo en la app.
func TestConInvitacionAppAgregaLaLineaUnaVez(t *testing.T) {
	msg := ConInvitacionApp("🎉 ¡Listo! Tu pedido está confirmado.\n\nYa va en camino.\n")
	if !strings.HasSuffix(msg, LineaAppEnCamino()) {
		t.Fatalf("no se agregó la invitación al final: %q", msg)
	}
	if !strings.Contains(msg, "camino.\n\n📲") {
		t.Errorf("la invitación no va separada por una línea en blanco: %q", msg)
	}
	if otra := ConInvitacionApp(msg); otra != msg {
		t.Errorf("se repitió la invitación en el mismo mensaje: %q", otra)
	}
}

func TestMensajeReasignadoConInvitacion(t *testing.T) {
	msg := ConInvitacionApp(MensajeReasignado("ÁNGEL VEGA", "ABC-123", 8, ""))
	if strings.Count(msg, LinkApp) != 1 || !strings.Contains(msg, "ÁNGEL VEGA") {
		t.Errorf("el aviso de nuevo repartidor no invita a la app una vez: %q", msg)
	}
}
