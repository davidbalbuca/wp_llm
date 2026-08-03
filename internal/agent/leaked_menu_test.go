package agent

import "testing"

func TestExtractLeakedMenu_ConPreambulo(t *testing.T) {
	s := "¡Hola, Juan! 👋 ¿Deseas repetir tu pedido?\n\n{\n  \"cuerpo\": \"¿Qué deseas hacer?\",\n  \"opciones\": [\"Repetir lo mismo\", \"Cambiar el pedido\"]\n}"
	cuerpo, opciones, preamble, ok := extractLeakedMenu(s)
	if !ok {
		t.Fatalf("esperaba detectar el menú narrado")
	}
	if cuerpo != "¿Qué deseas hacer?" {
		t.Fatalf("cuerpo inesperado: %q", cuerpo)
	}
	if len(opciones) != 2 || opciones[0] != "Repetir lo mismo" {
		t.Fatalf("opciones inesperadas: %v", opciones)
	}
	if preamble != "¡Hola, Juan! 👋 ¿Deseas repetir tu pedido?" {
		t.Fatalf("preámbulo inesperado: %q", preamble)
	}
}

func TestExtractLeakedMenu_ConCercosCodigo(t *testing.T) {
	s := "Elige 👇\n```json\n{\"cuerpo\":\"¿A cuál?\",\"opciones\":[\"Casa\",\"Trabajo\",\"Otra dirección\"]}\n```"
	cuerpo, opciones, preamble, ok := extractLeakedMenu(s)
	if !ok || cuerpo != "¿A cuál?" || len(opciones) != 3 {
		t.Fatalf("no parseó con cercos: ok=%v cuerpo=%q opciones=%v", ok, cuerpo, opciones)
	}
	if preamble != "Elige 👇" {
		t.Fatalf("preámbulo con cercos mal limpiado: %q", preamble)
	}
}

func TestExtractLeakedMenu_TextoNormalNoFalsoPositivo(t *testing.T) {
	if _, _, _, ok := extractLeakedMenu("¡Pedido registrado! Repartidor asignado: Pedro."); ok {
		t.Fatalf("no debía detectar menú en texto normal")
	}
}
