package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO REAL 593995446872 (2-oct): el código mandó la bienvenida ("Soy *Ubi*") y 5 segundos
// después el modelo redactó el menú de color con "📋 ¡Hola! 👋 Con gusto te ayudo…". Dos saludos
// seguidos. Los 7 casos del 2-oct son TODOS menús (menu=True en el análisis forense).
//
// El candado original (saludounico.go) solo actuaba sobre `reply`, así que los cuerpos de menú
// salían intactos. Este test verifica que limpiarSaludoDelCuerpoDeMenu sí quita el saludo cuando
// el código recién se presentó.
func TestLimpiarSaludoDelCuerpoDeMenuCasoReal(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593995446872"

	// Paso 1: el código se presenta (bienvenida.go). Se guarda como turno "model" en el historial.
	bienvenida := "¡Buenos días, tyty! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor de gas más cercano a ti, en minutos."
	store.AppendModel(from, bienvenida)

	// Paso 2: el modelo redacta el cuerpo del menú de color CON saludo repetido.
	cuerpoOriginal := "📋 ¡Hola! 👋 Con gusto te ayudo con tu pedido de gas. ¿Qué color o marca de cilindro de 15kg necesitas?"

	// Paso 3: el candado debe quitarlo ANTES de mandar el menú.
	limpio := ag.limpiarSaludoDelCuerpoDeMenu(from, cuerpoOriginal)

	if strings.Contains(limpio, "¡Hola!") {
		t.Errorf("el candado NO quitó el saludo repetido.\nORIG: %q\nLIMPIO: %q", cuerpoOriginal, limpio)
	}
	if !strings.Contains(limpio, "¿Qué color o marca") {
		t.Errorf("el candado BORRÓ la pregunta útil (solo debía quitar el saludo).\nLIMPIO: %q", limpio)
	}

	esperado := "¿Qué color o marca de cilindro de 15kg necesitas?"
	if limpio != esperado {
		t.Errorf("resultado inesperado.\nesperado: %q\nobtenido: %q", esperado, limpio)
	}
}

// Si el código NO se presentó, el modelo PUEDE saludar en el menú: no hay nada que quitar.
func TestLimpiarSaludoDelCuerpoDeMenuSinBienvenidaPrevia(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593999999999"

	cuerpo := "📋 ¡Hola! 👋 ¿Qué color necesitas?"
	limpio := ag.limpiarSaludoDelCuerpoDeMenu(from, cuerpo)

	if limpio != cuerpo {
		t.Errorf("el candado NO debía tocar nada (el código no se presentó).\nORIG: %q\nLIMPIO: %q", cuerpo, limpio)
	}
}

// Si el cuerpo del menú ERA solo el saludo (el modelo no redactó pregunta), se deja intacto para
// no mandar un menú vacío. Es mejor un saludo repetido que un menú sin contexto.
func TestLimpiarSaludoDelCuerpoDeMenuEraSoloSaludo(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900000001"

	bienvenida := "¡Hola, David! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor..."
	store.AppendModel(from, bienvenida)

	// El modelo SOLO saludó en el cuerpo del menú, sin pregunta.
	cuerpo := "📋 ¡Hola, David! 👋 Con gusto te ayudo."
	limpio := ag.limpiarSaludoDelCuerpoDeMenu(from, cuerpo)

	// Se deja el original: un menú sin cuerpo es peor que un saludo repetido.
	if limpio != cuerpo {
		t.Errorf("el candado NO debía vaciar el menú (era solo saludo).\nORIG: %q\nLIMPIO: %q", cuerpo, limpio)
	}
}
