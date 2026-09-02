package conversation

import (
	"sync"
	"time"

	"google.golang.org/genai"
)

// memStore es la implementación en memoria de Store.
// NOTA: se pierde al reiniciar y no sirve con múltiples instancias; para eso está redisStore.
type memStore struct {
	mu              sync.Mutex
	chatLeido       map[string]int64
	data            map[string][]*genai.Content
	locations       map[string]Location
	accounts        map[string]Account
	profiles        map[string]Profile
	lastOrders      map[string]LastOrder
	lastActivity    map[string]time.Time
	orderDrafts     map[string]OrderDraft
	pendingVerif    map[string]Account         // pending OTP verification accounts
	pendingRating   map[string]PendingRating   // pedidos entregados por calificar
	pendingRatingAt map[string]time.Time       // cuándo se creó cada pendiente (para RatingTTL)
	orderPhone      map[int]string             // pedido_id -> teléfono de WhatsApp con el que se hizo
	activePedido    map[string]int             // teléfono -> id del pedido activo (para cancelar)
	pendingWait     map[string]PendingWait     // teléfono -> pedido esperando conductor (reintento 5 min)
	messageLog      map[string][]LoggedMessage // teléfono -> auditoría de la conversación
	chatMode        map[string]string          // teléfono -> "bot" | "human"
	tickets         []Ticket                   // tickets de soporte (escalaciones)
	nextTicketID    int64
	scheduled       []ScheduledOrder // entregas programadas (fuera de horario)
	nextSchedID     int64
	tgThreads       map[string]int64     // teléfono -> hilo de Telegram (grupo de alertas)
	tgAvisado       map[string]time.Time // teléfono -> último aviso de inicio de conversación
	tgSondeo        map[string]time.Time // teléfono -> último aviso de posible sondeo
}

// NewMemStore crea un almacén en memoria vacío.
func NewMemStore() Store {
	return &memStore{
		data:            make(map[string][]*genai.Content),
		locations:       make(map[string]Location),
		accounts:        make(map[string]Account),
		profiles:        make(map[string]Profile),
		lastOrders:      make(map[string]LastOrder),
		lastActivity:    make(map[string]time.Time),
		orderDrafts:     make(map[string]OrderDraft),
		pendingVerif:    make(map[string]Account),
		pendingRating:   make(map[string]PendingRating),
		pendingRatingAt: make(map[string]time.Time),
		orderPhone:      make(map[int]string),
		activePedido:    make(map[string]int),
		pendingWait:     make(map[string]PendingWait),
		messageLog:      make(map[string][]LoggedMessage),
		chatMode:        make(map[string]string),
		tgThreads:       make(map[string]int64),
		tgAvisado:       make(map[string]time.Time),
		tgSondeo:        make(map[string]time.Time),
	}
}

// --- Hilos de Telegram ---

func (s *memStore) GetTelegramThread(phone string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.tgThreads[phone]
	return id, ok && id > 0
}

func (s *memStore) SetTelegramThread(phone string, threadID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tgThreads[phone] = threadID
}

// MarcarAvisoInicio devuelve true solo si toca avisar (no se avisó dentro de `ventana`).
func (s *memStore) MarcarAvisoInicio(phone string, ventana time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.tgAvisado[phone]; ok && time.Since(last) < ventana {
		return false
	}
	s.tgAvisado[phone] = time.Now()
	return true
}

func (s *memStore) LogMessage(phone, role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageLog[phone] = append(s.messageLog[phone], LoggedMessage{Role: role, Content: content, CreatedAt: time.Now().Unix()})
}

func (s *memStore) GetConversation(phone string, limit int) []LoggedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.messageLog[phone]
	if limit > 0 && len(src) > limit {
		src = src[len(src)-limit:]
	}
	out := make([]LoggedMessage, len(src))
	copy(out, src)
	return out
}

func (s *memStore) ListConversations(limit int) []ConversationSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ConversationSummary
	for phone, msgs := range s.messageLog {
		if len(msgs) == 0 {
			continue
		}
		last := msgs[len(msgs)-1]
		mode := s.chatMode[phone]
		if mode == "" {
			mode = ChatModeBot
		}
		out = append(out, ConversationSummary{
			Phone: phone, Mode: mode, LastMessage: last.Content, LastAt: last.CreatedAt, LastRole: last.Role,
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *memStore) CreateScheduled(o ScheduledOrder) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSchedID++
	o.ID = s.nextSchedID
	o.Estado = SchedulePendiente
	o.CreatedAt = time.Now().Unix()
	s.scheduled = append(s.scheduled, o)
	return o.ID
}

// ListScheduled lista las entregas agendadas (equivalente en memoria del store sqlite).
func (s *memStore) ListScheduled(estado string, limit int) []ScheduledOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	var out []ScheduledOrder
	for _, o := range s.scheduled {
		if estado != "" && o.Estado != estado {
			continue
		}
		out = append(out, o)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// CancelScheduled borra las agendadas pendientes del cliente (equivalente en memoria).
func (s *memStore) CancelScheduled(phone string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var quedan []ScheduledOrder
	n := 0
	for _, o := range s.scheduled {
		if o.Phone == phone && (o.Estado == SchedulePendiente || o.Estado == ScheduleConfirmando) {
			n++
			continue
		}
		quedan = append(quedan, o)
	}
	s.scheduled = quedan
	return n
}

func (s *memStore) DueScheduled(now int64) []ScheduledOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ScheduledOrder
	for _, o := range s.scheduled {
		if o.Estado == SchedulePendiente && o.HoraPropuesta <= now {
			out = append(out, o)
		}
	}
	return out
}

func (s *memStore) GetConfirmingSchedule(phone string) (ScheduledOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.scheduled) - 1; i >= 0; i-- {
		if s.scheduled[i].Phone == phone && s.scheduled[i].Estado == ScheduleConfirmando {
			return s.scheduled[i], true
		}
	}
	return ScheduledOrder{}, false
}

func (s *memStore) SetScheduledEstado(id int64, estado string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.scheduled {
		if s.scheduled[i].ID == id {
			s.scheduled[i].Estado = estado
		}
	}
}

func (s *memStore) MarcarChatLeido(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatLeido == nil {
		s.chatLeido = map[string]int64{}
	}
	s.chatLeido[phone] = time.Now().Unix()
}

func (s *memStore) CerrarProgramadoEnEspera(phone string, asignado bool) {
	estado := ScheduleSinRepartidor
	if asignado {
		estado = ScheduleConfirmado
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.scheduled {
		if s.scheduled[i].Phone == phone && s.scheduled[i].Estado == ScheduleEnEspera {
			s.scheduled[i].Estado = estado
		}
	}
}

func (s *memStore) MarkConfirmSent(id int64, ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.scheduled {
		if s.scheduled[i].ID == id {
			s.scheduled[i].Estado = ScheduleConfirmando
			s.scheduled[i].ConfirmSentAt = ts
		}
	}
}

func (s *memStore) ExpireConfirming(olderThan int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.scheduled {
		if s.scheduled[i].Estado == ScheduleConfirmando && s.scheduled[i].ConfirmSentAt < olderThan {
			s.scheduled[i].Estado = ScheduleExpirado
		}
	}
}

func (s *memStore) LastClientMessageAt(phone string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.messageLog[phone]
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].CreatedAt, true
		}
	}
	return 0, false
}

func (s *memStore) CountScheduled(estado string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, o := range s.scheduled {
		if o.Estado == estado {
			n++
		}
	}
	return n
}

func (s *memStore) CreateTicket(phone, motivo, resumen string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTicketID++
	t := Ticket{ID: s.nextTicketID, Phone: phone, Motivo: motivo, Resumen: resumen,
		Estado: TicketAbierto, CreatedAt: time.Now().Unix()}
	s.tickets = append(s.tickets, t)
	return t.ID
}

func (s *memStore) ListTickets(estado string, limit int) []Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ticket
	for i := len(s.tickets) - 1; i >= 0; i-- {
		t := s.tickets[i]
		if estado == TicketAbierto || estado == TicketCerrado {
			if t.Estado != estado {
				continue
			}
		}
		out = append(out, t)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *memStore) CloseTicket(id int64, solucion string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tickets {
		if s.tickets[i].ID == id && s.tickets[i].Estado == TicketAbierto {
			s.tickets[i].Estado = TicketCerrado
			s.tickets[i].Solucion = solucion
			s.tickets[i].ClosedAt = time.Now().Unix()
			return true
		}
	}
	return false
}

func (s *memStore) GetChatMode(phone string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.chatMode[phone]; m != "" {
		return m
	}
	return ChatModeBot
}

func (s *memStore) SetChatMode(phone, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode != ChatModeHuman {
		mode = ChatModeBot
	}
	s.chatMode[phone] = mode
}

func (s *memStore) History(phone string) []*genai.Content {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.data[phone]
	out := make([]*genai.Content, len(src))
	copy(out, src)
	return out
}

func (s *memStore) appendContent(phone string, c *genai.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := append(s.data[phone], c)
	if len(h) > maxTurns {
		h = h[len(h)-maxTurns:]
	}
	s.data[phone] = h
}

func (s *memStore) AppendUser(phone, text string) {
	s.appendContent(phone, turn{Role: "user", Text: text}.toContent())
}

func (s *memStore) AppendModel(phone, text string) {
	s.appendContent(phone, turn{Role: "model", Text: text}.toContent())
}

func (s *memStore) SetLocation(phone string, lat, lng float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locations[phone] = Location{Latitude: lat, Longitude: lng}
}

func (s *memStore) GetLocation(phone string) (Location, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, ok := s.locations[phone]
	return loc, ok
}

func (s *memStore) SetAccount(phone string, account Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[phone] = account
}

func (s *memStore) GetAccount(phone string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[phone]
	return account, ok
}

func (s *memStore) SetProfile(phone string, profile Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[phone] = profile
}

func (s *memStore) ClearHistory(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, phone)
}

func (s *memStore) GetProfile(phone string) (Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[phone]
	return profile, ok
}

func (s *memStore) SetLastOrder(phone string, order LastOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastOrders[phone] = order
}

func (s *memStore) GetLastOrder(phone string) (LastOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.lastOrders[phone]
	return order, ok
}

func (s *memStore) LastActivity(phone string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.lastActivity[phone]
	return t, ok
}

func (s *memStore) TouchActivity(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity[phone] = time.Now()
}

func (s *memStore) SetOrderDraft(phone string, draft OrderDraft) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orderDrafts[phone] = draft
}

func (s *memStore) GetOrderDraft(phone string) (OrderDraft, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	draft, ok := s.orderDrafts[phone]
	return draft, ok
}

func (s *memStore) ClearOrderDraft(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.orderDrafts, phone)
}

func (s *memStore) SetPendingVerification(phone string, account Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingVerif[phone] = account
}

func (s *memStore) GetPendingVerification(phone string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.pendingVerif[phone]
	return account, ok
}

func (s *memStore) ClearPendingVerification(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingVerif, phone)
}

func (s *memStore) SetPendingRating(phone string, rating PendingRating) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingRating[phone] = rating
	s.pendingRatingAt[phone] = time.Now()
}

func (s *memStore) GetPendingRating(phone string) (PendingRating, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rating, ok := s.pendingRating[phone]
	// Caducidad (RatingTTL): un pendiente viejo se descarta.
	if ok && time.Since(s.pendingRatingAt[phone]) > RatingTTL {
		delete(s.pendingRating, phone)
		delete(s.pendingRatingAt, phone)
		return PendingRating{}, false
	}
	return rating, ok
}

func (s *memStore) ClearPendingRating(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingRating, phone)
	delete(s.pendingRatingAt, phone)
}

func (s *memStore) SetOrderPhone(pedidoID int, phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orderPhone[pedidoID] = phone
}

func (s *memStore) GetOrderPhone(pedidoID int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	phone, ok := s.orderPhone[pedidoID]
	return phone, ok
}

func (s *memStore) SetActivePedido(phone string, pedidoID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activePedido[phone] = pedidoID
}

func (s *memStore) GetActivePedido(phone string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.activePedido[phone]
	return id, ok
}

func (s *memStore) ClearActivePedido(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.activePedido, phone)
}

func (s *memStore) SetPendingWait(phone string, w PendingWait) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingWait[phone] = w
}

func (s *memStore) GetPendingWait(phone string) (PendingWait, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.pendingWait[phone]
	return w, ok
}

func (s *memStore) ClearPendingWait(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingWait, phone)
}

func (s *memStore) MarcarAvisoSondeo(phone string, ventana time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.tgSondeo[phone]; ok && time.Since(last) < ventana {
		return false
	}
	s.tgSondeo[phone] = time.Now()
	return true
}
