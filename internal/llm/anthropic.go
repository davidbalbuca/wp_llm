package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	anthropicURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	// Reintentos ante 429 (límite de tasa) y 5xx. La API puede devolver 529 (sobrecargada)
	// en picos, y un cliente esperando su gas por WhatsApp no debe quedarse sin respuesta
	// por eso.
	anthropicIntentos = 3
)

// anthropicProvider habla directo con la API de Mensajes de Anthropic. No se usa un SDK
// para no arrastrar dependencias nuevas al binario por una prueba: son dos traducciones
// (herramientas e historial) y un POST.
type anthropicProvider struct {
	apiKey    string
	modelo    string
	maxTokens int
	// cacheTTL es cuanto vive el prompt cacheado: "" son los 5 minutos por defecto, "1h" la
	// hora. Cual conviene depende del ritmo real de conversaciones (ver marcarCache).
	cacheTTL string
	http     *http.Client
}

// NewAnthropic crea el proveedor de Claude.
func NewAnthropic(apiKey, modelo string, maxTokens int, cacheTTL string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("falta ANTHROPIC_API_KEY")
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	if cacheTTL != "1h" {
		cacheTTL = "" // cualquier otra cosa: los 5 minutos por defecto de la API
	}
	return &anthropicProvider{
		apiKey:    apiKey,
		modelo:    modelo,
		maxTokens: maxTokens,
		cacheTTL:  cacheTTL,
		// El webhook de WhatsApp ya respondió 200 antes de llegar aquí, así que este timeout
		// solo acota cuánto esperamos al modelo antes de darnos por vencidos.
		http: &http.Client{Timeout: 90 * time.Second},
	}, nil
}

func (a *anthropicProvider) Nombre() string { return "anthropic" }
func (a *anthropicProvider) Modelo() string { return a.modelo }

// --- Formato de la API ---

type anthMensaje struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

type anthPeticion struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    []map[string]any `json:"system,omitempty"`
	Messages  []anthMensaje    `json:"messages"`
	Tools     []map[string]any `json:"tools,omitempty"`
}

type anthBloque struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type anthRespuesta struct {
	Content    []anthBloque `json:"content"`
	StopReason string       `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		// Lo que se escribio en cache (se cobra 1.25x) y lo que se leyo de ella (0.1x).
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *anthropicProvider) Generate(ctx context.Context, system System, history []*genai.Content, tools []*genai.Tool) (Response, error) {
	mensajes := mensajesAnthropic(history)
	if len(mensajes) == 0 {
		return Response{}, fmt.Errorf("no hay mensajes que enviar")
	}
	a.marcarCache(mensajes)
	peticion := anthPeticion{
		Model:     a.modelo,
		MaxTokens: a.maxTokens,
		System:    a.sistemaEnBloques(system),
		Messages:  mensajes,
		Tools:     herramientasAnthropic(tools),
	}
	cuerpo, err := json.Marshal(peticion)
	if err != nil {
		return Response{}, err
	}

	var datos anthRespuesta
	var ultimoErr error
	for intento := 1; intento <= anthropicIntentos; intento++ {
		datos, ultimoErr = a.enviar(ctx, cuerpo)
		if ultimoErr == nil {
			break
		}
		if !esReintentable(ultimoErr) || intento == anthropicIntentos {
			return Response{}, ultimoErr
		}
		espera := time.Duration(intento) * 2 * time.Second
		log.Printf("[llm] anthropic reintento %d/%d en %s: %v", intento, anthropicIntentos, espera, ultimoErr)
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(espera):
		}
	}

	// Consumo a la vista: es lo que se factura, y con el bot en producción conviene poder
	// mirarlo en el log sin entrar a la consola de Anthropic.
	log.Printf("[llm] anthropic %s entrada=%d cache_lee=%d cache_escribe=%d salida=%d motivo=%s",
		a.modelo, datos.Usage.InputTokens, datos.Usage.CacheReadInputTokens,
		datos.Usage.CacheCreationInputTokens, datos.Usage.OutputTokens, datos.StopReason)

	var textos []string
	var calls []*genai.FunctionCall
	var partes []*genai.Part
	for _, b := range datos.Content {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				textos = append(textos, b.Text)
				partes = append(partes, &genai.Part{Text: b.Text})
			}
		case "tool_use":
			args := b.Input
			if args == nil {
				args = map[string]any{}
			}
			llamada := &genai.FunctionCall{ID: b.ID, Name: b.Name, Args: args}
			calls = append(calls, llamada)
			partes = append(partes, &genai.Part{FunctionCall: llamada})
		}
	}
	if len(partes) == 0 {
		return Response{}, nil
	}
	return Response{
		Text:    strings.TrimSpace(strings.Join(textos, "\n")),
		Calls:   calls,
		Content: &genai.Content{Role: "model", Parts: partes},
	}, nil
}

// errorAPI distingue lo que vale la pena reintentar de lo que no: una clave inválida no
// mejora reintentando.
type errorAPI struct {
	codigo  int
	mensaje string
}

func (e *errorAPI) Error() string {
	return fmt.Sprintf("anthropic HTTP %d: %s", e.codigo, e.mensaje)
}

func esReintentable(err error) bool {
	e, ok := err.(*errorAPI)
	if !ok {
		return true // error de red: sí se reintenta
	}
	return e.codigo == 429 || e.codigo >= 500
}

func (a *anthropicProvider) enviar(ctx context.Context, cuerpo []byte) (anthRespuesta, error) {
	var datos anthRespuesta
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(cuerpo))
	if err != nil {
		return datos, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	if a.cacheTTL == "1h" {
		req.Header.Set("anthropic-beta", "extended-cache-ttl-2025-04-11")
	}

	res, err := a.http.Do(req)
	if err != nil {
		return datos, err
	}
	defer res.Body.Close()
	crudo, err := io.ReadAll(res.Body)
	if err != nil {
		return datos, err
	}
	if res.StatusCode != http.StatusOK {
		detalle := strings.TrimSpace(string(crudo))
		if len(detalle) > 400 {
			detalle = detalle[:400]
		}
		return datos, &errorAPI{codigo: res.StatusCode, mensaje: detalle}
	}
	if err := json.Unmarshal(crudo, &datos); err != nil {
		return datos, fmt.Errorf("respuesta ilegible de anthropic: %w", err)
	}
	if datos.Error != nil {
		return datos, fmt.Errorf("anthropic: %s", datos.Error.Message)
	}
	return datos, nil
}

// --- Cacheo del prompt ---
//
// Anthropic cobra a la decima parte el texto que ya vio, siempre que el PRINCIPIO del prompt
// sea identico byte a byte. En este bot la mayor parte de cada llamada es exactamente eso:
// las reglas del bot y las diez herramientas viajan enteras en cada mensaje, y se repiten
// unas seis veces por conversacion.
//
// Cachear NO cambia lo que el modelo lee ni lo que contesta: es el mismo prompt, cobrado
// distinto. Solo cambia el precio.
//
// Se marcan dos cortes: uno al final de la parte fija del sistema (que arrastra tambien a las
// herramientas, porque van antes en el prompt) y otro al final del ultimo mensaje, para que la
// conversacion acumulada tampoco se pague entera en cada vuelta.

// bloqueCache devuelve la marca de cacheo con el TTL configurado.
func (a *anthropicProvider) bloqueCache() map[string]any {
	cc := map[string]any{"type": "ephemeral"}
	if a.cacheTTL != "" {
		cc["ttl"] = a.cacheTTL
	}
	return cc
}

// sistemaEnBloques parte el prompt de sistema en fijo (cacheado) y volatil.
func (a *anthropicProvider) sistemaEnBloques(system System) []map[string]any {
	var out []map[string]any
	if fijo := system.Estatico; fijo != "" {
		bloque := map[string]any{"type": "text", "text": fijo}
		// Por debajo del minimo cacheable la API ignora la marca; no se pone y listo.
		if len(fijo) > 8000 {
			bloque["cache_control"] = a.bloqueCache()
		}
		out = append(out, bloque)
	}
	if system.Volatil != "" {
		out = append(out, map[string]any{"type": "text", "text": system.Volatil})
	}
	return out
}

// marcarCache marca el final de la conversacion para que la proxima vuelta no vuelva a pagar
// el historial completo. Cada llamada solo escribe en cache lo que se agrego desde la anterior.
func (a *anthropicProvider) marcarCache(mensajes []anthMensaje) {
	if len(mensajes) == 0 {
		return
	}
	ultimo := mensajes[len(mensajes)-1]
	if len(ultimo.Content) == 0 {
		return
	}
	ultimo.Content[len(ultimo.Content)-1]["cache_control"] = a.bloqueCache()
}

// --- Traducciones ---

// herramientasAnthropic convierte las declaraciones de genai al formato de Anthropic.
func herramientasAnthropic(tools []*genai.Tool) []map[string]any {
	var out []map[string]any
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			esquema := esquemaJSON(fd.Parameters)
			// Anthropic exige que el esquema de entrada sea un objeto con propiedades,
			// aunque la herramienta no reciba nada.
			esquema["type"] = "object"
			if _, ok := esquema["properties"]; !ok {
				esquema["properties"] = map[string]any{}
			}
			out = append(out, map[string]any{
				"name":         fd.Name,
				"description":  fd.Description,
				"input_schema": esquema,
			})
		}
	}
	return out
}

// esquemaJSON pasa un genai.Schema a JSON Schema plano, que es lo que espera Anthropic.
func esquemaJSON(s *genai.Schema) map[string]any {
	out := map[string]any{}
	if s == nil {
		return out
	}
	if t := strings.ToLower(string(s.Type)); t != "" && t != "type_unspecified" {
		out["type"] = t
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if s.Items != nil {
		out["items"] = esquemaJSON(s.Items)
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for nombre, sub := range s.Properties {
			props[nombre] = esquemaJSON(sub)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	return out
}

// mensajesAnthropic traduce el historial de genai al formato de Anthropic.
//
// Tres reglas de Anthropic que Gemini no tiene y hay que respetar aquí: cada tool_result
// debe apuntar con tool_use_id a su tool_use (Gemini empareja por nombre y deja el id
// vacío), los roles deben alternar, y el primer mensaje tiene que ser del usuario.
func mensajesAnthropic(history []*genai.Content) []anthMensaje {
	var out []anthMensaje
	var idsUltimoTurno []string // ids de los tool_use del último turno del asistente
	autoID := 0

	for _, c := range history {
		if c == nil {
			continue
		}
		rol := "user"
		if c.Role == "model" || c.Role == "assistant" {
			rol = "assistant"
		}
		var bloques []map[string]any
		var idsEsteTurno []string
		resultados := 0

		for _, p := range c.Parts {
			switch {
			case p == nil:
				continue
			case p.FunctionCall != nil:
				id := p.FunctionCall.ID
				if id == "" {
					autoID++
					id = fmt.Sprintf("toolu_local_%d", autoID)
				}
				idsEsteTurno = append(idsEsteTurno, id)
				args := p.FunctionCall.Args
				if args == nil {
					args = map[string]any{}
				}
				bloques = append(bloques, map[string]any{
					"type":  "tool_use",
					"id":    id,
					"name":  p.FunctionCall.Name,
					"input": args,
				})
			case p.FunctionResponse != nil:
				id := p.FunctionResponse.ID
				if id == "" && resultados < len(idsUltimoTurno) {
					id = idsUltimoTurno[resultados] // van en el mismo orden que las llamadas
				}
				resultados++
				if id == "" {
					continue // sin id no es un tool_result válido; se descarta
				}
				bloques = append(bloques, map[string]any{
					"type":        "tool_result",
					"tool_use_id": id,
					"content":     textoResultado(p.FunctionResponse),
				})
			case strings.TrimSpace(p.Text) != "":
				bloques = append(bloques, map[string]any{"type": "text", "text": p.Text})
			}
		}

		if rol == "assistant" {
			idsUltimoTurno = idsEsteTurno
		}
		if len(bloques) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == rol {
			out[n-1].Content = append(out[n-1].Content, bloques...) // fusiona para que alternen
			continue
		}
		out = append(out, anthMensaje{Role: rol, Content: bloques})
	}

	for len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out
}

// textoResultado saca el texto que el agente devolvió desde la herramienta.
func textoResultado(fr *genai.FunctionResponse) string {
	if fr.Response != nil {
		if v, ok := fr.Response["result"]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		if b, err := json.Marshal(fr.Response); err == nil {
			return string(b)
		}
	}
	return "(sin resultado)"
}
