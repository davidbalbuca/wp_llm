package main

import (
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Lo que se prueba es cuándo NO hay que despedirse. Mandar este mensaje donde no toca es peor
// que no mandarlo: le llega a alguien que está esperando su gas, o seis horas tarde.

const minutos = time.Minute

// chatDe arma una conversación con recorrido suficiente y el silencio indicado.
func chatDe(store conversation.Store, phone string, silencio time.Duration) conversation.ConversationSummary {
	store.LogMessage(phone, "user", "Hola, me ayudas con el servicio de gas")
	store.LogMessage(phone, "model", "¡Hola! ¿Necesitas hacer un pedido?")
	store.LogMessage(phone, "user", "Blanco, 2")
	store.LogMessage(phone, "model", "¿Cuál es tu número de cédula?")
	return conversation.ConversationSummary{
		Phone:       phone,
		Mode:        conversation.ChatModeBot,
		LastMessage: "¿Cuál es tu número de cédula?",
		LastRole:    "model",
		LastAt:      time.Now().Add(-silencio).Unix(),
	}
}

func TestSeDespideCuandoElClienteSeQuedoCallado(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 8*minutos)
	if !mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("una conversación a medias con 8 minutos de silencio debía cerrarse")
	}
}

func TestNoSeDespideAntesDeTiempo(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 3*minutos)
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("a los 3 minutos el cliente todavía puede estar escribiendo")
	}
}

func TestNoSeDespideDeChatsViejos(t *testing.T) {
	// El caso del deploy: el bot arranca y ve callados todos los chats del día. Si no hubiera
	// techo, saldría a despedirse de todos de golpe, horas después de la última conversación.
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 5*time.Hour)
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("cinco horas después ya no se dice nada: llega molesto y fuera de lugar")
	}
}

func TestNoSeDespideSiQuedoUnMensajeDelClienteSinResponder(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 8*minutos)
	chat.LastRole = "user"
	chat.LastMessage = "0104426879"
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("si el último mensaje es del cliente hay que contestarle, no despedirse")
	}
}

func TestNoSeDespideDosVeces(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 20*minutos)
	chat.LastMessage = mensajeCierre
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("ya se despidió; no puede insistir cada minuto")
	}
}

func TestNoSeDespideEnMedioDeOtroFlujo(t *testing.T) {
	store := conversation.NewMemStore()
	base := chatDe(store, "593984187615", 8*minutos)

	esperando := base
	esperando.EnEspera = true
	if mereceCierre(store, esperando, time.Now(), 7*minutos, 60*minutos) {
		t.Error("está esperando repartidor: el sistema ya le va a escribir")
	}

	agendado := base
	agendado.Programado = true
	if mereceCierre(store, agendado, time.Now(), 7*minutos, 60*minutos) {
		t.Error("tiene una entrega agendada: ese aviso llega a su hora")
	}

	humano := base
	humano.Mode = conversation.ChatModeHuman
	if mereceCierre(store, humano, time.Now(), 7*minutos, 60*minutos) {
		t.Error("lo tomó una persona: el bot no se mete")
	}

	conPedido := base
	store.SetActivePedido("593984187615", 213)
	if mereceCierre(store, conPedido, time.Now(), 7*minutos, 60*minutos) {
		t.Error("tiene un pedido en curso: el chat sigue vivo")
	}
}

func TestNoSeDespideSiElBotYaCerroLaConversacion(t *testing.T) {
	// El caso de Juan Solano (28/08): pidió un color que no existe, el bot terminó la
	// conversación, y siete minutos después le soltó "parece que te ocupaste" encima de un chat
	// que ya estaba cerrado. Si el bot no dejó ninguna pregunta abierta, no hay nada que retomar.
	store := conversation.NewMemStore()
	chat := chatDe(store, "593983709153", 8*minutos)

	for _, cierre := range []string{
		"Perfecto, Juan. Voy a avisar al dueño sobre tu solicitud del color verde.",
		"¡Listo! Gracias por tu confianza. ¡Hasta pronto! 👋",
		"Entiendo. Lamentablemente el verde no está disponible. Solo tenemos Blanco, Amarillo, Naranja y Azul.",
	} {
		chat.LastMessage = cierre
		if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
			t.Errorf("no quedó ninguna pregunta abierta, no había que despedirse: %q", cierre)
		}
	}

	// Con una pregunta sin responder sí corresponde.
	chat.LastMessage = "¿Cuál es tu número de cédula?"
	if !mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Error("quedó una pregunta sin responder: ahí sí se cierra")
	}
}

func TestNoSeDespideDeQuienApenasSaludo(t *testing.T) {
	store := conversation.NewMemStore()
	store.LogMessage("593984187615", "user", "Hola")
	store.LogMessage("593984187615", "model", "¡Hola! ¿En qué te ayudo?")
	chat := conversation.ConversationSummary{
		Phone:       "593984187615",
		Mode:        conversation.ChatModeBot,
		LastMessage: "¡Hola! ¿En qué te ayudo?",
		LastRole:    "model",
		LastAt:      time.Now().Add(-8 * minutos).Unix(),
	}
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("escribió 'hola' y se fue: despedirse de eso no tiene sentido")
	}
}
