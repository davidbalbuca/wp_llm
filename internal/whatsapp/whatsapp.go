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
	"strings"
	"time"

	"wp-llm-gas/internal/config"
)

// coordsRe captura un par (lat, lng) dentro de un texto (enlaces de Google Maps, pares sueltos):
// muchos clientes pegan la ubicación como URL en vez del adjunto nativo de WhatsApp.
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

// linkCortoRe captura la URL acortada de Google Maps DENTRO de un texto. Va anclada al ESQUEMA
// más el host a propósito: buscar el host como substring suelto hacía que cualquier URL que lo
// llevara en un parámetro —"https://otrositio.com/x?ref=maps.app.goo.gl"— pasara el filtro, y el
// bot terminaba haciéndole una petición HTTP a un servidor ajeno elegido por quien escribe.
// El host va precedido por INICIO o ESPACIO (grupo 1) y seguido por "/", "?" o fin: así el
// esquema puede faltar —el cliente pega "maps.app.goo.gl/abc" sin https— sin que el host cuele
// dentro de otra URL, y "maps.app.goo.gl.atacante.net" no casa porque después del host viene un
// punto y no un separador de ruta.
var linkCortoRe = regexp.MustCompile(`(?i)(?:^|\s)((?:https?://)?(?:maps\.app\.goo\.gl|goo\.gl/maps)(?:[/?][^\s]*)?)(?:\s|$)`)

// esHostDeGoogle dice si el host pertenece a Google (dominio exacto o subdominio). Se compara
// por SUFIJO con el punto delante —".google.com", no "google.com" suelto— para que un host como
// "google.com.atacante.net" no cuele.
func esHostDeGoogle(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	// La etiqueta final tiene que ser de Google. Un HasPrefix("google.") dejaba entrar
	// "google.com.atacante.net", que es justo el truco que este filtro debe parar: lo que manda
	// es cómo TERMINA el host, no cómo empieza.
	for _, d := range []string{"google.com", "goo.gl", "google"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	// Dominios de país: google.es, google.com.mx, google.com.ec… Se acepta solo si el host
	// EMPIEZA por "google." (o ".google.") y su TLD es corto, sin etiquetas extra detrás.
	partes := strings.Split(host, ".")
	for i, p := range partes {
		if p != "google" {
			continue
		}
		// Tras "google" solo pueden quedar 1 o 2 etiquetas (com, com.ec, es…).
		if n := len(partes) - i - 1; n >= 1 && n <= 2 {
			return true
		}
	}
	return false
}

// clienteDeMaps es el cliente HTTP con el que se resuelven los acortadores. Vive aparte para que
// la prueba del filtro de redirect use EXACTAMENTE el mismo, y no una copia que podría divergir.
func clienteDeMaps() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			// El destino del redirect también lo elige quien escribe: un acortador puede apuntar
			// a donde sea. Sin este filtro, un link con forma de Maps llevaba al bot a pedirle una
			// página a un host cualquiera —incluida la red interna del server—. Solo se sigue a
			// dominios de Google, que es lo único a lo que resuelve un link de Maps legítimo.
			if !esHostDeGoogle(req.URL.Hostname()) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// ExtraerLinkCortoDeMaps devuelve la URL acortada de Maps que haya en el texto, o "" si no hay.
// Se EXTRAE en vez de usar el mensaje entero porque el cliente casi nunca manda el link solo:
// escribe "mira, aquí estoy: <link>". Pasarle esa frase completa al cliente HTTP no resolvía nada
// —la petición fallaba— y el bot le pedía el pin nativo como si el link no sirviera.
func ExtraerLinkCortoDeMaps(text string) string {
	m := linkCortoRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(m) < 2 {
		return ""
	}
	link := m[1]
	// El esquema es opcional en lo que escribe el cliente, pero obligatorio para pedirlo.
	if !strings.HasPrefix(strings.ToLower(link), "http") {
		link = "https://" + link
	}
	return link
}

// EsLinkCortoDeMaps dice si el texto TRAE un link acortado de Google Maps
// (maps.app.goo.gl, goo.gl/maps). Estos NO traen coordenadas en la URL y hay
// que resolver el redirect para obtenerlas; si no, entran como texto y el
// modelo los ignora (caso 593959499118: mandó maps.app.goo.gl y el bot dijo
// "ya avisé al repartidor" sin ubicación).
func EsLinkCortoDeMaps(text string) bool {
	return ExtraerLinkCortoDeMaps(text) != ""
}

// ResolverLinkCortoDeMaps sigue el redirect de un link acortado de Maps y
// extrae las coordenadas de la URL final (que sí las trae como ?q= o @lat,lng).
// Timeout corto: si no resuelve, el llamador pide el pin nativo.
//
// Recibe el TEXTO del cliente, no una URL limpia, y se queda solo con el link de Maps que
// contenga: el resto del mensaje no llega nunca al cliente HTTP.
func ResolverLinkCortoDeMaps(texto string) (lat, lng float64, ok bool) {
	link := ExtraerLinkCortoDeMaps(texto)
	if link == "" {
		return 0, 0, false
	}
	resp, err := clienteDeMaps().Get(link)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	finalURL := resp.Request.URL.String()
	if lat, lng, ok := ParseCoordsFromText(finalURL); ok {
		return lat, lng, true
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if lat, lng, ok := ParseCoordsFromText(string(body)); ok {
		return lat, lng, true
	}
	return 0, 0, false
}

// sendPayload envía un payload ya armado a la Graph API de Meta (POST /messages).
func sendPayload(cfg config.Config, payload map[string]any) error {
	// Sin credenciales no se sale a internet. En producción nunca faltan (config las exige al
	// arrancar), así que esto solo se cumple en los TESTS: sin el corte, cada prueba que ejerce
	// un menú dispara un POST real a la API de Meta —que no puede funcionar sin token, y que no
	// tiene nada que hacer saliendo de una prueba unitaria—.
	if cfg.WhatsAppToken == "" || cfg.PhoneNumberID == "" {
		return fmt.Errorf("WhatsApp no está configurado (falta el token o el phone number id)")
	}
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

// SendMenu envía un menú interactivo tappable: botones si <=3 opciones, lista si 4-10. El "id"
// de cada opción lleva el texto completo (el título visible se trunca) y vuelve en el webhook al
// elegir. Error si <1 o >10 opciones (el llamador cae a texto normal).
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
				// Contacts trae el nombre que el propio cliente puso en su perfil de WhatsApp.
				// Es mejor dato que cualquier nombre inferido del texto del mensaje.
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
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
	// PerfilNombre es el nombre del perfil de WhatsApp del cliente (vacío si Meta no lo manda:
	// no viene en todos los eventos). Lo escribió la propia persona, así que se prefiere a
	// deducirlo del mensaje.
	PerfilNombre string
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
	// El nombre del perfil viene aparte de los mensajes, en contacts[]. Se resuelve una vez y
	// se adjunta al Incoming sea cual sea el tipo de mensaje.
	perfil := ""
	for _, c := range p.Entry[0].Changes[0].Value.Contacts {
		if c.WaID == m.From || c.WaID == "" {
			perfil = strings.TrimSpace(c.Profile.Name)
			break
		}
	}
	switch {
	case m.Type == "location" && m.Location != nil:
		return Incoming{
			From:         m.From,
			HasLocation:  true,
			Latitude:     m.Location.Latitude,
			Longitude:    m.Location.Longitude,
			PerfilNombre: perfil,
		}, true
	case m.Type == "text":
		return Incoming{From: m.From, Text: m.Text.Body, IsText: true, PerfilNombre: perfil}, true
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
			return Incoming{From: m.From, Text: elegido, IsText: true, PerfilNombre: perfil}, true
		}
		return Incoming{From: m.From, IsText: false, PerfilNombre: perfil}, true
	default:
		return Incoming{From: m.From, IsText: false, PerfilNombre: perfil}, true // mensaje no-texto
	}
}

// VerifyWebhook resuelve el handshake GET de Meta (hub.mode/hub.verify_token/hub.challenge).
func VerifyWebhook(cfg config.Config, mode, token, challenge string) (string, bool) {
	if mode == "subscribe" && token == cfg.VerifyToken {
		return challenge, true
	}
	return "", false
}
