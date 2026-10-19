package whatsapp

// Canal openwa: el WhatsApp propio de cada BODEGA (18/10). Lo usa solo el bot de bodegas
// (BOT_CANAL=openwa, cmd/bot/bodegas.go); el bot de Ubi sigue por la Graph API de Meta.
//
// CANDADOS para que el WhatsApp de Ubi y el de las bodegas no se crucen:
//   - SendText/SendMenu eligen el canal por cfg.Canal; una config de bodega NUNCA llega a Meta y la
//     de Ubi nunca llega a openwa (enviarOpenWA y sendPayload lo verifican).
//   - Se manda SOLO por cfg.OpenWASesion, la de esa bodega. Vacía o igual a la de Ubi (la de los
//     conductores) = error, no se manda.
//   - ParseOpenWA descarta lo que llega por la sesión de Ubi, lo que mandamos nosotros, grupos y
//     estados.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"wp-llm-gas/internal/config"
)

var clienteOpenWA = &http.Client{Timeout: 20 * time.Second}

// ErrSesionAjena: se intentó mandar por una sesión que no es la de esta bodega.
var ErrSesionAjena = fmt.Errorf("openwa: sin sesión de bodega (o es la de Ubi); no se envía")

func sesionValida(cfg config.Config) bool {
	return cfg.OpenWASesion != "" && cfg.OpenWASesion != cfg.OpenWASesionUbi
}

func chatID(to string) string {
	digitos := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, to)
	return digitos + "@c.us"
}

// marcaUbi: el nombre de Ubi en los textos del bot (85 sitios). En el bot de bodegas se cambia por el
// de la bodega en el ÚNICO punto por el que sale todo: el cliente de una bodega no recibe mensajes "de
// Ubi". En minúsculas no se toca (enlaces como ubi.com.ec).
var marcaUbi = regexp.MustCompile(`\bUbi(Gas)?\b`)

// ConMarca cambia "Ubi" por la marca de la config (la bodega). En el bot de Ubi no cambia nada.
func ConMarca(cfg config.Config, texto string) string {
	if cfg.Marca == "" || cfg.Marca == "Ubi" {
		return texto
	}
	return marcaUbi.ReplaceAllString(texto, cfg.Marca)
}

// ErrNoPermitido: BOT_SOLO_TELEFONOS está puesto y el destino no está en la lista (pruebas en dev).
var ErrNoPermitido = fmt.Errorf("openwa: número fuera de BOT_SOLO_TELEFONOS; no se envía")

// AtenderEntrante: si el bot atiende este mensaje según los modos de prueba (BOT_SOLO_TELEFONOS y
// BOT_SOLO_NO_CONTACTOS). Con el WhatsApp personal vinculado, a los contactos guardados no se les
// contesta nada.
func AtenderEntrante(cfg config.Config, inc Incoming) bool {
	if cfg.SoloNoContactos && inc.EsContacto {
		return false
	}
	return Permitido(cfg, inc.From)
}

// Permitido dice si el bot puede atender / escribirle a ese número (ver config.SoloTelefonos).
func Permitido(cfg config.Config, telefono string) bool {
	return len(cfg.SoloTelefonos) == 0 || cfg.SoloTelefonos[strings.TrimSuffix(chatID(telefono), "@c.us")]
}

func enviarOpenWA(cfg config.Config, endpoint string, payload map[string]any) error {
	if cfg.Canal != config.CanalOpenWA || !sesionValida(cfg) {
		return ErrSesionAjena
	}
	if to, _ := payload["chatId"].(string); !Permitido(cfg, to) {
		return ErrNoPermitido
	}
	b, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/sessions/%s/messages/%s", cfg.OpenWABaseURL, cfg.OpenWASesion, endpoint)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("X-API-Key", cfg.OpenWAAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := clienteOpenWA.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openwa %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

func sendTextOpenWA(cfg config.Config, to, body string) error {
	return enviarOpenWA(cfg, "send-text", map[string]any{"chatId": chatID(to), "text": ConMarca(cfg, body)})
}

// --- Menús ---
// openwa no tiene los botones de Meta. El menú sale como texto numerado y la respuesta ("2", "2️⃣")
// se traduce a la opción en TraducirRespuestaMenu, así el agente recibe lo mismo que con un botón:
// el texto completo de la opción.

type menuEnviado struct {
	opciones []string
	en       time.Time
}

var (
	menusMu sync.Mutex
	menus   = map[string]menuEnviado{} // sesion|telefono -> último menú
)

const vigenciaMenu = 30 * time.Minute

var numerosEmoji = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}

func claveMenu(cfg config.Config, to string) string {
	return cfg.OpenWASesion + "|" + chatID(to)
}

func sendMenuOpenWA(cfg config.Config, to, body string, opciones []string) error {
	if len(opciones) == 0 || len(opciones) > len(numerosEmoji) {
		return fmt.Errorf("menú con %d opciones", len(opciones))
	}
	var sb strings.Builder
	sb.WriteString(body)
	sb.WriteString("\n")
	for i, o := range opciones {
		sb.WriteString("\n" + numerosEmoji[i] + " " + o)
	}
	sb.WriteString("\n\nResponde con el número de tu opción.")
	if err := sendTextOpenWA(cfg, to, sb.String()); err != nil {
		return err
	}
	menusMu.Lock()
	menus[claveMenu(cfg, to)] = menuEnviado{opciones: append([]string(nil), opciones...), en: time.Now()}
	menusMu.Unlock()
	return nil
}

// TraducirRespuestaMenu: si el cliente contestó el número de una opción del último menú (vigente), la
// devuelve; si no, el texto tal cual. En el bot de Ubi no hace nada (sus menús son botones).
func TraducirRespuestaMenu(cfg config.Config, from, texto string) string {
	if cfg.Canal != config.CanalOpenWA {
		return texto
	}
	t := strings.TrimSpace(texto)
	n := -1
	for i, e := range numerosEmoji {
		if t == e {
			n = i
		}
	}
	if n < 0 {
		if v, err := strconv.Atoi(strings.TrimSuffix(t, ".")); err == nil {
			n = v - 1
		}
	}
	if n < 0 {
		return texto
	}
	menusMu.Lock()
	defer menusMu.Unlock()
	m, ok := menus[claveMenu(cfg, from)]
	if !ok || time.Since(m.en) > vigenciaMenu || n >= len(m.opciones) {
		return texto
	}
	delete(menus, claveMenu(cfg, from))
	return m.opciones[n]
}

// --- Entrada ---

type mensajeOpenWA struct {
	Event     string `json:"event"`
	SessionID string `json:"sessionId"`
	Data      struct {
		From              string `json:"from"`
		ChatID            string `json:"chatId"`
		Body              string `json:"body"`
		Type              string `json:"type"`
		FromMe            bool   `json:"fromMe"`
		IsGroup           bool   `json:"isGroup"`
		IsStatusBroadcast bool   `json:"isStatusBroadcast"`
		IsLidSender       bool   `json:"isLidSender"`
		SenderPhone       string `json:"senderPhone"`
		Contact           struct {
			PushName    string `json:"pushName"`
			Name        string `json:"name"`
			IsMyContact bool   `json:"isMyContact"`
		} `json:"contact"`
		Location *struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
	} `json:"data"`
}

// FirmaOpenWAValida verifica X-OpenWA-Signature ('sha256=<hmac hex>' del cuerpo). Sin secreto
// configurado no se acepta nada: el bot de bodegas no corre sin firma.
func FirmaOpenWAValida(secreto string, body []byte, firma string) bool {
	if secreto == "" || !strings.HasPrefix(firma, "sha256=") {
		return false
	}
	m := hmac.New(sha256.New, []byte(secreto))
	m.Write(body)
	return hmac.Equal([]byte(firma), []byte("sha256="+hex.EncodeToString(m.Sum(nil))))
}

// ParseOpenWA saca la sesión y el mensaje de un webhook de openwa. ok=false para lo que no es un
// mensaje de un cliente: otros eventos, lo que mandamos nosotros, grupos, estados, la sesión de Ubi,
// o un remitente sin teléfono resoluble.
func ParseOpenWA(cfg config.Config, body []byte) (sesion string, inc Incoming, ok bool) {
	var p mensajeOpenWA
	if err := json.Unmarshal(body, &p); err != nil || p.Event != "message.received" {
		return "", Incoming{}, false
	}
	d := p.Data
	if p.SessionID == "" || p.SessionID == cfg.OpenWASesionUbi || d.FromMe || d.IsGroup || d.IsStatusBroadcast {
		return "", Incoming{}, false
	}
	telefono := ""
	if d.IsLidSender || strings.HasSuffix(d.From, "@lid") {
		telefono = d.SenderPhone
	} else {
		telefono = strings.SplitN(d.From, "@", 2)[0]
	}
	telefono = strings.TrimPrefix(strings.TrimSpace(telefono), "+")
	if telefono == "" || strings.Trim(telefono, "0123456789") != "" {
		return "", Incoming{}, false
	}
	inc = Incoming{From: telefono, PerfilNombre: firstNonEmpty(d.Contact.PushName, d.Contact.Name),
		EsContacto: d.Contact.IsMyContact}
	switch {
	case d.Location != nil && (d.Location.Latitude != 0 || d.Location.Longitude != 0):
		inc.HasLocation, inc.Latitude, inc.Longitude = true, d.Location.Latitude, d.Location.Longitude
	case d.Type == "text" || d.Type == "":
		inc.IsText, inc.Text = true, d.Body
	}
	return p.SessionID, inc, true
}
