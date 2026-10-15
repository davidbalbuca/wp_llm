package main

import (
	"strings"
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

// ── DOS PASOS (06/10): recordatorio según dónde se quedó y, si no contesta, la despedida ──

func TestQueLeFaltaSegunLaUltimaPregunta(t *testing.T) {
	casos := map[string]string{
		"📋 Cuéntame, ¿de qué color es tu cilindro? 👇 Así te busco a quien lo tenga más cerca [BLANCO / AMARILLO / NARANJA / AZUL]": faltaColor,
		"📋 ¡Perfecto! ¿Cuántos cilindros de 15kg Azul necesitas? [1 / 2 / 3 / Más de 3]":                                           faltaCantidad,
		"¡Listo, 2 cilindros blancos! 🙌 ¿Me ayudas con tu ubicación? Así el repartidor más cercano llega directo a tu casa 😊":      faltaUbicacion,
		"¿Me ayudas con tu nombre para que el repartidor te pueda ubicar en la entrega?":                                           faltaNombre,
		"Atendemos de 07:00 a 20:30 ¿A qué hora te gustaría que te llevemos tus 2 cilindros amarillos mañana? 😊":                   faltaHora,
		"¡Hola! ¿En qué te ayudo?": faltaOtra,
	}
	for texto, want := range casos {
		if got := queLeFalta(texto); got != want {
			t.Errorf("queLeFalta(%q) = %s; quería %s", texto, got, want)
		}
	}
}

func TestElRecordatorioDiceLoQueFalta(t *testing.T) {
	if got, want := textoRecordatorio("Ana", faltaUbicacion, ""),
		"¿Sigues por ahí, Ana? 😊 Solo me falta tu ubicación para buscarte al repartidor más cercano. "+
			"Cuando puedas, tocas el 📎 → *Ubicación* y listo."; got != want {
		t.Errorf("ubicación\ngot  %q\nwant %q", got, want)
	}
	if got := textoRecordatorio("", faltaCantidad, "AMARILLO"); got != "¿Sigues por ahí? 😊 ¿Cuántos cilindros de amarillo te mando? Con el número me basta." {
		t.Errorf("cantidad: %q", got)
	}
	if got := textoRecordatorio("Ana", faltaColor, ""); !strings.Contains(got, "de qué color es tu cilindro") {
		t.Errorf("color: %q", got)
	}
}

func TestLaDespedidaNoEsUnaPresentacion(t *testing.T) {
	got := textoDespedida("Ana")
	if got != "Te dejo por ahora, Ana 😊 Cuando necesites tu gas, escríbeme nomás: ¡*UbiGas* está aquísito no más! 🔥🚚" {
		t.Errorf("despedida: %q", got)
	}
	// La marca del saludo (saludounico.go) no puede estar: haría creer que el bot ya se presentó.
	if strings.Contains(got, "*UbiGas*, tu repartidor aquísito") {
		t.Errorf("la despedida no puede llevar la marca de la presentación: %q", got)
	}
}

func TestAlQueVinoDelAnuncioYNoEligioColorSeLeRecuerda(t *testing.T) {
	// "Deseo pedir GAS 😄" -> saludo -> menú de colores -> silencio. Tres mensajes.
	store := conversation.NewMemStore()
	const tel = "593984000099"
	store.LogMessage(tel, "user", "Deseo pedir GAS 😄")
	store.LogMessage(tel, "system", "¡Buenos días! 👋 ¿Se te acabó el gas? …")
	menu := "📋 Cuéntame, ¿de qué color es tu cilindro? 👇 [BLANCO / AMARILLO / NARANJA / AZUL]"
	store.LogMessage(tel, "model", menu)
	chat := conversation.ConversationSummary{Phone: tel, Mode: conversation.ChatModeBot, LastMessage: menu,
		LastRole: "model", LastAt: time.Now().Add(-8 * minutos).Unix()}
	if !mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Fatal("se quedó en el color: le toca el recordatorio")
	}
}

func TestDespuesDelRecordatorioLaDespedidaYNadaMas(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 8*minutos)
	recordatorio := textoRecordatorio("Ana", faltaUbicacion, "")
	store.LogMessage(chat.Phone, "system", recordatorio)
	chat.LastMessage, chat.LastRole = recordatorio, "system"

	chat.LastAt = time.Now().Add(-5 * minutos).Unix()
	if mereceDespedida(store, chat, time.Now(), 15*minutos, 60*minutos) {
		t.Error("todavía no: hay que darle tiempo de contestar el recordatorio")
	}
	if mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Error("no se manda un segundo recordatorio")
	}
	chat.LastAt = time.Now().Add(-16 * minutos).Unix()
	if !mereceDespedida(store, chat, time.Now(), 15*minutos, 60*minutos) {
		t.Error("no contestó el recordatorio: ahora sí la despedida")
	}

	// Y después de la despedida, silencio.
	despedida := textoDespedida("Ana")
	store.LogMessage(chat.Phone, "system", despedida)
	chat.LastMessage = despedida
	chat.LastAt = time.Now().Add(-20 * minutos).Unix()
	if mereceDespedida(store, chat, time.Now(), 15*minutos, 60*minutos) || mereceCierre(store, chat, time.Now(), 7*minutos, 60*minutos) {
		t.Error("ya se despidió: no se le escribe más")
	}
}

func TestSiContestaElRecordatorioNoHayDespedida(t *testing.T) {
	store := conversation.NewMemStore()
	chat := chatDe(store, "593984187615", 20*minutos)
	chat.LastMessage, chat.LastRole = "📍 ubicación: -2.9, -79.0", "user"
	if mereceDespedida(store, chat, time.Now(), 15*minutos, 60*minutos) {
		t.Error("contestó: se sigue con su pedido, no se despide")
	}
}

func TestPrimerNombreBonito(t *testing.T) {
	for in, want := range map[string]string{"MARÍA JOSÉ PEREZ": "María", "ana": "Ana", "": ""} {
		if got := primerNombreBonito(in); got != want {
			t.Errorf("primerNombreBonito(%q) = %q; quería %q", in, got, want)
		}
	}
}
