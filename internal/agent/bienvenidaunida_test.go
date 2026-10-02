package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO REAL 593963518172 (02/10): el cliente escribió "Gas" y recibió DOS mensajes —la
// presentación y, seis segundos después, el menú de colores—. El dueño pidió uno solo con todo.
// La presentación tiene que viajar en el CUERPO del menú.
func TestBienvenidaViajaEnElCuerpoDelMenu(t *testing.T) {
	store := conversation.NewMemStore()
	from := "593963518172"

	var cuerpoEnviado string
	ag := &Agent{
		store: store,
		enviarMenu: func(_, cuerpo string, _ []string) error {
			cuerpoEnviado = cuerpo
			return nil
		},
	}

	bienvenida := "¡Buenas tardes, Leandro! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor de gas más cercano a ti."
	ag.DejarBienvenidaPendiente(from, bienvenida)

	if err := ag.mandarMenu(from, "¿Qué cilindro de 15kg necesitas?", []string{"Blanco", "Amarillo"}); err != nil {
		t.Fatalf("mandarMenu: %v", err)
	}

	if !strings.Contains(cuerpoEnviado, "Soy *Ubi*") {
		t.Errorf("la presentación NO viajó en el cuerpo del menú:\n%q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, "¿Qué cilindro de 15kg necesitas?") {
		t.Errorf("se perdió la pregunta del menú:\n%q", cuerpoEnviado)
	}
	// La presentación va PRIMERO: quién somos y luego qué se pregunta.
	if strings.Index(cuerpoEnviado, "Soy *Ubi*") > strings.Index(cuerpoEnviado, "¿Qué cilindro") {
		t.Errorf("la presentación tiene que ir ANTES de la pregunta:\n%q", cuerpoEnviado)
	}
	// Y la marca se consume: no puede repetirse en el mensaje siguiente.
	if _, hay := ag.HayBienvenidaPendiente(from); hay {
		t.Error("la marca sigue pendiente después de entregarla; se repetiría")
	}
}

// Cuando el turno acaba en TEXTO (el cliente preguntó el precio, no pidió gas), la presentación
// viaja igual, pero en el texto de la respuesta.
func TestBienvenidaViajaEnElTextoDeLaRespuesta(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900000011"

	ag.DejarBienvenidaPendiente(from, "¡Hola! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor más cercano.")
	salida := ag.conBienvenida(from, "El cilindro de 15KG cuesta $3.25 😊")

	if !strings.Contains(salida, "Soy *Ubi*") {
		t.Errorf("la presentación no viajó en el texto:\n%q", salida)
	}
	if !strings.Contains(salida, "$3.25") {
		t.Errorf("se perdió la respuesta al cliente:\n%q", salida)
	}
	if _, hay := ag.HayBienvenidaPendiente(from); hay {
		t.Error("la marca no se consumió")
	}
}

// Sin presentación pendiente no se toca nada: el cliente a media conversación no se saluda otra vez.
func TestSinBienvenidaPendienteNoSeTocaElMensaje(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900000012"

	const original = "¿Cuántos cilindros necesitas?"
	if got := ag.conBienvenida(from, original); got != original {
		t.Errorf("se modificó el mensaje sin haber presentación pendiente:\n%q", got)
	}
}

// LA REGRESIÓN QUE MÁS IMPORTA. El candado del doble saludo (saludounico.go) decide mirando si el
// último turno del modelo es la presentación. Al dejar de enviarla con avisarCliente —que hacía el
// AppendModel— ese registro había que rehacerlo a mano: si falta, yaSePresentoElCodigo devuelve
// false, el candado no actúa y vuelve el doble saludo arreglado hoy mismo.
func TestDejarBienvenidaPendienteRegistraElTurnoParaElCandado(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900000013"

	ag.DejarBienvenidaPendiente(from, "¡Hola, Ana! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor.")

	if !ag.yaSePresentoElCodigo(from) {
		t.Fatal("el candado del doble saludo NO ve la presentación: volvería a saludar dos veces")
	}

	// Y el candado la usa: quita el saludo repetido del cuerpo de un menú.
	limpio := ag.limpiarSaludoDelCuerpoDeMenu(from, "📋 ¡Hola, Ana! 👋 ¿Qué color necesitas?")
	if strings.Contains(limpio, "¡Hola, Ana!") {
		t.Errorf("el candado no quitó el saludo repetido:\n%q", limpio)
	}
}

// Los dos arreglos de hoy juntos, en el orden real del turno: el código se presenta, el modelo
// redacta un menú saludando otra vez → sale UN mensaje, con UN saludo (el del código) y la pregunta.
func TestUnSoloMensajeConUnSoloSaludo(t *testing.T) {
	store := conversation.NewMemStore()
	from := "593963518172"

	var cuerpoEnviado string
	ag := &Agent{
		store: store,
		enviarMenu: func(_, cuerpo string, _ []string) error {
			cuerpoEnviado = cuerpo
			return nil
		},
	}

	ag.DejarBienvenidaPendiente(from, "¡Buenas tardes, Leandro! 👋 Soy *Ubi* 🔥\n\nTe conecto con el repartidor más cercano.")

	// El modelo redacta el menú saludando por su cuenta (lo que hacía de verdad).
	cuerpoDelModelo := ag.limpiarSaludoDelCuerpoDeMenu(from, "📋 ¡Hola, Leandro! 👋 Con gusto te ayudo. ¿Qué color necesitas?")
	if err := ag.mandarMenu(from, cuerpoDelModelo, []string{"Blanco", "Amarillo"}); err != nil {
		t.Fatalf("mandarMenu: %v", err)
	}

	// UN saludo: el del código. El del modelo se fue.
	if n := strings.Count(cuerpoEnviado, "👋"); n != 1 {
		t.Errorf("se esperaba UN solo saludo, hay %d:\n%q", n, cuerpoEnviado)
	}
	if strings.Contains(cuerpoEnviado, "¡Hola, Leandro!") {
		t.Errorf("sobrevivió el saludo del modelo:\n%q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, "Soy *Ubi*") || !strings.Contains(cuerpoEnviado, "¿Qué color necesitas?") {
		t.Errorf("el mensaje único perdió la presentación o la pregunta:\n%q", cuerpoEnviado)
	}
}
