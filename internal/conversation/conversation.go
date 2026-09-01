// Package conversation guarda el historial de cada cliente y su última ubicación GPS.
//
// Expone una interfaz Store con dos implementaciones intercambiables:
//   - memStore   → en memoria (desarrollo; se pierde al reiniciar). Ver mem.go.
//   - redisStore → en Redis con expiración (producción; sobrevive reinicios y varias
//     instancias). Ver redis.go.
//
// main.go elige cuál según el entorno; el resto del proyecto depende solo de esta interfaz.
package conversation

import (
	"time"

	"google.golang.org/genai"
)

// maxTurns es solo una RED DE SEGURIDAD contra crecimiento sin límite (un cliente que manda
// cientos de mensajes en una sola sesión), NO el delimitador de la conversación. El límite
// real de una conversación es la SESIÓN: empieza con el primer mensaje tras un periodo de
// inactividad y termina al concretar el pedido, derivar al dueño o quedar inactiva
// (ver SessionGap y la lógica de sesión en cmd/bot). Por eso se mantiene alto: dentro de una
// misma sesión NUNCA queremos truncar y perder contexto a mitad de un pedido.
const maxTurns = 100

// SessionGap es el tiempo de inactividad tras el cual el próximo mensaje del cliente se
// considera una conversación NUEVA (se limpia el historial anterior para arrancar fresco).
// Decisión de David (2026-08): la memoria del bot dura la VENTANA COMPLETA de 24 horas de
// WhatsApp — ningún evento (espera, entrega, cancelación) la borra; solo el paso de 24h.
const SessionGap = 24 * time.Hour

// RatingTTL es cuánto vale una calificación pendiente. Pasado este tiempo se descarta sola:
// sin esto, un pendiente sin responder hacía que el bot pidiera la calificación en el saludo
// de CADA conversación nueva, para siempre.
const RatingTTL = 24 * time.Hour

// Modos de un chat para el control humano (takeover).
const (
	ChatModeBot   = "bot"   // el bot responde normalmente
	ChatModeHuman = "human" // control manual: el bot NO responde; contesta un humano desde la web
)

// LoggedMessage es una línea del registro de auditoría (message_log): todo lo dicho en la
// conversación, incluidas las notificaciones del sistema. Se conserva varios días (durable, NO lo
// borra ClearHistory ni el TTL corto del historial del bot), para revisar/gestionar desde la web.
type LoggedMessage struct {
	Role      string `json:"role"` // "user" (cliente) | "model" (bot) | "system" (notificación) | "human" (respuesta manual)
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"` // unix (segundos)
}

// ConversationSummary resume un chat para la lista de conversaciones de la web.
type ConversationSummary struct {
	Phone       string `json:"phone"`
	Mode        string `json:"mode"`
	LastMessage string `json:"last_message"`
	LastAt      int64  `json:"last_at"`
	// LastRole dice quien hablo de ultimo ("user" o "model"/"system"). Sirve para saber si la
	// conversacion quedo esperando al CLIENTE, que es la unica que tiene sentido cerrar.
	LastRole string `json:"last_role"`
	// Para separar los chats en el panel: sin estas banderas todas las conversaciones se ven
	// iguales y no se distingue cual esta agendada, cual quedo esperando conductor y cual ya
	// se resolvio.
	Programado bool `json:"programado"` // tiene una entrega agendada pendiente
	EnEspera   bool `json:"en_espera"`  // quedo esperando conductor / sin pedido asignado
	// SinPedido: hablo con el bot y NO hay ningun pedido de por medio. Antes esta pestana
	// miraba pending_wait, que solo tiene filas durante los 5 minutos que dura la busqueda de
	// repartidor, asi que estaba vacia practicamente siempre.
	SinPedido bool `json:"sin_pedido"`
	// NoAsignado: su pedido se quedo sin conductor y todavia nadie lo ha resuelto. Se deduce
	// del ticket abierto que abre el backend, asi que no hace falta guardar nada aparte.
	NoAsignado bool `json:"no_asignado"`
}

// Estados de un ticket de soporte.
const (
	TicketAbierto = "abierto"
	TicketCerrado = "cerrado"
)

// Estados de un pedido PROGRAMADO (entrega agendada fuera de horario).
const (
	SchedulePendiente   = "pendiente"   // esperando que llegue la hora propuesta
	ScheduleConfirmando = "confirmando" // ya se le escribió al cliente; esperando su "sí"
	ScheduleConfirmado  = "confirmado"  // el cliente confirmó y el pedido real se registró
	// ScheduleEnEspera: el cliente confirmó pero no había repartidor libre, así que el pedido
	// está en la cola de asignación y TODAVÍA no existe en el backend. Es un estado aparte y no
	// "confirmado" porque si no el panel miente: decía confirmado y en Pedidos no había nada.
	ScheduleEnEspera = "buscando_repartidor"
	// ScheduleSinRepartidor: se acabaron los 5 minutos de busqueda sin nadie libre. El pedido
	// pasa a la lista de No asignados para que alguien lo gestione a mano.
	ScheduleSinRepartidor = "sin_repartidor"
	ScheduleExpirado      = "expirado" // no confirmó / venció la ventana de 24h
)

// ScheduledOrder es una entrega PROGRAMADA: el cliente escribió fuera del horario laboral y
// agendó una hora (dentro del horario y de la ventana de 24h de WhatsApp). A la hora propuesta
// el scheduler le escribe para confirmar; si confirma, se registra el pedido real.
type ScheduledOrder struct {
	ID             int64   `json:"id"`
	Phone          string  `json:"phone"`
	Identificacion string  `json:"identificacion"`
	Nombres        string  `json:"nombres"`
	IDCategoria    int     `json:"idcategoria"`
	IDProducto     int     `json:"idproducto"`
	IDColor        int     `json:"idcolor"`
	Cantidad       int     `json:"cantidad"`
	IDTipoPago     int     `json:"idtipopago"`
	ProductoNombre string  `json:"producto_nombre"`
	ColorNombre    string  `json:"color_nombre"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	HoraPropuesta  int64   `json:"hora_propuesta"` // unix
	Estado         string  `json:"estado"`
	ConfirmSentAt  int64   `json:"confirm_sent_at"`
	CreatedAt      int64   `json:"created_at"`
}

// Ticket es un caso de SOPORTE creado cuando el bot escala (cliente pide un humano, la IA no
// puede resolver, o hubo un error técnico). Se gestiona desde el panel: un agente lo revisa,
// interactúa con el cliente (chat/llamada) y lo cierra escribiendo la solución.
type Ticket struct {
	ID        int64  `json:"id"`
	Phone     string `json:"phone"`
	Motivo    string `json:"motivo"`
	Resumen   string `json:"resumen"`
	Estado    string `json:"estado"` // "abierto" | "cerrado"
	Solucion  string `json:"solucion"`
	CreatedAt int64  `json:"created_at"`
	ClosedAt  int64  `json:"closed_at"` // 0 si sigue abierto
}

// Location es la última ubicación GPS que compartió un cliente por WhatsApp.
// Se usa para registrar el pedido en el backend, que requiere latitude/longitude.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Account son las credenciales y tokens georoutes de un cliente de WhatsApp. El backend genera
// username/password al dar de alta la cuenta; el bot los guarda (durables) y los reutiliza para
// autenticar (login → JWT). Los JWT se cachean aquí para no re-hacer login en cada pedido.
type Account struct {
	Username string `json:"username"`
	Password string `json:"password"`
	UserID   int    `json:"user_id"`
	JWT      string `json:"jwt"`
	Refresh  string `json:"refresh"`
}

// Profile son los datos personales del cliente que se piden para un pedido. Se guardan
// (durables) tras el primer pedido para NO volver a pedírselos en pedidos siguientes:
// un cliente recurrente solo comparte ubicación y elige producto.
type Profile struct {
	Identificacion string `json:"identificacion"`
	Nombres        string `json:"nombres"`
	Correo         string `json:"correo"`
}

// LastOrder es el resumen del último pedido exitoso de un cliente. Se guarda (durable) para
// ofrecerle repetir lo mismo cuando vuelve, en lugar de preguntarle todo desde cero.
type LastOrder struct {
	Producto string `json:"producto"` // nombre del producto (ej: "GAS 23KG")
	Color    string `json:"color"`    // color/marca del cilindro
	Cantidad int    `json:"cantidad"` // cantidad de cilindros
	Fecha    string `json:"fecha"`    // fecha del pedido, formato legible (dd/mm/aaaa)
}

// PendingRating indica que un pedido del cliente acaba de ser ENTREGADO y el bot le pidió
// calificar al conductor. Mientras exista, si el cliente manda una calificación (1-5), el
// bot la registra contra este pedido/conductor. Transitorio: se limpia al calificar o al
// arrancar una sesión nueva.
type PendingRating struct {
	PedidoID  int    `json:"pedido_id"`
	Conductor string `json:"conductor"`
}

// OrderDraft es un pedido ya recopilado que quedó EN PAUSA esperando la verificación OTP
// del cliente. Se guarda (transitorio) para poder RETOMAR el pedido automáticamente en
// cuanto el cliente valida su código, sin depender de que la IA recuerde el historial.
type OrderDraft struct {
	Color    string `json:"color"`
	Cantidad int    `json:"cantidad"`
}

// PendingWait es un pedido que NO encontró conductor y quedó a la espera porque el cliente
// eligió esperar (~5 min). Guarda lo necesario para REINTENTAR la asignación; la ubicación y la
// cuenta se leen del store (ya están). Transitorio: se limpia al asignar, cancelar o expirar.
type PendingWait struct {
	IDCategoria    int    `json:"idcategoria"`
	IDProducto     int    `json:"idproducto"`
	IDColor        int    `json:"idcolor"`
	Cantidad       int    `json:"cantidad"`
	IDTipoPago     int    `json:"idtipopago"`
	ProductoNombre string `json:"producto_nombre"`
	ColorNombre    string `json:"color_nombre"`
	Identificacion string `json:"identificacion"`
	Nombres        string `json:"nombres"`
}

// Store es el almacén de estado conversacional por número de teléfono.
// Las operaciones no devuelven error a propósito: un fallo del backend de estado
// se registra y degrada de forma segura (p. ej. historial vacío), sin tumbar el chat.
type Store interface {
	// History devuelve una copia del historial del cliente (vacío si es nuevo).
	History(phone string) []*genai.Content
	// AppendUser añade un turno del cliente al historial.
	AppendUser(phone, text string)
	// AppendModel añade un turno del modelo al historial.
	AppendModel(phone, text string)
	// SetLocation guarda la última ubicación compartida por el cliente.
	SetLocation(phone string, lat, lng float64)
	// GetLocation devuelve la última ubicación del cliente (ok=false si no hay).
	GetLocation(phone string) (Location, bool)
	// SetAccount guarda las credenciales georoutes del cliente (durables).
	SetAccount(phone string, account Account)
	// GetAccount devuelve las credenciales georoutes del cliente (ok=false si no hay).
	GetAccount(phone string) (Account, bool)
	// ClearHistory elimina todo el historial de conversación de un cliente. Se usa
	// cuando se deriva al dueño, para que el próximo mensaje arranque fresco sin que
	// la IA repita "ya derivé al dueño" del historial anterior.
	ClearHistory(phone string)
	// SetProfile guarda los datos personales del cliente (durables) para no re-pedirlos.
	SetProfile(phone string, profile Profile)
	// GetProfile devuelve los datos personales del cliente (ok=false si no hay).
	GetProfile(phone string) (Profile, bool)
	// SetLastOrder guarda el resumen del último pedido exitoso del cliente (durable).
	SetLastOrder(phone string, order LastOrder)
	// GetLastOrder devuelve el último pedido exitoso del cliente (ok=false si no hay).
	GetLastOrder(phone string) (LastOrder, bool)
	// LastActivity devuelve el instante del último mensaje del cliente (ok=false si no hay).
	// Sirve para delimitar sesiones: si pasó más de SessionGap, el próximo mensaje inicia
	// una conversación nueva.
	LastActivity(phone string) (time.Time, bool)
	// TouchActivity registra que hubo actividad del cliente ahora (para el control de sesión).
	TouchActivity(phone string)
	// SetOrderDraft guarda un pedido en pausa a la espera de verificación OTP (transitorio).
	SetOrderDraft(phone string, draft OrderDraft)
	// GetOrderDraft devuelve el pedido en pausa por OTP (ok=false si no hay).
	GetOrderDraft(phone string) (OrderDraft, bool)
	// ClearOrderDraft elimina el pedido en pausa por OTP.
	ClearOrderDraft(phone string)
	// SetPendingVerification marca al cliente como pendiente de verificación OTP.
	// Mientras esté en este estado, el bot trata el próximo mensaje como el código de
	// verificación en lugar de pasarlo a la IA.
	SetPendingVerification(phone string, account Account)
	// GetPendingVerification devuelve los datos de verificación pendiente.
	GetPendingVerification(phone string) (Account, bool)
	// ClearPendingVerification elimina el estado de verificación pendiente.
	ClearPendingVerification(phone string)
	// SetPendingRating marca que el cliente tiene un pedido recién entregado por calificar.
	SetPendingRating(phone string, rating PendingRating)
	// GetPendingRating devuelve el pedido pendiente de calificación (ok=false si no hay).
	GetPendingRating(phone string) (PendingRating, bool)
	// ClearPendingRating elimina el estado de calificación pendiente.
	ClearPendingRating(phone string)
	// SetOrderPhone recuerda con qué teléfono de WhatsApp se hizo un pedido. Se usa para
	// contactar al cliente por el número CORRECTO cuando el backend avisa que se entregó,
	// aunque el Cliente.telefono del backend sea distinto (p. ej. clientes que se registraron
	// antes por la app con otro número).
	SetOrderPhone(pedidoID int, phone string)
	// GetOrderPhone devuelve el teléfono de WhatsApp asociado a un pedido (ok=false si no hay).
	GetOrderPhone(pedidoID int) (string, bool)
	// SetActivePedido guarda el id del pedido ACTIVO del cliente (el último creado), para poder
	// cancelarlo si el cliente lo pide por WhatsApp ("cancelar mi pedido").
	SetActivePedido(phone string, pedidoID int)
	// GetActivePedido devuelve el id del pedido activo del cliente (ok=false si no hay).
	GetActivePedido(phone string) (int, bool)
	// ClearActivePedido elimina el pedido activo (tras cancelarlo o entregarlo).
	ClearActivePedido(phone string)
	// SetPendingWait guarda un pedido que no encontró conductor y quedó esperando (el cliente
	// eligió esperar). Sirve para reintentar la asignación durante ~5 min.
	SetPendingWait(phone string, w PendingWait)
	// GetPendingWait devuelve el pedido en espera de conductor (ok=false si no hay).
	GetPendingWait(phone string) (PendingWait, bool)
	// ClearPendingWait elimina el pedido en espera (al asignarse, cancelar o expirar).
	ClearPendingWait(phone string)

	// --- Auditoría de conversaciones (durable, para revisar/gestionar desde la web) ---
	// LogMessage añade una línea al registro de auditoría. NO la borra ClearHistory ni el TTL
	// corto del historial del bot; se conserva según la retención configurada (AUDIT_LOG_DAYS).
	LogMessage(phone, role, content string)
	// GetConversation devuelve las últimas `limit` líneas de auditoría de un chat (orden cronológico).
	GetConversation(phone string, limit int) []LoggedMessage
	// ListConversations lista los chats recientes con su último mensaje y modo.
	ListConversations(limit int) []ConversationSummary
	// ListScheduled lista las entregas agendadas (para verlas en el panel de Pedidos).
	// estado vacío = todas; si no, filtra por ese estado.
	ListScheduled(estado string, limit int) []ScheduledOrder
	// CancelScheduled BORRA las entregas agendadas de un cliente que aún no se cumplieron.
	// Devuelve cuántas se eliminaron.
	CancelScheduled(phone string) int
	// GetChatMode devuelve el modo del chat ("bot" por defecto).
	GetChatMode(phone string) string
	// SetChatMode fija el modo del chat ("bot" o "human").
	SetChatMode(phone, mode string)

	// --- Pedidos PROGRAMADOS (entregas agendadas fuera de horario) ---
	// CreateScheduled guarda una entrega programada (estado pendiente). Devuelve su id (0 si falló).
	CreateScheduled(s ScheduledOrder) int64
	// DueScheduled devuelve los programados PENDIENTES cuya hora ya llegó.
	DueScheduled(now int64) []ScheduledOrder
	// GetConfirmingSchedule devuelve el programado EN CONFIRMACIÓN de un cliente (si hay).
	GetConfirmingSchedule(phone string) (ScheduledOrder, bool)
	// CerrarProgramadoEnEspera cierra la entrega agendada que quedo buscando repartidor: pasa a
	// confirmado si se asigno, o a sin_repartidor si se agotaron los 5 minutos. Sin esto el panel
	// se queda diciendo "buscando" para siempre. No hace nada si el cliente no venia de una
	// entrega agendada, que es el caso del pedido normal.
	CerrarProgramadoEnEspera(phone string, asignado bool)
	// SetScheduledEstado cambia el estado de un programado.
	SetScheduledEstado(id int64, estado string)
	// MarkConfirmSent marca que ya se le escribió al cliente (estado confirmando + timestamp).
	MarkConfirmSent(id int64, ts int64)
	// ExpireConfirming expira los "confirmando" cuyo aviso se envió antes de `olderThan` (unix).
	ExpireConfirming(olderThan int64)
	// LastClientMessageAt devuelve cuándo escribió el CLIENTE por última vez (ventana de 24h
	// de WhatsApp; sale del message_log, no de la actividad del panel).
	LastClientMessageAt(phone string) (int64, bool)
	// CountScheduled cuenta los programados en un estado (para el tablero de control).
	CountScheduled(estado string) int

	// --- Tickets de soporte (escalaciones; durables, se gestionan desde la web) ---
	// CreateTicket crea un ticket ABIERTO y devuelve su id (0 si falló).
	CreateTicket(phone, motivo, resumen string) int64
	// ListTickets lista tickets por estado ("abierto", "cerrado" o "" = todos), recientes primero.
	ListTickets(estado string, limit int) []Ticket
	// CloseTicket cierra un ticket con su solución. Devuelve false si no existe o ya está cerrado.
	CloseTicket(id int64, solucion string) bool
}

// turn es la forma serializable de un turno de conversación. Se usa para persistir
// en Redis de forma compacta y legible (role + texto), y se reconstruye a genai.Content
// al leer. El historial que guardamos es siempre texto (user/model), sin function calls.
type turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (t turn) toContent() *genai.Content {
	return &genai.Content{Role: t.Role, Parts: []*genai.Part{{Text: t.Text}}}
}
