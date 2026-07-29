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
// Así el bot sabe dónde empieza/termina una conversación por comportamiento real, sin
// depender de contar turnos.
const SessionGap = 40 * time.Minute

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

// OrderDraft es un pedido ya recopilado que quedó EN PAUSA esperando la verificación OTP
// del cliente. Se guarda (transitorio) para poder RETOMAR el pedido automáticamente en
// cuanto el cliente valida su código, sin depender de que la IA recuerde el historial.
type OrderDraft struct {
	Color    string `json:"color"`
	Cantidad int    `json:"cantidad"`
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
