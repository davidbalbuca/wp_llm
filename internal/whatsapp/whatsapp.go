// Package whatsapp maneja la comunicación con la WhatsApp Business Cloud API de Meta:
// verificación del webhook, parseo de mensajes entrantes y envío de texto.
package whatsapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"wp-llm-gas/internal/config"
)

// coordsRe captura un par de coordenadas (lat, lng) dentro de un texto: enlaces de Google
// Maps (`?q=-2.84,-78.99`, `@-2.84,-78.99`), pares "lat, lng" sueltos, etc. Muchos clientes
// pegan la ubicación como URL en vez de usar el adjunto nativo de WhatsApp; así igual la tomamos.
var coordsRe = regexp.MustCompile(`(-?\d{1,3}\.\d{3,})[,\s]+(-?\d{1,3}\.\d{3,})`)

// ParseCoordsFromText intenta extraer coordenadas GPS de un texto (p. ej. un enlace de
// Google Maps que el cliente pegó). Devuelve ok=false si no encuentra un par válido y
// plausible (lat en [-90,90], lng en [-180,180]).
func ParseCoordsFromText(text string) (lat, lng float64, ok bool) {
	m := coordsRe.FindStringSubmatch(text)
	if len(m) != 3 {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(m[1], 64)
	lng, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// sendPayload envía un payload ya armado a la Graph API de Meta (POST /messages).
func sendPayload(cfg config.Config, payload map[string]any) error {
	url := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages",
		cfg.GraphAPIVersion, cfg.PhoneNumberID)
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+cfg.WhatsAppToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph API %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

// SendText envía un mensaje de texto por WhatsApp vía la Graph API de Meta.
func SendText(cfg config.Config, to, body string) error {
	return sendPayload(cfg, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": body},
	})
}

// firstNonEmpty devuelve el primer string no vacío.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// trunc recorta s a n runas (los títulos de botón/fila de WhatsApp tienen límites cortos).
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// SendMenu envía un MENÚ INTERACTIVO tappable por WhatsApp: con 2-3 opciones usa botones,
// con 4-10 usa una lista. El "id" de cada opción lleva el texto completo (para no perder
// información por el truncado del título visible); ese id vuelve en el webhook al elegir.
// Devuelve error si hay <1 o >10 opciones (el llamador debe caer a texto normal).
func SendMenu(cfg config.Config, to, body string, opciones []string) error {
	interactive, err := buildInteractiveMenu(body, opciones)
	if err != nil {
		return err
	}
	return sendPayload(cfg, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "interactive",
		"interactive":       interactive,
	})
}

// buildInteractiveMenu arma el objeto "interactive" (botones si <=3, lista si 4..10).
func buildInteractiveMenu(body string, opciones []string) (map[string]any, error) {
	if len(opciones) == 0 {
		return nil, fmt.Errorf("menú sin opciones")
	}
	if len(opciones) > 10 {
		return nil, fmt.Errorf("demasiadas opciones (%d); el máximo de un menú es 10", len(opciones))
	}

	var interactive map[string]any
	if len(opciones) <= 3 {
		buttons := make([]map[string]any, 0, len(opciones))
		for _, o := range opciones {
			buttons = append(buttons, map[string]any{
				"type":  "reply",
				"reply": map[string]string{"id": trunc(o, 256), "title": trunc(o, 20)},
			})
		}
		interactive = map[string]any{
			"type":   "button",
			"body":   map[string]string{"text": trunc(body, 1024)},
			"action": map[string]any{"buttons": buttons},
		}
	} else {
		rows := make([]map[string]any, 0, len(opciones))
		for _, o := range opciones {
			rows = append(rows, map[string]any{"id": trunc(o, 200), "title": trunc(o, 24)})
		}
		interactive = map[string]any{
			"type": "list",
			"body": map[string]string{"text": trunc(body, 1024)},
			"action": map[string]any{
				"button":   "Ver opciones",
				"sections": []map[string]any{{"title": "Opciones", "rows": rows}},
			},
		}
	}

	return interactive, nil
}

// --- Parseo del webhook entrante ---

type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Messages []struct {
					From string `json:"from"`
					Type string `json:"type"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
					Location *struct {
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
					} `json:"location"`
					Interactive *struct {
						Type        string `json:"type"`
						ButtonReply *struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"button_reply"`
						ListReply *struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"list_reply"`
					} `json:"interactive"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// Incoming es un mensaje entrante ya normalizado. Las coordenadas se exponen como
// campos planos para no acoplar este paquete con el almacén de conversaciones.
type Incoming struct {
	From        string
	Text        string
	IsText      bool
	HasLocation bool
	Latitude    float64
	Longitude   float64
}

// ParseIncoming extrae el primer mensaje útil del payload de Meta.
// Devuelve ok=false para eventos de estado u otros payloads sin mensaje.
func ParseIncoming(body []byte) (Incoming, bool) {
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Incoming{}, false
	}
	if len(p.Entry) == 0 || len(p.Entry[0].Changes) == 0 {
		return Incoming{}, false
	}
	msgs := p.Entry[0].Changes[0].Value.Messages
	if len(msgs) == 0 {
		return Incoming{}, false // evento de estado u otro sin mensaje
	}
	m := msgs[0]
	switch {
	case m.Type == "location" && m.Location != nil:
		return Incoming{
			From:        m.From,
			HasLocation: true,
			Latitude:    m.Location.Latitude,
			Longitude:   m.Location.Longitude,
		}, true
	case m.Type == "text":
		return Incoming{From: m.From, Text: m.Text.Body, IsText: true}, true
	case m.Type == "interactive" && m.Interactive != nil:
		// El cliente tocó un botón o una opción de lista. Tomamos el id (que lleva el texto
		// completo) y lo tratamos como si lo hubiera escrito.
		var elegido string
		if m.Interactive.ButtonReply != nil {
			elegido = firstNonEmpty(m.Interactive.ButtonReply.ID, m.Interactive.ButtonReply.Title)
		} else if m.Interactive.ListReply != nil {
			elegido = firstNonEmpty(m.Interactive.ListReply.ID, m.Interactive.ListReply.Title)
		}
		if elegido != "" {
			return Incoming{From: m.From, Text: elegido, IsText: true}, true
		}
		return Incoming{From: m.From, IsText: false}, true
	default:
		return Incoming{From: m.From, IsText: false}, true // mensaje no-texto
	}
}

// VerifyWebhook resuelve el handshake GET de Meta (hub.mode/hub.verify_token/hub.challenge).
func VerifyWebhook(cfg config.Config, mode, token, challenge string) (string, bool) {
	if mode == "subscribe" && token == cfg.VerifyToken {
		return challenge, true
	}
	return "", false
}
