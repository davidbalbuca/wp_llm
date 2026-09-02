package notify

import (
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Un Notifier apagado (sin credenciales) debe tragarse TODAS las llamadas sin entrar en pánico:
// es la garantía de que Telegram jamás rompe el bot. Si esto falla, un .env incompleto tumba
// el proceso en producción.
func TestApagadoNoRompe(t *testing.T) {
	for _, c := range []struct{ nombre, token, chat string }{
		{"sin nada", "", ""},
		{"solo token", "123:ABC", ""},
		{"solo chat", "", "-100"},
		{"solo espacios", "   ", "  "},
	} {
		n := New(c.token, c.chat, true, conversation.NewMemStore())
		if n != nil {
			t.Fatalf("%s: esperaba nil (apagado), vino un Notifier", c.nombre)
		}
		n.AvisarInicio("593999", "Ana", "hola")
		n.Fallo("593999", "Ana", "motivo", "detalle")
		n.Resumen("parte diario")
		if n.Activo() {
			t.Fatalf("%s: un notificador nil no puede estar activo", c.nombre)
		}
	}
}

// El aviso verde es UNO POR SESIÓN: si avisara por mensaje, el grupo quedaría inservible.
func TestAvisoInicioUnaVezPorSesion(t *testing.T) {
	store := conversation.NewMemStore()
	if !store.MarcarAvisoInicio("593999", time.Hour) {
		t.Fatal("el primer mensaje de la sesión SÍ debe avisar")
	}
	for i := 0; i < 5; i++ {
		if store.MarcarAvisoInicio("593999", time.Hour) {
			t.Fatalf("el mensaje %d de la misma sesión no debía volver a avisar", i+2)
		}
	}
	if !store.MarcarAvisoInicio("593999", time.Nanosecond) {
		t.Fatal("pasada la ventana, una conversación nueva debe volver a avisar")
	}
	if !store.MarcarAvisoInicio("593888", time.Hour) {
		t.Fatal("otro cliente tiene su propia sesión y debe avisar")
	}
}

// El anti-inundación deja pasar los primeros y calla el resto: si Meta se cae, los notifyOrder*
// fallan en cadena y sin esto el grupo recibe cientos de mensajes iguales.
func TestAntiInundacion(t *testing.T) {
	n := &Notifier{vistos: make(map[string]*contador)}
	pasaron := 0
	for i := 0; i < 50; i++ {
		if n.permitido("Meta caída") {
			pasaron++
		}
	}
	if pasaron != topeFallosIguales {
		t.Fatalf("pasaron %d avisos del mismo motivo; esperaba el tope de %d", pasaron, topeFallosIguales)
	}
	if !n.permitido("otro motivo distinto") {
		t.Fatal("el tope es POR MOTIVO: uno nuevo no puede quedar silenciado por otro")
	}
}
