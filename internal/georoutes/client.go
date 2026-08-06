// Package georoutes es el cliente HTTP de la API de georoutes del backend GEOWARE
// (ubi-geoware). El bot de WhatsApp entra por el MISMO flujo que la app móvil
// (registro/login/dirección/pedido), sin crear un canal de pedidos paralelo.
//
// Contrato: ubi-geoware/core/georoutes/urls.py + apis.py. Todas las respuestas usan
// el envoltorio { "codigo", "mensaje", "resultado" }.
package georoutes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Identificadores fijos del bot como "dispositivo" ante el backend (login/createUser
// los exigen). No representan un teléfono real: identifican al canal WhatsApp.
const (
	botDevice        = "whatsapp-bot"
	botFirebaseToken = "whatsapp-bot"
)

// envelope es el envoltorio estándar de respuesta de la API georoutes.
type envelope struct {
	Codigo    int             `json:"codigo"`
	Mensaje   string          `json:"mensaje"`
	Resultado json.RawMessage `json:"resultado"`
}

// Client llama a la API georoutes bajo {backendURL}/georoutes/.
type Client struct {
	baseURL string
	http    *http.Client

	// Credenciales de una cuenta de servicio para leer el catálogo cuando el backend
	// corre con DEBUG=False (los GET de catálogo exigen JWT). Si están vacías, el bot
	// llama al catálogo sin token (comportamiento de DEV con DEBUG=True).
	svcUser     string
	svcPassword string

	// svcToken es el JWT de servicio cacheado; svcMu lo protege del acceso concurrente.
	svcMu    sync.Mutex
	svcToken string
}

// NewClient crea el cliente apuntando a {backendURL}/georoutes.
func NewClient(backendURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(backendURL, "/") + "/georoutes",
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// SetServiceAccount define las credenciales de la cuenta de servicio usada para leer el
// catálogo con JWT (necesario en prod con DEBUG=False). Si user o password están vacíos,
// el catálogo se pide sin token (DEV).
func (c *Client) SetServiceAccount(user, password string) {
	c.svcUser = user
	c.svcPassword = password
}

// serviceToken devuelve el JWT de servicio cacheado, autenticando si hace falta. Si no
// hay credenciales de servicio configuradas, devuelve "" (el catálogo se pedirá sin token).
// Si force es true, ignora la caché y vuelve a hacer login (para recuperarse de un 401).
func (c *Client) serviceToken(force bool) (string, error) {
	if c.svcUser == "" || c.svcPassword == "" {
		return "", nil
	}

	c.svcMu.Lock()
	defer c.svcMu.Unlock()

	if !force && c.svcToken != "" {
		return c.svcToken, nil
	}

	tokens, err := c.Login(c.svcUser, c.svcPassword)
	if err != nil {
		return "", fmt.Errorf("login de cuenta de servicio (catálogo) falló: %w", err)
	}
	c.svcToken = tokens.Access
	return c.svcToken, nil
}

// post envía JSON a path con un bearer opcional y devuelve el "resultado" crudo.
// Si el HTTP no es 2xx, devuelve un error con el "mensaje" del backend (en español).
func (c *Client) post(path string, payload any, bearer string) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("respuesta no válida del backend (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		mensaje := strings.TrimSpace(env.Mensaje)
		if mensaje == "" {
			mensaje = fmt.Sprintf("error del backend (HTTP %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", mensaje)
	}
	return env.Resultado, nil
}

// ClientInfo indica si ya existe un cliente con cierta cédula, y sus datos básicos.
type ClientInfo struct {
	Existe  bool   `json:"existe"`
	Nombres string `json:"nombres"`
	Correo  string `json:"correo"`
}

// ClientExists consulta (SOLO LECTURA, sin efectos secundarios) si ya existe un cliente
// con esa cédula. A diferencia de UserExists, NO crea usuario ni envía código: sirve para
// reconocer al cliente al inicio y saltarse el registro (nombre/correo).
func (c *Client) ClientExists(identificacion string) (*ClientInfo, error) {
	res, err := c.get("/clientExists/?identificacion=" + url.QueryEscape(identificacion))
	if err != nil {
		return nil, err
	}
	var info ClientInfo
	if err := json.Unmarshal(res, &info); err != nil {
		return nil, fmt.Errorf("respuesta de cliente no válida del backend: %w", err)
	}
	return &info, nil
}

// Account son las credenciales que el backend genera y devuelve para un cliente.
// El bot las guarda por teléfono y las reutiliza para hacer login.
type Account struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UserExists devuelve las credenciales del cliente con esa identificación si ya
// existe. Devuelve error (HTTP 404 del backend) si no existe.
func (c *Client) UserExists(identificacion string) (*Account, error) {
	res, err := c.post("/userExists/", map[string]any{
		"identificacion": identificacion,
		"dispositivo":    botDevice,
	}, "")
	if err != nil {
		return nil, err
	}
	var acc Account
	if err := json.Unmarshal(res, &acc); err != nil {
		return nil, fmt.Errorf("credenciales no válidas del backend: %w", err)
	}
	return &acc, nil
}

// NewClientInput son los datos para crear la cuenta del cliente de WhatsApp.
// El backend crea User + Cliente (+ dirección) y devuelve las credenciales.
type NewClientInput struct {
	Identificacion string
	Nombres        string
	Telefono       string
	Correo         string
	Direccion      string
	Alias          string
	Referencia     string
	Latitude       float64
	Longitude      float64
}

// WppGetOrCreateClient recupera (o crea) el cliente del BOT de WhatsApp y devuelve sus
// credenciales. El backend lo deja YA VERIFICADO (sin OTP ni correo) y usa un correo
// placeholder. Endpoint EXCLUSIVO del bot: POST /wppGetOrCreateClient/. Reemplaza el par
// UserExists+CreateUser del flujo viejo (que reseteaba verificación y mandaba correo).
func (c *Client) WppGetOrCreateClient(identificacion, nombres, telefono string) (*Account, error) {
	res, err := c.post("/wppGetOrCreateClient/", map[string]any{
		"identificacion": identificacion,
		"nombres":        nombres,
		"telefono":       telefono,
	}, "")
	if err != nil {
		return nil, err
	}
	var acc Account
	if err := json.Unmarshal(res, &acc); err != nil {
		return nil, fmt.Errorf("credenciales no válidas del backend: %w", err)
	}
	return &acc, nil
}

// CreateUser da de alta al cliente y devuelve las credenciales generadas por el backend.
func (c *Client) CreateUser(in NewClientInput) (*Account, error) {
	res, err := c.post("/createUser/", map[string]any{
		"identificacion": in.Identificacion,
		"nombres":        in.Nombres,
		"telefono":       in.Telefono,
		"correo":         in.Correo,
		"direccion":      in.Direccion,
		"alias":          in.Alias,
		"referencia":     in.Referencia,
		"latitude":       in.Latitude,
		"longitude":      in.Longitude,
		"dispositivo":    botDevice,
		"firebasetoken":  botFirebaseToken,
	}, "")
	if err != nil {
		return nil, err
	}
	var acc Account
	if err := json.Unmarshal(res, &acc); err != nil {
		return nil, fmt.Errorf("credenciales no válidas del backend: %w", err)
	}
	return &acc, nil
}

// Tokens son los JWT que devuelve el login, más el estado de verificación.
type Tokens struct {
	Access       string `json:"access"`
	Refresh      string `json:"refresh"`
	EstaValidado bool   `json:"estaValidado"`
	AceptoPDP    bool   `json:"aceptoPDP"`
}

// Login autentica con las credenciales y devuelve los JWT (access/refresh).
func (c *Client) Login(username, password string) (*Tokens, error) {
	res, err := c.post("/login/", map[string]any{
		"usuario":       username,
		"password":      password,
		"dispositivo":   botDevice,
		"firebasetoken": botFirebaseToken,
	}, "")
	if err != nil {
		return nil, err
	}
	var tokens Tokens
	if err := json.Unmarshal(res, &tokens); err != nil {
		return nil, fmt.Errorf("tokens no válidos del backend: %w", err)
	}
	return &tokens, nil
}

// GetVerificationCode solicita que el backend envíe un código OTP al correo del
// cliente autenticado. Requiere el JWT del cliente a verificar.
func (c *Client) GetVerificationCode(jwt string) error {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/getCodeVerification/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("respuesta no válida (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s", strings.TrimSpace(env.Mensaje))
	}
	return nil
}

// ValidateVerificationCode envía el código OTP que el cliente recibió por correo y
// lo valida. Devuelve error si el código es incorrecto.
func (c *Client) ValidateVerificationCode(jwt, codigo string) error {
	_, err := c.post("/validateCodeVerification/", map[string]any{"codigo": codigo}, jwt)
	return err
}

// DirectionInput es la dirección a registrar desde la ubicación de WhatsApp.
type DirectionInput struct {
	Direccion  string
	Alias      string
	Referencia string
	Latitude   float64
	Longitude  float64
}

// Direction es la dirección creada; su ID se usa como "iddireccion" del pedido.
type Direction struct {
	ID        int    `json:"id"`
	SectorID  int    `json:"sector_id"`
	Direccion string `json:"direccion"`
}

// CreateDirection registra una dirección (principal) del cliente autenticado (JWT)
// y devuelve su ID para usarlo en el pedido.
func (c *Client) CreateDirection(jwt string, in DirectionInput) (*Direction, error) {
	res, err := c.post("/createDirectionClient/", map[string]any{
		"direccion":  in.Direccion,
		"alias":      in.Alias,
		"principal":  true,
		"referencia": in.Referencia,
		"latitude":   in.Latitude,
		"longitude":  in.Longitude,
	}, jwt)
	if err != nil {
		return nil, err
	}
	var dir Direction
	if err := json.Unmarshal(res, &dir); err != nil {
		return nil, fmt.Errorf("dirección no válida del backend: %w", err)
	}
	return &dir, nil
}
