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
	"sync"
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

// --- Anti-duplicados de mensajes ---
// Ignora un mensaje de texto IDÉNTICO del mismo teléfono dentro de una ventana corta (el cliente
// que manda "Hola" dos veces, o un reintento del webhook de Meta), para no responder dos veces.
type dedupEntry struct {
	text string
	at   time.Time
}

type msgDedup struct {
	mu   sync.Mutex
	last map[string]dedupEntry
}

var dedup = &msgDedup{last: map[string]dedupEntry{}}

const dedupWindow = 6 * time.Second

func (d *msgDedup) isDuplicate(phone, text string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	prev, ok := d.last[phone]
	if ok && prev.text == text && now.Sub(prev.at) < dedupWindow {
		return true
	}
	d.last[phone] = dedupEntry{text: text, at: now}
	return false
}

// looksLikeLeakedJSON detecta si la respuesta de la IA es una estructura cruda (JSON/acción) que
// NUNCA debe llegarle al cliente. Ninguna respuesta normal del bot empieza con { [ o ```.
func looksLikeLeakedJSON(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "```") {
		return true
	}
	low := strings.ToLower(t)
	if strings.Contains(low, `"action"`) || strings.Contains(low, "action_input") {
		return true
	}
	if strings.Contains(low, `"opciones"`) && strings.Contains(low, `"cuerpo"`) {
		return true
	}
	return false
}

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

	// Notificación INTERNA del backend: el conductor LLEGÓ a la ubicación (pulsó "Sí, llegué").
	// El bot le avisa al cliente por WhatsApp que salga a recibir el pedido. Mismo secreto
	// compartido que /internal/order-finished.
	mux.HandleFunc("POST /internal/order-arrived", func(w http.ResponseWriter, r *http.Request) {
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
		go notifyOrderArrived(cfg, store, payload.PedidoID, payload.Telefono)
	})

	// Notificación INTERNA del backend: el conductor CANCELÓ el pedido. El bot le avisa al
	// cliente por WhatsApp. Mismo secreto compartido.
	mux.HandleFunc("POST /internal/order-cancelled", func(w http.ResponseWriter, r *http.Request) {
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
		go notifyOrderCancelled(cfg, store, payload.PedidoID, payload.Telefono)
	})

	mux.HandleFunc("POST /internal/order-reassigned", func(w http.ResponseWriter, r *http.Request) {
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
		go notifyOrderReassigned(cfg, store, payload.PedidoID, payload.Telefono, payload.Conductor)
	})

	log.Printf("Servidor escuchando en http://localhost:%s", cfg.Port)
	log.Printf("Modelo Gemini: %s", cfg.GeminiModel)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}

func processWebhook(cfg config.Config, ag *agent.Agent, store conversation.Store, body []byte) {
	// Blindaje: un panic inesperado NO debe tumbar el proceso; si sabemos el teléfono, le avisamos
	// al cliente con un mensaje amable (nunca un error crudo).
	var recoverPhone string
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[webhook] PANIC recuperado: %v", r)
			if recoverPhone != "" {
				_ = whatsapp.SendText(cfg, recoverPhone, "Disculpa, estamos con un problema técnico. Por favor intenta de nuevo en un momento. 🙏")
			}
		}
	}()

	log.Printf("[webhook] payload recibido: %s", string(body))

	inc, ok := whatsapp.ParseIncoming(body)
	if !ok {
		log.Printf("[webhook] sin mensaje (probable evento de estado); se ignora.")
		return
	}
	recoverPhone = inc.From

	// Anti-duplicados: si el cliente manda el MISMO texto dos veces seguidas (doble-tap) o Meta
	// reintenta el webhook, respondemos una sola vez.
	if inc.IsText && dedup.isDuplicate(inc.From, strings.TrimSpace(inc.Text)) {
		log.Printf("[webhook] mensaje duplicado de %s ignorado: %q", inc.From, inc.Text)
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
	// Blindaje final: el cliente NUNCA debe ver JSON crudo ni algo técnico. Si la IA filtró una
	// estructura (acción/menú mal formado) que no se convirtió en menú, la cambiamos por un
	// mensaje amable en vez de mostrarle el texto crudo.
	if looksLikeLeakedJSON(reply) {
		log.Printf("[webhook] fuga de JSON descartada para %s: %.150q", inc.From, reply)
		reply = "Disculpa, tuve un pequeño inconveniente 🙈. ¿Me repites qué necesitas, por favor?"
	}
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

// notifyOrderArrived avisa al cliente por WhatsApp que el conductor LLEGÓ y salga a recibir.
// Usa el número real del pedido (order_phone) o el que mande el backend.
func notifyOrderArrived(cfg config.Config, store conversation.Store, pedidoID int, telefono string) {
	phone := telefono
	if p, ok := store.GetOrderPhone(pedidoID); ok && p != "" {
		phone = p
	}
	if phone == "" {
		log.Printf("[order-arrived] pedido %d sin teléfono de contacto; se ignora", pedidoID)
		return
	}
	msg := "🛵 El conductor llegó a tu ubicación. Por favor, sal a recibir tu pedido. 📦"
	if err := whatsapp.SendText(cfg, phone, msg); err != nil {
		log.Printf("[order-arrived] error enviando a %s: %v", phone, err)
	}
}

// notifyOrderCancelled avisa al cliente por WhatsApp que el conductor CANCELÓ su pedido. Limpia el
// historial para que la próxima conversación arranque fresca (sin arrastrar el pedido cancelado).
func notifyOrderCancelled(cfg config.Config, store conversation.Store, pedidoID int, telefono string) {
	phone := telefono
	if p, ok := store.GetOrderPhone(pedidoID); ok && p != "" {
		phone = p
	}
	if phone == "" {
		log.Printf("[order-cancelled] pedido %d sin teléfono de contacto; se ignora", pedidoID)
		return
	}
	store.ClearActivePedido(phone)
	store.ClearHistory(phone)
	msg := "😔 Tu pedido fue cancelado por el conductor. Disculpa las molestias. Cuando quieras, puedes hacer un nuevo pedido."
	if err := whatsapp.SendText(cfg, phone, msg); err != nil {
		log.Printf("[order-cancelled] error enviando a %s: %v", phone, err)
	}
}

// notifyOrderReassigned avisa al cliente por WhatsApp que su pedido fue REASIGNADO a un nuevo
// repartidor (el anterior canceló). El pedido sigue vivo, así que NO se limpia el historial.
func notifyOrderReassigned(cfg config.Config, store conversation.Store, pedidoID int, telefono, conductor string) {
	phone := telefono
	if p, ok := store.GetOrderPhone(pedidoID); ok && p != "" {
		phone = p
	}
	if phone == "" {
		log.Printf("[order-reassigned] pedido %d sin teléfono de contacto; se ignora", pedidoID)
		return
	}
	msg := "🚚 Tu pedido fue asignado a un nuevo repartidor"
	if conductor != "" {
		msg += ": " + conductor
	}
	msg += ". ¡Ya va en camino!"
	if err := whatsapp.SendText(cfg, phone, msg); err != nil {
		log.Printf("[order-reassigned] error enviando a %s: %v", phone, err)
	}
}
