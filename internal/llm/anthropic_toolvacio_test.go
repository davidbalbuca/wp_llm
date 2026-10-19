package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"
)

// El proxy (claude -p) a veces dice stop_reason=tool_use sin mandar la herramienta: se vuelve a pedir,
// en vez de entregar solo el texto ("Estos son los colores que manejamos 👇" sin el menú, 19/10).
func TestToolUseSinHerramientaSeVuelveAPedir(t *testing.T) {
	pedidos := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pedidos++
		w.Header().Set("Content-Type", "application/json")
		if pedidos == 1 {
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Estos son los que manejamos"}],"stop_reason":"tool_use"}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Estos son"},{"type":"tool_use","id":"t1","name":"mostrar_menu","input":{"cuerpo":"¿Cuál?"}}],"stop_reason":"tool_use"}`))
	}))
	defer srv.Close()
	t.Setenv(anthropicURLEnv, srv.URL)
	p, err := NewAnthropic("k", "m", 100, "5m")
	if err != nil {
		t.Fatal(err)
	}
	hist := []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hola"}}}}
	res, err := p.Generate(context.Background(), System{Estatico: "s"}, hist, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pedidos != 2 || len(res.Calls) != 1 || res.Calls[0].Name != "mostrar_menu" {
		t.Fatalf("pedidos=%d calls=%v", pedidos, res.Calls)
	}
}
