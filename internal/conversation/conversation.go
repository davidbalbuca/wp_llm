// Package conversation guarda el historial de cada cliente y su última ubicación GPS.
//
// Expone una interfaz Store con dos implementaciones intercambiables:
//   - memStore   → en memoria (desarrollo; se pierde al reiniciar). Ver mem.go.
//   - redisStore → en Redis con expiración (producción; sobrevive reinicios y varias
//     instancias). Ver redis.go.
//
// main.go elige cuál según el entorno; el resto del proyecto depende solo de esta interfaz.
package conversation

import "google.golang.org/genai"

// maxTurns es el máximo de turnos (Content) que se conservan por conversación.
const maxTurns = 20

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
