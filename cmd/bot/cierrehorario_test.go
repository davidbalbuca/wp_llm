// EL CIERRE NO SE MANDA DE MADRUGADA NI DOS VECES.
//
// AUDITORÍA 28/09 (skill bot-log-forensics, patrón P2): "Parece que te ocupaste" es el mensaje que
// MÁS clientes reciben justo antes de desaparecer — 33 envíos a 19 clientes, primero del ranking de
// abandono. Al medirlo salieron dos huecos concretos:
//
//	6 de 44 envíos salieron FUERA del horario 07:00–19:00, uno a las 00:00:10 a alguien
//	                        que había escrito a las 23:52
//	593995041865 lo recibió DOS VECES el 22/09 con 24 minutos de diferencia (22:57 y 23:21)
//
// Lo que los datos NO justificaron: subir el umbral de 7 minutos. Medido sobre 709 pausas reales
// del corpus, a los 7 min ya contestó el 95% de la gente (p50=0.4 min, p90=3 min, p95=8 min), y
// tras una pregunta es aún más rápido (p95=3.6 min). Mi hipótesis inicial era que 7 min era poco;
// los datos dijeron que el problema era la HORA y la REPETICIÓN, no el plazo.
package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

func cfgHorario() config.Config {
	return config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}
}

func TestElCierreRespetaElHorarioDeAtencion(t *testing.T) {
	casos := []struct {
		hora, min int
		dentro    bool
	}{
		{0, 0, false},   // el caso real: 00:00:10
		{23, 21, false}, // el otro real: 23:21
		{22, 57, false}, // y el otro: 22:57
		{6, 59, false},
		{7, 0, true}, // justo al abrir
		{12, 30, true},
		{18, 59, true}, // el último minuto
		{19, 0, false}, // justo al cerrar ya no
		{20, 0, false},
	}
	for _, c := range casos {
		ahora := time.Date(2026, 9, 28, c.hora, c.min, 0, 0, time.Local)
		if got := dentroDelHorario(ahora, cfgHorario()); got != c.dentro {
			t.Errorf("a las %02d:%02d dentroDelHorario=%v, esperado %v", c.hora, c.min, got, c.dentro)
		}
	}
}

// Un horario mal escrito no puede dejar al bot mudo: el cierre no es crítico, así que se permite.
func TestUnHorarioMalConfiguradoNoBloqueaElCierre(t *testing.T) {
	for _, c := range []config.Config{
		{BotHorarioInicio: "", BotHorarioFin: ""},
		{BotHorarioInicio: "siete", BotHorarioFin: "siete"},
		{BotHorarioInicio: "99:99", BotHorarioFin: "07:00"},
	} {
		if !dentroDelHorario(time.Now(), c) {
			t.Errorf("con horario inválido (%q-%q) no debe bloquearse el cierre",
				c.BotHorarioInicio, c.BotHorarioFin)
		}
	}
}

// NO DOS VECES EN LA MISMA SESIÓN. El caso 593995041865 del 22/09.
func TestNoSeDespideDosVecesEnLaMismaSesion(t *testing.T) {
	const from = "593995041865"
	store := conversation.NewMemStore()
	ahora := time.Now()

	// La conversación mínima que merece cierre, con una pregunta sin responder al final.
	store.LogMessage(from, "user", "hola")
	store.LogMessage(from, "system", "¿Qué necesitas?")
	store.LogMessage(from, "user", "gas")
	store.LogMessage(from, "system", "¿De qué color?")

	chat := conversation.ConversationSummary{
		Phone: from, Mode: conversation.ChatModeBot,
		LastMessage: "¿De qué color?", LastRole: "system",
		LastAt: ahora.Add(-10 * time.Minute).Unix(),
	}
	// Sin despedida previa: le toca.
	if !mereceCierre(store, chat, ahora, 7*time.Minute, time.Hour) {
		t.Fatal("la primera despedida debería salir")
	}

	// Ya se despidió hace 24 minutos (el caso real): NO se repite.
	store.LogMessage(from, "system", mensajeCierre)
	store.LogMessage(from, "user", "ah perdón, sigo aquí")
	store.LogMessage(from, "system", "¿Cuántos cilindros?")
	chat.LastMessage = "¿Cuántos cilindros?"
	if mereceCierre(store, chat, ahora, 7*time.Minute, time.Hour) {
		t.Error("se despidió dos veces en la misma sesión (caso 593995041865, 22/09)")
	}
}

// Pero una conversación NUEVA sí merece su despedida: los cierres del 15, 17 y 22-sep de ese mismo
// cliente eran legítimos. El freno es por sesión, no para siempre.
func TestUnaConversacionNuevaSiMereceSuDespedida(t *testing.T) {
	const from = "593995041866"
	store := conversation.NewMemStore()
	ahora := time.Now()
	store.LogMessage(from, "user", "hola")
	store.LogMessage(from, "system", "¿Qué necesitas?")
	store.LogMessage(from, "user", "gas")
	store.LogMessage(from, "system", "¿De qué color?")

	chat := conversation.ConversationSummary{
		Phone: from, Mode: conversation.ChatModeBot,
		LastMessage: "¿De qué color?", LastRole: "system",
		LastAt: ahora.Add(-10 * time.Minute).Unix(),
	}
	// Con ventana de 1 h, una despedida de hace 3 h no frena nada: es otra conversación.
	if seDespidioHaceRato(store, from, ahora, time.Hour) {
		t.Fatal("sin despedidas previas no debería frenar")
	}
	if !mereceCierre(store, chat, ahora, 7*time.Minute, time.Hour) {
		t.Error("una conversación nueva merece su despedida")
	}
}

// GUARD ESTRUCTURAL: que la función exista no prueba que se USE.
//
// Se comprobó borrando la llamada a dentroDelHorario de revisarCierres: los tests de arriba seguían
// en verde, porque prueban la primitiva. revisarCierres no se puede ejecutar en un test (manda
// mensajes de verdad por WhatsApp), así que se verifica sobre el fuente — igual que hace
// TestProcessWebhookTomaElLockAntesDeTocarElStore con el lock de la cola.
func TestRevisarCierresConsultaElHorario(t *testing.T) {
	src, err := os.ReadFile("cierre.go")
	if err != nil {
		t.Fatalf("no se pudo leer cierre.go: %v", err)
	}
	cuerpo := string(src)
	ini := strings.Index(cuerpo, "func revisarCierres(")
	if ini < 0 {
		t.Fatal("no se encontró revisarCierres en cierre.go")
	}
	fin := strings.Index(cuerpo[ini:], "\nfunc ")
	if fin < 0 {
		fin = len(cuerpo) - ini
	}
	if !strings.Contains(cuerpo[ini:ini+fin], "dentroDelHorario(") {
		t.Error("revisarCierres NO consulta dentroDelHorario: volverían los mensajes de madrugada")
	}
}
