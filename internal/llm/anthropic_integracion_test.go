package llm

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/genai"
)

// Prueba contra la API DE VERDAD. Se salta sola si no hay clave en el entorno, así que no
// molesta en el día a día ni gasta nada; se corre a mano cuando se toca la traducción:
//
//	ANTHROPIC_API_KEY=... go test ./internal/llm/ -run Integracion -v
func TestIntegracionAnthropicLlamaHerramienta(t *testing.T) {
	clave := os.Getenv("ANTHROPIC_API_KEY")
	if clave == "" {
		t.Skip("sin ANTHROPIC_API_KEY: se omite la prueba contra la API real")
	}
	modelo := os.Getenv("ANTHROPIC_MODEL")
	if modelo == "" {
		modelo = "claude-haiku-4-5-20251001"
	}
	p, err := NewAnthropic(clave, modelo, 512)
	if err != nil {
		t.Fatalf("no se pudo crear el proveedor: %v", err)
	}

	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
		Name:        "registrar_pedido",
		Description: "Registra el pedido de gas del cliente.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"color":    {Type: genai.TypeString, Description: "Color del cilindro."},
				"cantidad": {Type: genai.TypeInteger, Description: "Cuántos cilindros."},
			},
			Required: []string{"color", "cantidad"},
		},
	}}}}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Primer turno: el modelo debería querer llamar a la herramienta.
	historial := []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Quiero 2 cilindros blancos, ya tienes todos mis datos."}}}}
	resp, err := p.Generate(ctx, "Eres el bot de pedidos de gas de Ubi. Si el cliente pide gas y da color y cantidad, llama a registrar_pedido de una vez.", historial, tools)
	if err != nil {
		t.Fatalf("la llamada a la API falló: %v", err)
	}
	if len(resp.Calls) == 0 {
		t.Fatalf("esperaba una llamada a registrar_pedido, llegó texto: %q", resp.Text)
	}
	c := resp.Calls[0]
	if c.Name != "registrar_pedido" || c.ID == "" {
		t.Fatalf("llamada inesperada: %+v", c)
	}
	t.Logf("llamó %s con %v (id=%s)", c.Name, c.Args, c.ID)

	// Segundo turno: se le devuelve el resultado, igual que hace el agente. Esto es lo que
	// valida el emparejamiento tool_use/tool_result contra la API real.
	historial = append(historial, resp.Content)
	historial = append(historial, &genai.Content{Role: "user", Parts: []*genai.Part{{
		FunctionResponse: &genai.FunctionResponse{
			ID: c.ID, Name: c.Name,
			Response: map[string]any{"result": "Pedido 999 creado. El repartidor Juan llega en 15 minutos."},
		},
	}}})
	resp2, err := p.Generate(ctx, "Eres el bot de pedidos de gas de Ubi. Confirma al cliente en una frase corta.", historial, tools)
	if err != nil {
		t.Fatalf("el segundo turno falló (emparejamiento de tool_result): %v", err)
	}
	if resp2.Text == "" {
		t.Fatalf("esperaba una confirmación en texto, llegó: %+v", resp2)
	}
	t.Logf("respuesta final: %s", resp2.Text)
}
