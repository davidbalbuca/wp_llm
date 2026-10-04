package whatsapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"wp-llm-gas/internal/config"
)

// Con WHATSAPP_API_BASE apuntando a otro servidor (cmd/simulador en local), los mensajes del bot
// tienen que ir ahí, con la misma ruta y el mismo cuerpo que recibiría Meta. Si esto se rompe, el
// simulador deja de ver las respuestas o, peor, una prueba local termina escribiéndole a un
// cliente real.
func TestSendText_UsaLaBaseConfigurada(t *testing.T) {
	var ruta, auth string
	var cuerpo map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ruta = r.URL.Path
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &cuerpo)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		WhatsAppToken:   "token-de-prueba",
		PhoneNumberID:   "123",
		GraphAPIVersion: "v21.0",
		WhatsAppAPIBase: srv.URL,
	}
	if err := SendText(cfg, "593900000099", "hola"); err != nil {
		t.Fatalf("SendText devolvió error: %v", err)
	}
	if ruta != "/v21.0/123/messages" {
		t.Errorf("ruta = %q, se esperaba /v21.0/123/messages", ruta)
	}
	if auth != "Bearer token-de-prueba" {
		t.Errorf("Authorization = %q", auth)
	}
	if cuerpo["to"] != "593900000099" || cuerpo["type"] != "text" {
		t.Errorf("cuerpo inesperado: %v", cuerpo)
	}
}

// Sin WHATSAPP_API_BASE (producción, y cualquier Config armado a mano) se sigue usando la Graph
// API real: el cambio no puede alterar a dónde escribe el bot en prod.
func TestGraphAPIBase_PorDefectoEsMeta(t *testing.T) {
	if got := (config.Config{}).GraphAPIBase(); got != "https://graph.facebook.com" {
		t.Errorf("GraphAPIBase() = %q, se esperaba https://graph.facebook.com", got)
	}
}
