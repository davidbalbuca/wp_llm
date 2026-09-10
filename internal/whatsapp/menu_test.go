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

// El payload REAL de Guillermo Pacheco (10/09): su nombre venía en contacts[].profile.name y el
// parser lo tiraba, así que el bot lo dedujo del texto y lo llamó "Brito" tres veces.
func TestParseIncoming_NombreDelPerfil(t *testing.T) {
	payload := []byte(`{"entry":[{"changes":[{"value":{"contacts":[{"profile":{"name":"Guillermo Pacheco"},"wa_id":"593963646872"}],"messages":[{"from":"593963646872","type":"text","text":{"body":"Brito por favor 2 cilindros a la iglesia"}}]}}]}]}`)
	inc, ok := ParseIncoming(payload)
	if !ok {
		t.Fatal("no se parseó el mensaje")
	}
	if inc.PerfilNombre != "Guillermo Pacheco" {
		t.Errorf("se perdió el nombre del perfil: %q", inc.PerfilNombre)
	}
}

// La ubicación llega en un payload aparte: el nombre tiene que viajar igual, o el bot lo pierde
// justo en el mensaje donde va a registrar el pedido.
func TestParseIncoming_NombreDelPerfilEnUbicacion(t *testing.T) {
	payload := []byte(`{"entry":[{"changes":[{"value":{"contacts":[{"profile":{"name":"Guillermo Pacheco"},"wa_id":"593963646872"}],"messages":[{"from":"593963646872","type":"location","location":{"latitude":-2.915673,"longitude":-79.039159}}]}}]}]}`)
	inc, ok := ParseIncoming(payload)
	if !ok || !inc.HasLocation {
		t.Fatalf("esperaba ubicación, got ok=%v hasLoc=%v", ok, inc.HasLocation)
	}
	if inc.PerfilNombre != "Guillermo Pacheco" {
		t.Errorf("se perdió el nombre del perfil en la ubicación: %q", inc.PerfilNombre)
	}
}

// Meta no manda contacts[] en todos los eventos: sin nombre no se rompe nada.
func TestParseIncoming_SinContactos(t *testing.T) {
	payload := []byte(`{"entry":[{"changes":[{"value":{"messages":[{"from":"593999","type":"text","text":{"body":"hola"}}]}}]}]}`)
	inc, ok := ParseIncoming(payload)
	if !ok || inc.Text != "hola" {
		t.Fatalf("esperaba 'hola', got ok=%v text=%q", ok, inc.Text)
	}
	if inc.PerfilNombre != "" {
		t.Errorf("se inventó un nombre: %q", inc.PerfilNombre)
	}
}
