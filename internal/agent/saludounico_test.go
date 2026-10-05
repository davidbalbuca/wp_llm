// UN SOLO SALUDO POR CONVERSACIÓN.
//
// INCIDENTE 28/09, señalado por el dueño con el caso 593999376151, y presente en 70 clientes del
// histórico: el código se presenta y el modelo saluda otra vez a los pocos segundos.
//
//	07:49:48  system   "¡Hola, Chri! 👋 ¿Se te acabó el gas? 😱 ¡Tranqui, *UbiGas* está aquísito no más! 🔥  Te conecto con el repartidor…"
//	07:49:56  model    "¡Hola, Chri! 👋 Con gusto te ayudo con tu pedido de gas 😊…"
//
// Las frases de prueba son las REALES: salen de medir las 93 aperturas que el modelo escribió justo
// después del saludo del código.
package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// El caso exacto del dueño: el segundo saludo no sale, y lo que el modelo aportaba sí.
func TestIncidenteChri_NoSaleElSegundoSaludo(t *testing.T) {
	const from = "593999376151"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	// El saludo del código, tal cual lo manda avisarCliente (AppendModel).
	store.AppendModel(from, textoBienvenida("Chri"))

	// Y la respuesta REAL del modelo de aquel turno.
	reply := "¡Hola, Chri! 👋 Con gusto te ayudo con tu pedido de gas 😊\n\n" +
		"Tenemos cilindros de 15kg en estos colores: Blanco, Amarillo, Naranja y Azul."
	salida := ag.revisarSaludoDuplicado(from, reply)

	if strings.Contains(salida, "Hola, Chri") {
		t.Errorf("el segundo saludo salió igual: %q", salida)
	}
	// Lo que SÍ aportaba se conserva: el candado quita el saludo, no el contenido.
	if !strings.Contains(salida, "Blanco") {
		t.Errorf("se perdió el contenido útil del mensaje: %q", salida)
	}
	if !strings.HasPrefix(salida, "C") && !strings.HasPrefix(salida, "T") {
		t.Errorf("el texto recortado no empieza bien (mayúscula inicial): %q", salida)
	}
}

// Las 93 aperturas medidas en producción, por familia.
func TestQuitarSaludoConLasAperturasRealesDelModelo(t *testing.T) {
	casos := []struct {
		in    string
		queda string // fragmento que TIENE que sobrevivir
	}{
		{"¡Hola, Chri! 👋 Con gusto te ayudo con tu pedido de gas 😊 Dime el color.", "Dime el color"},
		{"¡Hola! 👋 Qué bueno que quieras pedir tu gas. ¿De qué color?", "¿De qué color?"},
		{"📋 ¡Buenos días, Geovanny! 👋 Qué gusto saludarte. El precio es $3.25", "El precio es $3.25"},
		{"¡Buenas noches, Doris! 👋 Claro, aquí estoy. ¿Cuántos cilindros?", "¿Cuántos cilindros?"},
		{"¡Buenas tardes, Carlos! 👋 ¿En qué puedo ayudarte?", "¿En qué puedo ayudarte?"},
		{"Hola Sergy 👋\n\nClaro que sí, pero aquí atendemos solo gas.", "aquí atendemos solo gas"},
		{"¡Hola, Josué! 👋 ¿Qué necesitas hoy?", "¿Qué necesitas hoy?"},
		{"¡Qué tal, Eliza! 👋 El cilindro cuesta $3.25", "El cilindro cuesta $3.25"},
	}
	for _, c := range casos {
		out := quitarSaludoDuplicado(c.in)
		if !strings.Contains(out, c.queda) {
			t.Errorf("se perdió contenido:\n  in:  %q\n  out: %q\n  faltaba: %q", c.in, out, c.queda)
		}
		bajo := strings.ToLower(out)
		for _, resto := range []string{"hola", "buenos días", "buenas tardes", "buenas noches"} {
			if strings.HasPrefix(bajo, resto) {
				t.Errorf("sigue empezando con un saludo (%q): %q", resto, out)
			}
		}
	}
}

// CASO 04/10 (593984***145): el nombre de perfil era "J.L🪽". El patrón genérico solo acepta letras
// en el nombre, cortaba en el punto y el cliente recibió "L🪽! 👋 No hay problema..." pegado a la
// bienvenida. Con el nombre que el código ya conoce, el saludo se quita entero, tenga lo que tenga.
func TestQuitarSaludoConNombresQueNoSonSoloLetras(t *testing.T) {
	casos := []struct{ nombre, in, queda string }{
		{"J.L🪽", "¡Buenos días, J.L🪽! 👋 No hay problema, hoy sí te ayudamos con gusto 😊",
			"No hay problema"},
		{"Mari_88", "¡Hola, Mari_88! 👋 ¿Qué color necesitas?", "¿Qué color necesitas?"},
		{"Dra. Fanny", "¡Buenas tardes, Dra. Fanny! 👋 Te ayudo con tu pedido.", "Te ayudo con tu pedido."},
	}
	for _, c := range casos {
		out := quitarSaludoDuplicado(c.in, primerNombre(c.nombre), c.nombre)
		if !strings.HasPrefix(out, c.queda) {
			t.Errorf("con el nombre %q quedó un jirón del saludo:\n  in:  %q\n  out: %q", c.nombre, c.in, out)
		}
	}
}

// Y si el modelo usa OTRO nombre (o no hay nombre), sigue funcionando el patrón genérico.
func TestConOtroNombreSigueElPatronGenerico(t *testing.T) {
	out := quitarSaludoDuplicado("¡Hola, Chri! 👋 Dime el color.", "Christian")
	if out != "Dime el color." {
		t.Errorf("con un nombre distinto al conocido no se quitó el saludo: %q", out)
	}
}

// LO QUE NO SE TOCA: 22 de las 93 aperturas iban DIRECTO al grano, sin saludar. Ese es el
// comportamiento correcto y el candado no puede alterarlo.
func TestSinSaludoElTextoQuedaIntacto(t *testing.T) {
	for _, in := range []string{
		"📋 ¿Qué color/marca de cilindro de 15kg necesitas?",
		"El cilindro de GAS 15KG cuesta $3.25 💰",
		"Compárteme tu ubicación por WhatsApp 📎",
		"¿Cuántos cilindros necesitas?",
	} {
		if out := quitarSaludoDuplicado(in); out != in {
			t.Errorf("se alteró un texto que no saludaba:\n  in:  %q\n  out: %q", in, out)
		}
	}
}

// Si el modelo SOLO saluda y no aporta nada, su turno se omite: el saludo del código ya salió.
func TestSiElModeloSoloSaludaSeOmiteSuTurno(t *testing.T) {
	const from = "593999376152"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	store.AppendModel(from, textoBienvenida("Ana"))

	if out := ag.revisarSaludoDuplicado(from, "¡Hola, Ana! 👋"); out != "" {
		t.Errorf("un turno que solo repite el saludo debe omitirse, salió %q", out)
	}
}

// A MITAD DE CONVERSACIÓN EL MODELO SALUDA NORMAL. behavior.md:103 nació de un incidente ("un menú
// sin saludo se siente como hablarle a una máquina") y sigue vigente cuando el código NO se presentó.
func TestAMitadDeConversacionElSaludoSeRespeta(t *testing.T) {
	const from = "593999376153"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	// Historial SIN la presentación: el cliente lleva rato hablando.
	store.AppendUser(from, "hola")
	store.AppendModel(from, "¿De qué color lo necesitas?")

	reply := "¡Hola, David! 👋 ¿Deseas lo mismo de la última vez (2 GAS 15KG BLANCO)?"
	if out := ag.revisarSaludoDuplicado(from, reply); out != reply {
		t.Errorf("se quitó un saludo legítimo a mitad de conversación:\n  %q", out)
	}
}

// Y si la presentación fue hace rato (no el último turno), el modelo puede saludar otra vez.
func TestUnSaludoViejoNoSilenciaAlModelo(t *testing.T) {
	const from = "593999376154"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	store.AppendModel(from, textoBienvenida("Luis"))
	store.AppendUser(from, "blanco")
	store.AppendModel(from, "¿Cuántos?")
	store.AppendUser(from, "2")

	reply := "¡Hola, Luis! 👋 Confirmo 2 blancos."
	if out := ag.revisarSaludoDuplicado(from, reply); out != reply {
		t.Errorf("la presentación era vieja; el saludo debía respetarse:\n  %q", out)
	}
}

// LA FRANJA DEL DÍA la dice el código, para que el modelo no tenga nada que corregir. A las 20:00
// el saludo era "¡Hola, Doris!" y el modelo respondía "¡Buenas noches, Doris!".
func TestElSaludoDelCodigoLlevaLaFranjaDelDia(t *testing.T) {
	casos := []struct {
		hora   int
		quiero string
	}{
		{8, "Buenos días"}, {11, "Buenos días"},
		{12, "Buenas tardes"}, {15, "Buenas tardes"}, {18, "Buenas tardes"},
		{20, "Buenas noches"}, {23, "Buenas noches"}, {2, "Buenos días"},
	}
	for _, c := range casos {
		ahora := time.Date(2026, 9, 28, c.hora, 0, 0, 0, time.Local)
		texto := textoBienvenidaA("Doris", ahora)
		if !strings.Contains(texto, c.quiero) {
			t.Errorf("a las %02d:00 el saludo debía decir %q: %q", c.hora, c.quiero, texto[:40])
		}
		if !strings.Contains(texto, "Doris") {
			t.Errorf("a las %02d:00 se perdió el nombre: %q", c.hora, texto[:40])
		}
		// Y sigue llevando la presentación, que es su razón de ser (pedido de David).
		if !strings.Contains(texto, marcaDePresentacion) {
			t.Errorf("el saludo perdió la presentación: %q", texto)
		}
	}
}
