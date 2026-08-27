package llm

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

// El historial que guarda el bot es solo texto, pero DENTRO de un turno el agente reinyecta
// las llamadas a herramientas y sus resultados. Anthropic es estricto con eso, así que la
// traducción es lo que se prueba aquí.

func TestMensajesAnthropicAlternaYEmpiezaEnUsuario(t *testing.T) {
	historial := []*genai.Content{
		{Role: "model", Parts: []*genai.Part{{Text: "hola, soy el bot"}}}, // se descarta: va primero
		{Role: "user", Parts: []*genai.Part{{Text: "quiero gas"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "blanco"}}}, // se fusiona con el anterior
		{Role: "model", Parts: []*genai.Part{{Text: "¿cuántos?"}}},
	}
	msgs := mensajesAnthropic(historial)
	if len(msgs) != 2 {
		t.Fatalf("esperaba 2 mensajes fusionados, hay %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("el primer mensaje debe ser del usuario, es %q", msgs[0].Role)
	}
	if len(msgs[0].Content) != 2 {
		t.Fatalf("los dos turnos seguidos del usuario debían fusionarse: %+v", msgs[0].Content)
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("esperaba assistant, es %q", msgs[1].Role)
	}
}

func TestMensajesAnthropicEmparejaResultadoConSuLlamada(t *testing.T) {
	// Gemini deja el ID vacío (empareja por nombre); Anthropic exige tool_use_id.
	historial := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "quiero 2 blancos"}}},
		{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "verificar_cliente", Args: map[string]any{"identificacion": "0104816269"}}},
			{FunctionCall: &genai.FunctionCall{Name: "registrar_pedido", Args: map[string]any{"cantidad": 2}}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{Name: "verificar_cliente", Response: map[string]any{"result": "cliente existe"}}},
			{FunctionResponse: &genai.FunctionResponse{Name: "registrar_pedido", Response: map[string]any{"result": "pedido 213 creado"}}},
		}},
	}
	msgs := mensajesAnthropic(historial)
	if len(msgs) != 3 {
		t.Fatalf("esperaba 3 mensajes, hay %d", len(msgs))
	}
	usos := msgs[1].Content
	resultados := msgs[2].Content
	if len(usos) != 2 || len(resultados) != 2 {
		t.Fatalf("esperaba 2 tool_use y 2 tool_result, hay %d y %d", len(usos), len(resultados))
	}
	for i := range usos {
		if usos[i]["type"] != "tool_use" || resultados[i]["type"] != "tool_result" {
			t.Fatalf("tipos de bloque mal traducidos: %+v / %+v", usos[i], resultados[i])
		}
		id, _ := usos[i]["id"].(string)
		ref, _ := resultados[i]["tool_use_id"].(string)
		if id == "" || id != ref {
			t.Fatalf("el resultado %d no apunta a su llamada: id=%q tool_use_id=%q", i, id, ref)
		}
	}
	if resultados[1]["content"] != "pedido 213 creado" {
		t.Fatalf("el texto del resultado se perdió: %+v", resultados[1])
	}
}

func TestMensajesAnthropicUsaElIDCuandoVieneDado(t *testing.T) {
	// Cuando el proveedor es Anthropic, las llamadas ya traen su id real y hay que respetarlo.
	historial := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hola"}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "toolu_abc", Name: "mostrar_menu"}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "toolu_abc", Name: "mostrar_menu", Response: map[string]any{"result": "menú enviado"}}}}},
	}
	msgs := mensajesAnthropic(historial)
	if msgs[1].Content[0]["id"] != "toolu_abc" || msgs[2].Content[0]["tool_use_id"] != "toolu_abc" {
		t.Fatalf("no se respetó el id real: %+v / %+v", msgs[1].Content[0], msgs[2].Content[0])
	}
}

func TestHerramientasAnthropicTraduceElEsquema(t *testing.T) {
	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
		Name:        "mostrar_menu",
		Description: "Muestra un menú al cliente.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"cuerpo":   {Type: genai.TypeString, Description: "Texto que acompaña al menú."},
				"opciones": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
				"cantidad": {Type: genai.TypeInteger},
			},
			Required: []string{"cuerpo", "opciones"},
		},
	}}}}

	out := herramientasAnthropic(tools)
	if len(out) != 1 {
		t.Fatalf("esperaba 1 herramienta, hay %d", len(out))
	}
	esquema, _ := out[0]["input_schema"].(map[string]any)
	if esquema["type"] != "object" {
		t.Fatalf("el esquema raíz debe ser object: %+v", esquema)
	}
	props, _ := esquema["properties"].(map[string]any)
	if len(props) != 3 {
		t.Fatalf("esperaba 3 propiedades, hay %d", len(props))
	}
	opciones, _ := props["opciones"].(map[string]any)
	if opciones["type"] != "array" {
		t.Fatalf("opciones debía ser array: %+v", opciones)
	}
	items, _ := opciones["items"].(map[string]any)
	if items["type"] != "string" {
		t.Fatalf("los items debían ser string: %+v", items)
	}
	if props["cantidad"].(map[string]any)["type"] != "integer" {
		t.Fatalf("cantidad debía ser integer: %+v", props["cantidad"])
	}
	// Y sobre todo: tiene que poder serializarse tal cual va al POST.
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("el esquema no es serializable: %v", err)
	}
}

func TestHerramientaSinParametrosSigueSiendoObjeto(t *testing.T) {
	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "cancelar_pedido"}}}}
	esquema, _ := herramientasAnthropic(tools)[0]["input_schema"].(map[string]any)
	if esquema["type"] != "object" {
		t.Fatalf("Anthropic rechaza un input_schema que no sea object: %+v", esquema)
	}
	if _, ok := esquema["properties"]; !ok {
		t.Fatalf("falta properties (aunque vaya vacío): %+v", esquema)
	}
}
