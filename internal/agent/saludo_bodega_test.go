package agent

import "testing"

// Primera prueba del bot de bodegas (19/10): tras la presentación del código, el modelo saludó otra vez
// con "¡Buenas, Josué! 👋 Soy de Bodega Prueba." y el candado no lo reconoció ("buenas" a secas, el
// nombre con tilde y la presentación con la marca de la bodega).
func TestSeQuitaElSaludoRepetidoConBuenasYLaMarcaDeLaBodega(t *testing.T) {
	got := quitarSaludoDuplicado("¡Buenas, Josué! 👋 Soy de Bodega Prueba.\n\nMira, hoy domingo no tenemos servicio 😔", "Josue")
	if got != "Mira, hoy domingo no tenemos servicio 😔" {
		t.Fatalf("got %q", got)
	}
	if got := quitarSaludoDuplicado("Buenas noticias: tu gas ya salió 🚚"); got != "Buenas noticias: tu gas ya salió 🚚" {
		t.Fatalf("un mensaje que no es saludo no se toca: %q", got)
	}
	if got := quitarSaludoDuplicado("¡Buenas! 👋 ¿De qué color es tu cilindro?"); got != "¿De qué color es tu cilindro?" {
		t.Fatalf("got %q", got)
	}
}
