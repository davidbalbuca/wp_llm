package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// Los seis mensajes REALES de produccion (message_log del bot, 23/10 al 06/11). Si el candado no
// los agarra, no sirve: son exactamente lo que el cliente leyo.
func TestAgarraLasNegativasRealesDeProduccion(t *testing.T) {
	reales := []string{
		"📋 ¡Hola, Tom! 👋 No manejamos ninguna app, aquí mismo por WhatsApp te tomo tu pedido de gas directico 😊",
		"📋 No tenemos una aplicación ni usuario/contraseña 😊 Todo lo hacemos aquí mismo por WhatsApp, sin registros ni claves.",
		"📋 Por ahora no manejamos una aplicación para descargar: todo nuestro servicio es aquí mismo por WhatsApp, bien fácil y rápido 😊",
		"📋 Mira, en realidad no tenemos una app como tal: todo el pedido lo hacemos aquí mismo por WhatsApp, directito conmigo 😊",
		"📋 Por ahora no manejamos una app para descargar, todo lo hacemos directo por aquí, por WhatsApp 😊",
		"📋 ¡Hola, María! 😊 Por ahora no contamos con una app, pero tranquila: todo tu pedido lo gestionamos aquí mismito por WhatsApp.",
	}
	for _, m := range reales {
		if !niegaLaApp(m) {
			t.Errorf("no detecto la negativa real: %q", m)
		}
	}
}

// La otra cara, y la que de verdad importa: lo que NO debe tocar. Un candado que se pasa de listo
// rompe la conversacion normal y acaba apagado.
func TestNoSeMeteEnFrasesQueNoNieganLaApp(t *testing.T) {
	inocentes := []string{
		// Hablan de que el CLIENTE no la tiene, no de que no exista.
		"Si no tienes la app instalada, te dejo el enlace 👉 https://ubi.ec/app.html",
		"No te preocupes si no tienes la app, aquí también te tomo el pedido",
		// "no hay" sobre otra cosa, con la palabra app cerca.
		"No hay problema con la app, solo cierra y vuelve a abrirla",
		"No hay stock de blanco ahora; en la app ves los colores disponibles",
		// El bot promocionandola (lo correcto).
		"📲 Mientras llega, baja nuestra app y sigue tu pedido en vivo 👉 https://ubi.ec/app.html",
		"Desde la app pides en dos toques y ves al repartidor en el mapa",
		// Negativas de OTRAS cosas: no deben disparar este candado.
		"No tenemos cobertura en esa zona todavía",
		"No manejamos pago con tarjeta por ahora",
		"No tenemos cilindros azules en este momento",
	}
	for _, m := range inocentes {
		if niegaLaApp(m) {
			t.Errorf("falso positivo: %q", m)
		}
	}
}

// El reemplazo conserva lo que el modelo escribio alrededor y antepone la verdad con el enlace.
func TestElReemplazoDiceLaVerdadYConservaElResto(t *testing.T) {
	a := &Agent{}
	original := "No manejamos ninguna app. ¿De qué color es tu cilindro?"
	got := a.revisarNegativaDeApp("593999", original)

	if !strings.Contains(got, LinkApp) {
		t.Errorf("la respuesta no lleva el enlace de descarga: %q", got)
	}
	if niegaLaApp(got) {
		t.Errorf("la negativa sobrevivio al reemplazo: %q", got)
	}
	if !strings.Contains(got, "¿De qué color es tu cilindro?") {
		t.Errorf("se perdio la pregunta que si estaba bien: %q", got)
	}
}

// Si el mensaje era SOLO la negativa, queda la verdad sola (sin un salto de linea huerfano).
func TestNegativaSolaQuedaSoloLaVerdad(t *testing.T) {
	a := &Agent{}
	got := a.revisarNegativaDeApp("593999", "No tenemos una aplicación.")
	if got != MensajeLaAppSiExiste() {
		t.Errorf("esperaba solo el mensaje de la app, got %q", got)
	}
}

// Un texto sano pasa intacto: el candado no debe reescribir lo que no toca.
func TestTextoSanoPasaIntacto(t *testing.T) {
	a := &Agent{}
	sano := "¿De qué color es tu cilindro? 👇"
	if got := a.revisarNegativaDeApp("593999", sano); got != sano {
		t.Errorf("modifico un texto sano: %q", got)
	}
}

// EL CASO REAL, y la razón de que el candado vaya también en mandarMenu: los cuatro mensajes del
// 06/11 llegaron al cliente como CUERPO del menú de colores, no como texto de la respuesta.
// Cuando el turno acaba en menú el texto NO se envía, así que un candado enganchado solo en la
// cadena de `reply` habría pasado los tests y en producción no habría agarrado ni uno.
func TestIncidente_LaNegativaDeAppSeTapaAunqueElTurnoAcabeEnMenu(t *testing.T) {
	const from = "593999557515"
	store := conversation.NewMemStore()

	var cuerpoEnviado string
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.enviarMenu = func(_, cuerpo string, _ []string) error {
		cuerpoEnviado = cuerpo
		return nil
	}

	// Textual de produccion (cliente ...7515, 06/11 12:31).
	cuerpo := "Por ahora no manejamos una aplicación para descargar: todo nuestro servicio es " +
		"aquí mismo por WhatsApp 😊 ¿De qué color es tu cilindro?"
	if err := ag.mandarMenu(from, cuerpo, []string{"BLANCO", "AMARILLO"}); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	if niegaLaApp(cuerpoEnviado) {
		t.Fatalf("la negativa llegó al cliente en el cuerpo del menú: %q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, LinkApp) {
		t.Errorf("el menú salió sin el enlace de descarga: %q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, "¿De qué color es tu cilindro?") {
		t.Errorf("se perdió la pregunta del color, que estaba bien: %q", cuerpoEnviado)
	}
}

// La redaccion que pidio David el 06/11, fijada aqui para que no se deshaga sin darse cuenta:
// "dos PASOS" (no toques), nada de registro/usuario/contrasena, y corto.
func TestElMensajeDeLaAppDiceLoEsencialYNadaMas(t *testing.T) {
	m := MensajeLaAppSiExiste()

	if !strings.Contains(m, "dos pasos") {
		t.Errorf("David pidio 'dos pasos': %q", m)
	}
	if strings.Contains(strings.ToLower(m), "toque") {
		t.Errorf("'toques' se cambio por 'pasos' a peticion de David: %q", m)
	}
	for _, prohibido := range []string{"registr", "usuario", "contrasena", "contraseña", "clave"} {
		if strings.Contains(strings.ToLower(m), prohibido) {
			t.Errorf("no se habla de %q: alarga el mensaje y no hace falta (%q)", prohibido, m)
		}
	}
	if !strings.Contains(m, "más cercano") {
		t.Errorf("falta lo esencial -que le busca al repartidor mas cercano-: %q", m)
	}
	if !strings.Contains(m, LinkApp) {
		t.Errorf("falta el enlace: %q", m)
	}
	// Una sola linea, como las demas invitaciones del bot.
	if n := strings.Count(strings.TrimSpace(m), "\n"); n != 0 {
		t.Errorf("deberia ser una sola linea, tiene %d saltos: %q", n+1, m)
	}
}
