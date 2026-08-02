package whatsapp

import "testing"

func TestBuildInteractiveMenu_Buttons(t *testing.T) {
	m, err := buildInteractiveMenu("¿Qué cilindro?", []string{"Blanco", "Amarillo", "Naranja"})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if m["type"] != "button" {
		t.Fatalf("con 3 opciones esperaba botones, got %v", m["type"])
	}
	btns := m["action"].(map[string]any)["buttons"].([]map[string]any)
	if len(btns) != 3 {
		t.Fatalf("esperaba 3 botones, got %d", len(btns))
	}
}

func TestBuildInteractiveMenu_List(t *testing.T) {
	m, err := buildInteractiveMenu("Elige", []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if m["type"] != "list" {
		t.Fatalf("con 5 opciones esperaba lista, got %v", m["type"])
	}
	rows := m["action"].(map[string]any)["sections"].([]map[string]any)[0]["rows"].([]map[string]any)
	if len(rows) != 5 {
		t.Fatalf("esperaba 5 filas, got %d", len(rows))
	}
}

func TestBuildInteractiveMenu_TooMany(t *testing.T) {
	if _, err := buildInteractiveMenu("x", make([]string, 11)); err == nil {
		t.Fatalf("esperaba error con 11 opciones")
	}
}

func TestParseIncoming_ButtonReply(t *testing.T) {
	payload := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"from":"593999","type":"interactive","interactive":{"type":"button_reply","button_reply":{"id":"Amarillo (Duragas)","title":"Amarillo"}}}]}}]}]}`)
	inc, ok := ParseIncoming(payload)
	if !ok || !inc.IsText {
		t.Fatalf("esperaba mensaje de texto, got ok=%v isText=%v", ok, inc.IsText)
	}
	if inc.Text != "Amarillo (Duragas)" {
		t.Fatalf("esperaba el id completo como texto, got %q", inc.Text)
	}
}

func TestParseIncoming_ListReply(t *testing.T) {
	payload := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"from":"593999","type":"interactive","interactive":{"type":"list_reply","list_reply":{"id":"Casa","title":"Casa"}}}]}}]}]}`)
	inc, ok := ParseIncoming(payload)
	if !ok || inc.Text != "Casa" {
		t.Fatalf("esperaba 'Casa', got ok=%v text=%q", ok, inc.Text)
	}
}
