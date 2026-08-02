// Command bot es el punto de entrada del agente de WhatsApp para gas a domicilio.
// Solo cablea dependencias, expone los endpoints HTTP y arranca el servidor; la lógica
// vive en los paquetes internal/.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"wp-llm-gas/internal/agent"
	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/escalation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/whatsapp"
)

// catalogTTL es cuánto se cachea el contexto del negocio traído del backend antes de
// refrescarlo. Balance entre reflejar cambios pronto y no consultar en cada mensaje.
const catalogTTL = 5 * time.Minute

func main() {
	_ = godotenv.Load() // carga .env si existe
	cfg := config.Load()

	// Selección del almacén de estado: SQLite si hay DB_PATH, memoria en caso contrario.
	var store conversation.Store
	if cfg.DBPath != "" {
		ss, err := conversation.NewSQLiteStore(cfg.DBPath)
		if err != nil {
			log.Fatalf("No se pudo abrir SQLite (%s): %v", cfg.DBPath, err)
		}
		store = ss
		log.Printf("Historial: SQLite (%s)", cfg.DBPath)
	} else {
		store = conversation.NewMemStore()
		log.Printf("Historial: en memoria (sin DB_PATH; se pierde al reiniciar)")
	}

	// Cliente de la API georoutes del backend (mismo flujo que la app móvil), compartido
	// por el catálogo (lectura) y el agente (registro de pedidos).
	grClient := georoutes.NewClient(cfg.BackendURL)
	// Cuenta de servicio para leer el catálogo con JWT en prod (DEBUG=False). Si no está
	// configurada, el catálogo se pide sin token (DEV con DEBUG=True).
	grClient.SetServiceAccount(cfg.CatalogUser, cfg.CatalogPassword)

	// Cliente del catálogo dinámico del negocio (productos con colores/precios y formas de pago).
	catalogClient := catalog.NewClient(grClient, catalogTTL)

	ag, err := agent.New(context.Background(), cfg, store, catalogClient, grClient)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Verificación del webhook (handshake de Meta).
	mux.HandleFunc("GET /webhook", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if ch, ok := whatsapp.VerifyWebhook(cfg, q.Get("hub.mode"), q.Get("hub.verify_token"), q.Get("hub.challenge")); ok {
			_, _ = w.Write([]byte(ch))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})

	// Recepción de mensajes: responder 200 ya y procesar en una goroutine
	// (Meta reintenta si el webhook tarda).
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		go processWebhook(cfg, ag, store, body)
	})

	// Notificación INTERNA del backend: un conductor finalizó un pedido. El bot le manda al
	// cliente un agradecimiento y le pide calificar al repartidor. Protegido por secreto
	// compartido (BACKEND_CHANNEL_SECRET) para que solo el backend pueda dispararlo.
	mux.HandleFunc("POST /internal/order-finished", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			PedidoID  int    `json:"pedido_id"`
			Telefono  string `json:"telefono"`
			Conductor string `json:"conductor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.PedidoID <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		go notifyOrderFinished(cfg, store, payload.PedidoID, payload.Telefono, payload.Conductor)
	})

	log.Printf("Servidor escuchando en http://localhost:%s", cfg.Port)
	log.Printf("Modelo Gemini: %s", cfg.GeminiModel)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}

func processWebhook(cfg config.Config, ag *agent.Agent, store conversation.Store, body []byte) {
	log.Printf("[webhook] payload recibido: %s", string(body))

	inc, ok := whatsapp.ParseIncoming(body)
	if !ok {
		log.Printf("[webhook] sin mensaje (probable evento de estado); se ignora.")
		return
	}

	// --- Delimitación de la conversación por SESIÓN (no por número de turnos) ---
	// Si pasó más de SessionGap desde el último mensaje del cliente, esto es una
	// conversación NUEVA: limpiamos el historial anterior para arrancar fresco, sin
	// arrastrar un pedido a medias ni depender de contar turnos. Un pedido en curso
	// (draft OTP) también se descarta por inactividad.
	if last, ok := store.LastActivity(inc.From); ok && time.Since(last) > conversation.SessionGap {
		log.Printf("[webhook] nueva sesión para %s (inactivo %s); se limpia historial", inc.From, time.Since(last).Round(time.Minute))
		store.ClearHistory(inc.From)
		store.ClearOrderDraft(inc.From)
		store.ClearPendingVerification(inc.From)
	}
	store.TouchActivity(inc.From)

	// Determina el texto a procesar por la IA.
	messageForAgent := inc.Text

	// Mensaje de ubicación: lo guardamos y avisamos a la IA (en nombre del cliente)
	// que ya compartió su ubicación, para que pueda continuar/cerrar el pedido.
	if inc.HasLocation {
		store.SetLocation(inc.From, inc.Latitude, inc.Longitude)
		log.Printf("[webhook] ubicación de %s: %f, %f", inc.From, inc.Latitude, inc.Longitude)
		messageForAgent = "He compartido mi ubicación actual."
	} else if inc.IsText {
		// Muchos clientes pegan la ubicación como enlace de Google Maps o texto con
		// coordenadas en vez de usar el adjunto nativo de WhatsApp. Si el texto contiene
		// un par de coordenadas plausible, lo tratamos igual que una ubicación.
		if lat, lng, ok := whatsapp.ParseCoordsFromText(inc.Text); ok {
			store.SetLocation(inc.From, lat, lng)
			log.Printf("[webhook] ubicación (de texto) de %s: %f, %f", inc.From, lat, lng)
			messageForAgent = "He compartido mi ubicación actual."
		} else {
			log.Printf("[webhook] mensaje de %s: %q", inc.From, inc.Text)
		}
	}

	// Mensaje no-texto y sin ubicación (imagen, audio, etc.): pedimos texto.
	if messageForAgent == "" {
		_ = whatsapp.SendText(cfg, inc.From, "Por ahora solo puedo leer mensajes de texto y ubicaciones. Por favor, escribe tu consulta. 🙂")
		return
	}

	// Verificación OTP pendiente: si el cliente tiene una verificación en curso y
	// envía un texto corto (el código), lo tratamos como código de verificación.
	if _, pending := store.GetPendingVerification(inc.From); pending && inc.IsText {
		codigo := strings.TrimSpace(inc.Text)
		reply, retomar := ag.HandleVerification(inc.From, codigo)
		if retomar {
			// Código válido y hay un pedido en pausa: lo retomamos automáticamente
			// (la IA concreta el pedido y redacta la confirmación).
			log.Printf("[webhook] OTP validado para %s; retomando pedido en pausa", inc.From)
			resumeReply, err := ag.ResumeOrder(context.Background(), inc.From)
			if err != nil {
				log.Printf("[server] Error retomando pedido de %s: %v", inc.From, err)
				resumeReply = "¡Tu cuenta ya está verificada ✅! Cuéntame, ¿qué cilindro necesitas y cuántos?"
			}
			_ = whatsapp.SendText(cfg, inc.From, resumeReply)
			if ag.DidEscalate() {
				store.ClearHistory(inc.From)
				store.ClearPendingVerification(inc.From)
				ag.ClearEscalated()
			}
			return
		}
		if reply != "" {
			log.Printf("[webhook] código OTP procesado para %s", inc.From)
			log.Printf("[webhook] respuesta: %q", reply)
			_ = whatsapp.SendText(cfg, inc.From, reply)
			return
		}
	}

	reply, err := ag.HandleMessage(context.Background(), inc.From, messageForAgent)
	if err != nil {
		log.Printf("[server] Error procesando mensaje: %v", err)
		_ = whatsapp.SendText(cfg, inc.From, "Disculpa, tuvimos un inconveniente técnico. Ya avisé a nuestro equipo para que te contacte.")
		escalation.NotifyOwner(cfg, inc.From, "Error técnico del agente", "El cliente envió: \""+messageForAgent+"\". La IA falló al responder.")
		return
	}

	// Si la IA ya envió un MENÚ interactivo (botones/lista) en este turno, ese es el mensaje;
	// no mandamos además el texto de respuesta (evita duplicar la pregunta).
	if ag.MenuSent() {
		log.Printf("[webhook] menú interactivo enviado a %s ✔", inc.From)
		ag.ClearMenuSent()
		return
	}

	log.Printf("[webhook] respuesta de la IA: %q", reply)
	if err := whatsapp.SendText(cfg, inc.From, reply); err != nil {
		log.Printf("[server] Error enviando a %s: %v", inc.From, err)
		return
	}
	if ag.DidEscalate() {
		log.Printf("[webhook] escalation detectada, limpiando historial para %s", inc.From)
		store.ClearHistory(inc.From)
		store.ClearPendingVerification(inc.From)
		ag.ClearEscalated()
	}
	log.Printf("[webhook] respuesta enviada a %s ✔", inc.From)
}

// notifyOrderFinished maneja el aviso del backend de que un pedido se entregó: le manda al
// cliente un agradecimiento y le pide calificar al repartidor, y deja el pedido marcado como
// "pendiente de calificar" para que la próxima respuesta (1-5) la registre el agente.
// Usa el teléfono de WhatsApp con el que se hizo el pedido (más fiable que el Cliente.telefono
// del backend); si el bot no lo tiene (p. ej. reinició sin SQLite), cae al que envió el backend.
func notifyOrderFinished(cfg config.Config, store conversation.Store, pedidoID int, telefono, conductor string) {
	phone := telefono
	if p, ok := store.GetOrderPhone(pedidoID); ok && p != "" {
		phone = p
	}
	if phone == "" {
		log.Printf("[order-finished] pedido %d sin teléfono de contacto; se ignora", pedidoID)
		return
	}

	store.SetPendingRating(phone, conversation.PendingRating{PedidoID: pedidoID, Conductor: conductor})

	msg := "¡Tu pedido fue entregado! 🎉 Gracias por preferirnos. 🙌\n\n"
	if conductor != "" {
		msg += fmt.Sprintf("¿Cómo calificarías a tu repartidor %s? Responde con un número del 1 al 5 ⭐ "+
			"(y si quieres, un breve comentario).", conductor)
	} else {
		msg += "¿Cómo calificarías a tu repartidor? Responde con un número del 1 al 5 ⭐ (y si quieres, un breve comentario)."
	}
	if err := whatsapp.SendText(cfg, phone, msg); err != nil {
		log.Printf("[order-finished] error enviando a %s: %v", phone, err)
	}
}
