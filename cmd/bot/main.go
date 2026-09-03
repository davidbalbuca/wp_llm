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
	"strconv"
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
	"wp-llm-gas/internal/notify"
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

// replyClient envía un mensaje al cliente y lo registra en la auditoría (message_log), para que
// quede en el historial durable de la conversación.
func replyClient(cfg config.Config, store conversation.Store, phone, text string) error {
	store.LogMessage(phone, "model", text)
	return whatsapp.SendText(cfg, phone, text)
}

// avisarCliente manda un mensaje que NO nace de una respuesta del cliente -la confirmacion de
// una entrega agendada, el conductor que llego, el pedido cancelado- y lo deja en DOS sitios:
// la auditoria (lo que se ve en el panel) y la memoria del modelo.
//
// Lo segundo es lo que faltaba, y costo caro. Estos avisos se guardaban solo con LogMessage,
// que alimenta el panel pero NO el historial que lee la IA. Por eso el 27/08 el sistema le
// pregunto a un cliente "¿Confirmas tu pedido?" a las 11:30, el contesto "Si" a las 11:37, y
// para el modelo la conversacion era: bot "¡Que disfrutes tu gas! 😊" -> cliente "Si". Un si a
// nada. Respondio "¿Necesitas algo mas?" sin registrar el pedido, y visto lo que veia, tenia
// razon: nadie le habia preguntado nada. Si el bot le habla al cliente, el modelo se entera.
//
// Primero se envia y solo despues se registra: nunca se le hace creer al modelo que dijo algo
// que el cliente jamas recibio.
func avisarCliente(cfg config.Config, store conversation.Store, phone, texto string) error {
	if err := whatsapp.SendText(cfg, phone, texto); err != nil {
		return err
	}
	store.LogMessage(phone, "system", texto)
	store.AppendModel(phone, texto)
	return nil
}

// avisarClienteMenu manda un aviso con BOTONES. Para una confirmacion es mejor que el texto
// libre: lo que vuelve es el titulo exacto del boton, no un "si" escrito de veinte maneras que
// haya que interpretar. Si el menu falla por lo que sea, se cae a texto plano: el aviso tiene
// que salir igual.
func avisarClienteMenu(cfg config.Config, store conversation.Store, phone, cuerpo string, opciones []string, respaldo string) error {
	if err := whatsapp.SendMenu(cfg, phone, cuerpo, opciones); err != nil {
		log.Printf("[aviso] el menú falló para %s (%v); se envía como texto", phone, err)
		return avisarCliente(cfg, store, phone, respaldo)
	}
	registro := cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	store.LogMessage(phone, "system", "📋 "+registro)
	store.AppendModel(phone, registro)
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// avisos es el notificador de Telegram del proceso. Global porque los fallos que hay que reportar
// nacen en sitios muy repartidos (handlers, goroutines del scheduler, la espera de conductor) y
// enhebrarlo por firma hasta cada uno obligaría a tocar código que no tiene nada que ver con
// esto. Se asigna una sola vez en main, antes de que exista cualquier goroutine, y es nil cuando
// Telegram no está configurado: los métodos sobre un *Notifier nil no hacen nada.
var avisos *notify.Notifier

// reportarFallo deja constancia de un error que el CLIENTE no puede ver y que nadie miraría en el
// log: ticket en el panel, correo al equipo y aviso a Telegram. Es el camino que ya usaba el
// fallo de la IA (el único instrumentado hasta ahora), extraído para poder usarlo en los demás.
//
// Todo lo de aquí es best-effort: si el SMTP no está configurado o Telegram falla, el ticket ya
// quedó guardado y el chat del cliente sigue su curso. Nunca devuelve error a propósito — quien
// llama está en un camino donde ya no hay nada que decidir.
func reportarFallo(cfg config.Config, store conversation.Store, phone, motivo, detalle string) {
	log.Printf("[fallo] %s (%s): %s", motivo, phone, detalle)
	var tid int64
	if phone != "" {
		tid = store.CreateTicket(phone, motivo, detalle)
		if tid > 0 {
			// Queda en la conversación: al abrir el chat en el panel se ve DÓNDE se rompió.
			store.LogMessage(phone, "system", fmt.Sprintf("🎫 Ticket #%d — %s", tid, motivo))
		}
	}
	go escalation.SendSupportEmail(cfg, tid, phone, motivo, detalle)
	avisos.Fallo(phone, nombreDe(store, phone), motivo, detalle)
}

// nombreDe busca el nombre del cliente para que el aviso diga quién es y no solo un número.
// Devuelve "" si todavía no lo conocemos (cliente nuevo), y el aviso se arregla sin él.
func nombreDe(store conversation.Store, phone string) string {
	if phone == "" {
		return ""
	}
	if p, ok := store.GetProfile(phone); ok {
		return strings.TrimSpace(p.Nombres)
	}
	return ""
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func main() {
	_ = godotenv.Load() // carga .env si existe
	cfg := config.Load()

	// Selección del almacén de estado: SQLite si hay DB_PATH, memoria en caso contrario.
	var store conversation.Store
	if cfg.DBPath != "" {
		ss, err := conversation.NewSQLiteStore(cfg.DBPath, cfg.AuditLogDays)
		if err != nil {
			log.Fatalf("No se pudo abrir SQLite (%s): %v", cfg.DBPath, err)
		}
		store = ss
		log.Printf("Historial: SQLite (%s)", cfg.DBPath)
	} else {
		store = conversation.NewMemStore()
		log.Printf("Historial: en memoria (sin DB_PATH; se pierde al reiniciar)")
	}

	// Avisos de operación a Telegram (test productivo). Queda en nil si no está configurado y
	// el bot funciona exactamente igual, solo que sin avisar.
	avisos = notify.New(cfg.TelegramBotToken, cfg.TelegramChatID, cfg.TelegramAvisarInicio, store)
	notify.Default = avisos // para los paquetes hondos (agent), que no reciben el notificador

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

	// Notificación INTERNA del backend: el pedido se cerró porque el CLIENTE NO SALIÓ a recibir
	// (estado "no salió el cliente"). Mensaje específico; mismo secreto compartido.
	mux.HandleFunc("POST /internal/order-no-show", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			PedidoID int    `json:"pedido_id"`
			Telefono string `json:"telefono"`
			Motivo   string `json:"motivo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.PedidoID <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		go notifyOrderNoShow(cfg, store, payload.PedidoID, payload.Telefono, payload.Motivo)
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
	// --- Web/panel: revisar y controlar conversaciones (protegido por el secreto de canal) ---
	// Lista de chats recientes (número, último mensaje, modo bot/humano).
	mux.HandleFunc("GET /internal/conversations", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, store.ListConversations(atoiDefault(r.URL.Query().Get("limit"), 100)))
	})
	// Conversación completa de un número (para revisarla).
	mux.HandleFunc("GET /internal/conversation", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		phone := strings.TrimSpace(r.URL.Query().Get("phone"))
		if phone == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"phone":    phone,
			"mode":     store.GetChatMode(phone),
			"messages": store.GetConversation(phone, atoiDefault(r.URL.Query().Get("limit"), 300)),
		})
	})
	// Cambiar el modo de un chat: "bot" (responde el bot) o "human" (respondes tú desde la web).
	// El panel avisa que alguien ABRIO un chat, para dejar de marcarlo como no leido.
	mux.HandleFunc("POST /internal/chat-read", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Phone string `json:"phone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || strings.TrimSpace(payload.Phone) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		store.MarcarChatLeido(payload.Phone)
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /internal/chat-control", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Phone string `json:"phone"`
			Mode  string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || strings.TrimSpace(payload.Phone) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		store.SetChatMode(payload.Phone, payload.Mode)
		if payload.Mode == conversation.ChatModeHuman {
			store.TouchActivity(payload.Phone) // arranca el contador de inactividad del control humano
		}
		writeJSON(w, map[string]any{"phone": payload.Phone, "mode": store.GetChatMode(payload.Phone)})
	})
	// Enviar un mensaje al cliente desde la web (respuesta manual del humano). Lo registra en el audit.
	mux.HandleFunc("POST /internal/send-message", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Phone string `json:"phone"`
			Text  string `json:"text"`
			// MaxHoras: si viene, el mensaje solo sale si el cliente escribio hace menos de esas
			// horas. Lo usa el panel para avisar una entrega hecha por un repartidor externo: un
			// "tu pedido fue entregado" que llega seis horas tarde no le informa nada al cliente
			// -ya recibio su gas- y solo delata que alguien se olvido de marcarlo. Sin este
			// campo el comportamiento es el de siempre (se manda y punto).
			MaxHoras float64 `json:"max_horas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || strings.TrimSpace(payload.Phone) == "" || strings.TrimSpace(payload.Text) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.MaxHoras > 0 {
			ultimo, hay := store.LastClientMessageAt(payload.Phone)
			if !hay || time.Since(time.Unix(ultimo, 0)).Hours() > payload.MaxHoras {
				// 409: no es un error del que llama, es que ya no corresponde mandarlo.
				w.WriteHeader(http.StatusConflict)
				writeJSON(w, map[string]any{"ok": false, "motivo": "fuera de la ventana de tiempo"})
				return
			}
		}
		store.TouchActivity(payload.Phone) // mantiene viva la sesión de control humano
		store.LogMessage(payload.Phone, "human", payload.Text)
		if err := whatsapp.SendText(cfg, payload.Phone, payload.Text); err != nil {
			log.Printf("[send-message] error enviando a %s: %v", payload.Phone, err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// --- Tickets de soporte (panel web; secreto de canal) ---
	// Lista de tickets: ?estado=abierto|cerrado (vacío = todos).
	// Entregas AGENDADAS, para mostrarlas en el panel de Pedidos. Viven aqui (SQLite del bot)
	// y no en la base del backend, porque hasta que se confirman no son pedidos todavia.
	mux.HandleFunc("GET /internal/scheduled", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, store.ListScheduled(r.URL.Query().Get("estado"),
			atoiDefault(r.URL.Query().Get("limit"), 200)))
	})
	mux.HandleFunc("GET /internal/tickets", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, store.ListTickets(r.URL.Query().Get("estado"), atoiDefault(r.URL.Query().Get("limit"), 200)))
	})
	// Cerrar un ticket con su solución.
	// Crear un ticket DESDE EL BACKEND. Hoy lo usa el aviso de "pedido sin conductor": ese
	// pedido es una venta que se pierde si nadie lo ve a tiempo, y antes solo quedaba esperando
	// a que alguien mirara la pantalla de no asignados. Se crea el ticket y sale el correo, la
	// misma via que ya usan las derivaciones.
	mux.HandleFunc("POST /internal/ticket-create", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Phone   string `json:"phone"`
			Motivo  string `json:"motivo"`
			Resumen string `json:"resumen"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || strings.TrimSpace(payload.Motivo) == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := store.CreateTicket(payload.Phone, payload.Motivo, payload.Resumen)
		if id == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if payload.Phone != "" {
			store.LogMessage(payload.Phone, "system",
				fmt.Sprintf("🎫 Ticket de soporte #%d creado — %s", id, payload.Motivo))
		}
		go escalation.SendSupportEmail(cfg, id, payload.Phone, payload.Motivo, payload.Resumen)
		log.Printf("[ticket] #%d creado desde el backend: %s", id, payload.Motivo)
		writeJSON(w, map[string]any{"ok": true, "id": id})
	})

	// Cuando escribio el cliente por ultima vez. El panel lo consulta para mostrar la casilla de
	// "avisar al cliente" desactivada CON el motivo, en vez de dejar marcarla y que no pase nada.
	mux.HandleFunc("GET /internal/last-client-message", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		phone := strings.TrimSpace(r.URL.Query().Get("phone"))
		if phone == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ts, hay := store.LastClientMessageAt(phone)
		if !hay {
			writeJSON(w, map[string]any{"ok": true, "ts": nil})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "ts": ts})
	})

	mux.HandleFunc("POST /internal/ticket-close", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			ID       int64  `json:"id"`
			Solucion string `json:"solucion"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ID <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ok := store.CloseTicket(payload.ID, strings.TrimSpace(payload.Solucion))
		writeJSON(w, map[string]any{"ok": ok})
	})

	// Resumen para el TABLERO de control del panel (una sola llamada).
	mux.HandleFunc("GET /internal/stats", func(w http.ResponseWriter, r *http.Request) {
		if cfg.ChannelSecret == "" || r.Header.Get("X-Channel-Secret") != cfg.ChannelSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		corte := time.Now().Add(-24 * time.Hour).Unix()
		convs := store.ListConversations(500)
		activas, humanos := 0, 0
		for _, c := range convs {
			if c.LastAt >= corte {
				activas++
			}
			if c.Mode == conversation.ChatModeHuman {
				humanos++
			}
		}
		writeJSON(w, map[string]any{
			"tickets_abiertos":        len(store.ListTickets(conversation.TicketAbierto, 500)),
			"conversaciones_activas":  activas,
			"chats_humano":            humanos,
			"programados_pendientes":  store.CountScheduled(conversation.SchedulePendiente),
			"programados_confirmando": store.CountScheduled(conversation.ScheduleConfirmando),
		})
	})

	// --- Scheduler de entregas PROGRAMADAS ---
	// Ticker de 60s (simple y a prueba de reinicios: el estado vive en la tabla, no en memoria).
	// A la hora propuesta le escribe al cliente para confirmar; solo dentro de la ventana de 24h
	// de WhatsApp (desde el ÚLTIMO mensaje del cliente). Confirmaciones sin respuesta expiran en 1h.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[schedule] panic recuperado: %v", r)
			}
		}()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().Unix()
			store.ExpireConfirming(now - 3600)
			for _, sch := range store.DueScheduled(now) {
				lastMsg, ok := store.LastClientMessageAt(sch.Phone)
				if !ok || now-lastMsg > 24*3600-300 {
					// Ventana de 24h cerrada (o sin margen): ya no se le puede escribir. Expira.
					store.SetScheduledEstado(sch.ID, conversation.ScheduleExpirado)
					log.Printf("[schedule] programado #%d de %s expirado (ventana 24h cerrada)", sch.ID, sch.Phone)
					continue
				}
				cuerpo := fmt.Sprintf("⏰ ¡Hola! Tenemos programada tu entrega de %d x %s color %s. "+
					"¿La enviamos ahora? 🚚", sch.Cantidad, sch.ProductoNombre, sch.ColorNombre)
				// Respaldo por si el menú interactivo no sale: el aviso no se puede perder.
				respaldo := cuerpo + " Responde \"Sí\" para enviarlo."
				if err := avisarClienteMenu(cfg, store, sch.Phone, cuerpo,
					[]string{agent.BotonConfirmarEntrega, agent.BotonCancelarEntrega}, respaldo); err != nil {
					log.Printf("[schedule] error escribiendo a %s: %v (reintento en 1 min)", sch.Phone, err)
					continue // sigue 'pendiente'; el próximo tick reintenta
				}
				store.MarkConfirmSent(sch.ID, now)
				log.Printf("[schedule] confirmación enviada al cliente %s (programado #%d)", sch.Phone, sch.ID)
			}
		}
	}()

	// Cierre amable de las conversaciones que quedan a medias (ver cierre.go).
	go cerrarConversacionesInactivas(cfg, store)

	if cfg.LLMProvider == "anthropic" {
		log.Printf("Modelo: anthropic %s (max_tokens=%d)", cfg.AnthropicModel, cfg.AnthropicMaxTokens)
	} else {
		log.Printf("Modelo: gemini %s", cfg.GeminiModel)
	}
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
	last, hasLast := store.LastActivity(inc.From)
	if hasLast && time.Since(last) > conversation.SessionGap {
		log.Printf("[webhook] nueva sesión para %s (inactivo %s); se limpia historial", inc.From, time.Since(last).Round(time.Minute))
		store.ClearHistory(inc.From)
		store.ClearOrderDraft(inc.From)
		store.ClearPendingVerification(inc.From)
	}

	// --- Control humano (takeover) ---
	// Si el chat está tomado por un humano pero lleva más de HumanTakeoverTimeout inactivo, vuelve
	// SOLO al bot (para que un pedido NUEVO lo atienda el bot y no quede colgado esperando a alguien).
	humanControlled := store.GetChatMode(inc.From) == conversation.ChatModeHuman
	if humanControlled && (!hasLast || time.Since(last) > cfg.HumanTakeoverTimeout) {
		log.Printf("[webhook] control humano expiró por inactividad para %s; vuelve al bot", inc.From)
		store.SetChatMode(inc.From, conversation.ChatModeBot)
		humanControlled = false
	}
	store.TouchActivity(inc.From)

	// Auditoría: registra el mensaje ENTRANTE del cliente (siempre, incluso en control humano).
	inboundAudit := strings.TrimSpace(inc.Text)
	if inc.HasLocation {
		inboundAudit = fmt.Sprintf("📍 ubicación: %.6f, %.6f", inc.Latitude, inc.Longitude)
	}
	if inboundAudit != "" {
		store.LogMessage(inc.From, "user", inboundAudit)
	}

	// Aviso al grupo de Telegram de que alguien empezó a escribir. Va DESPUÉS de registrar el
	// mensaje y antes de responder, para que el equipo pueda seguir la conversación desde el
	// principio durante el test. El propio notificador se encarga de mandar uno solo por sesión.
	avisos.AvisarInicio(inc.From, nombreDe(store, inc.From), inboundAudit)

	// En control HUMANO el bot NO responde: solo deja registrado el mensaje para que un humano
	// conteste desde la web. (Las notificaciones de pedido llegó/entregado siguen igual.)
	if humanControlled {
		log.Printf("[webhook] chat en control humano; el bot no responde a %s", inc.From)
		return
	}

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
		_ = replyClient(cfg, store, inc.From, "Por ahora solo puedo leer mensajes de texto y ubicaciones. Por favor, escribe tu consulta. 🙂")
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
			_ = replyClient(cfg, store, inc.From, resumeReply)
			if ag.DidEscalate() {
				// Ya NO se borra el historial al escalar: el ticket queda registrado y la
				// conversación sigue con contexto (un "gracias/no" recibe una despedida normal).
				store.ClearPendingVerification(inc.From)
				ag.ClearEscalated()
			}
			return
		}
		if reply != "" {
			log.Printf("[webhook] código OTP procesado para %s", inc.From)
			log.Printf("[webhook] respuesta: %q", reply)
			_ = replyClient(cfg, store, inc.From, reply)
			return
		}
	}

	// Confirmacion de DIRECCION: el cliente esta respondiendo a "¿te lo enviamos a X?". Se
	// resuelve en codigo, no por el modelo: una direccion mal interpretada manda el gas a otra
	// casa (ver internal/agent/direccion.go).
	if inc.IsText {
		if reply, manejado := ag.ConfirmarDireccion(inc.From, inc.Text); manejado {
			log.Printf("[webhook] confirmacion de direccion resuelta para %s", inc.From)
			_ = replyClient(cfg, store, inc.From, reply)
			return
		}
	}

	// Entrega AGENDADA esperando confirmacion: el "si" del cliente no puede depender de que el
	// modelo decida llamar a la herramienta. Paso tres veces el 27/08 en produccion, con dos
	// modelos distintos: el cliente confirmo y el bot le contesto "¿necesitas algo mas?" sin
	// crear nada. Aqui el pedido se registra en codigo. Si la respuesta no es un si o un no
	// inequivoco, no se toca y sigue al modelo (ver internal/agent/confirmacion.go).
	if inc.IsText {
		if reply, manejado := ag.ConfirmarProgramado(context.Background(), inc.From, inc.Text); manejado {
			log.Printf("[webhook] confirmacion de entrega agendada resuelta para %s", inc.From)
			_ = replyClient(cfg, store, inc.From, reply)
			return
		}
	}

	reply, err := ag.HandleMessage(context.Background(), inc.From, messageForAgent)
	if err != nil {
		log.Printf("[server] Error procesando mensaje: %v", err)
		_ = replyClient(cfg, store, inc.From, "Disculpa, tuvimos un inconveniente técnico. Ya avisé a nuestro equipo para que te contacte.")
		// Ticket de soporte + correo al equipo (antes era un WhatsApp al dueño que podía perderse).
		resumenErr := "El cliente envió: \"" + messageForAgent + "\". La IA falló al responder."
		tid := store.CreateTicket(inc.From, "Error técnico del agente", resumenErr)
		if tid > 0 {
			store.LogMessage(inc.From, "system", fmt.Sprintf("🎫 Ticket de soporte #%d creado — error técnico", tid))
		}
		go escalation.SendSupportEmail(cfg, tid, inc.From, "Error técnico del agente", resumenErr)
		return
	}

	// Si la IA ya envió un MENÚ interactivo (botones/lista) en este turno, ese es el mensaje;
	// no mandamos además el texto de respuesta (evita duplicar la pregunta).
	if ag.MenuSent() {
		log.Printf("[webhook] menú interactivo enviado a %s ✔", inc.From)
		menuTxt := ag.LastMenuText()
		if menuTxt == "" {
			menuTxt = "menú interactivo enviado"
		}
		store.LogMessage(inc.From, "model", "📋 "+menuTxt)
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
	if err := replyClient(cfg, store, inc.From, reply); err != nil {
		log.Printf("[server] Error enviando a %s: %v", inc.From, err)
		return
	}
	if ag.DidEscalate() {
		// Ya NO se borra el historial al escalar (antes el próximo mensaje reiniciaba el flujo
		// como conversación nueva). El ticket queda registrado y el chat sigue con contexto.
		log.Printf("[webhook] escalation detectada para %s (ticket creado; historial se conserva)", inc.From)
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
	if err := avisarCliente(cfg, store, phone, msg); err != nil {
		reportarFallo(cfg, store, phone, "No se pudo avisar la ENTREGA ni pedir la calificación",
			fmt.Sprintf("Pedido #%d. El mensaje no salió: %v", pedidoID, err))
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
	if err := avisarCliente(cfg, store, phone, msg); err != nil {
		reportarFallo(cfg, store, phone, "No se pudo avisar que el conductor LLEGÓ",
			fmt.Sprintf("Pedido #%d. El mensaje no salió: %v", pedidoID, err))
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
	// El historial NO se borra: la memoria del chat dura la ventana de 24h (regla general).
	msg := "😔 Tu pedido fue cancelado por el conductor. Disculpa las molestias. Cuando quieras, puedes hacer un nuevo pedido."
	if err := avisarCliente(cfg, store, phone, msg); err != nil {
		reportarFallo(cfg, store, phone, "No se pudo avisar la CANCELACIÓN del pedido",
			fmt.Sprintf("Pedido #%d. El mensaje no salió: %v", pedidoID, err))
	}
}

// notifyOrderNoShow avisa al cliente por WhatsApp que su pedido se cerró con un PROBLEMA en la
// entrega (no salió / ubicación equivocada / otro). Limpia el pedido activo y el historial.
func notifyOrderNoShow(cfg config.Config, store conversation.Store, pedidoID int, telefono, motivo string) {
	phone := telefono
	if p, ok := store.GetOrderPhone(pedidoID); ok && p != "" {
		phone = p
	}
	if phone == "" {
		log.Printf("[order-no-show] pedido %d sin teléfono de contacto; se ignora", pedidoID)
		return
	}
	store.ClearActivePedido(phone)
	// El historial NO se borra: la memoria del chat dura la ventana de 24h (regla general).
	msg := "🚚 No se pudo completar la entrega de tu pedido 😔"
	if strings.TrimSpace(motivo) != "" {
		msg += " (motivo: " + strings.TrimSpace(motivo) + ")"
	}
	msg += ". Cuando quieras, puedes hacer un nuevo pedido."
	if err := avisarCliente(cfg, store, phone, msg); err != nil {
		reportarFallo(cfg, store, phone, "No se pudo avisar el cierre por AUSENCIA del cliente",
			fmt.Sprintf("Pedido #%d. El mensaje no salió: %v", pedidoID, err))
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
	if err := avisarCliente(cfg, store, phone, msg); err != nil {
		reportarFallo(cfg, store, phone, "No se pudo avisar la REASIGNACIÓN a otro conductor",
			fmt.Sprintf("Pedido #%d. El mensaje no salió: %v", pedidoID, err))
	}
}
