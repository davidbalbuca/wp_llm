package conversation

import (
	"sync"
	"time"

	"google.golang.org/genai"
)

// memStore es la implementación en memoria de Store.
// NOTA: se pierde al reiniciar y no sirve con múltiples instancias; para eso está redisStore.
type memStore struct {
	mu            sync.Mutex
	data          map[string][]*genai.Content
	locations     map[string]Location
	accounts      map[string]Account
	profiles      map[string]Profile
	lastOrders    map[string]LastOrder
	lastActivity  map[string]time.Time
	orderDrafts   map[string]OrderDraft
	pendingVerif  map[string]Account       // pending OTP verification accounts
	pendingRating map[string]PendingRating // pedidos entregados por calificar
	orderPhone    map[int]string           // pedido_id -> teléfono de WhatsApp con el que se hizo
	activePedido  map[string]int           // teléfono -> id del pedido activo (para cancelar)
}

// NewMemStore crea un almacén en memoria vacío.
func NewMemStore() Store {
	return &memStore{
		data:          make(map[string][]*genai.Content),
		locations:     make(map[string]Location),
		accounts:      make(map[string]Account),
		profiles:      make(map[string]Profile),
		lastOrders:    make(map[string]LastOrder),
		lastActivity:  make(map[string]time.Time),
		orderDrafts:   make(map[string]OrderDraft),
		pendingVerif:  make(map[string]Account),
		pendingRating: make(map[string]PendingRating),
		orderPhone:    make(map[int]string),
		activePedido:  make(map[string]int),
	}
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
}

func (s *memStore) GetPendingRating(phone string) (PendingRating, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rating, ok := s.pendingRating[phone]
	return rating, ok
}

func (s *memStore) ClearPendingRating(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pendingRating, phone)
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
