package conversation

import (
	"sync"

	"google.golang.org/genai"
)

// memStore es la implementación en memoria de Store.
// NOTA: se pierde al reiniciar y no sirve con múltiples instancias; para eso está redisStore.
type memStore struct {
	mu        sync.Mutex
	data      map[string][]*genai.Content
	locations map[string]Location
	accounts  map[string]Account
}

// NewMemStore crea un almacén en memoria vacío.
func NewMemStore() Store {
	return &memStore{
		data:      make(map[string][]*genai.Content),
		locations: make(map[string]Location),
		accounts:  make(map[string]Account),
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
