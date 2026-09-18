package agent

import (
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// EL CANDADO DE COBERTURA TENÍA UNA SOLA CARA.
//
// El de arriba (cobertura.go) vigila la NEGATIVA: que el modelo no rechace a un cliente por el
// nombre de su barrio. Se escribió con esta justificación, que resultó equivocada:
//
//	"Solo se vigila la NEGATIVA, no el 'sí': afirmar cobertura de más no pierde al cliente y el
//	 pedido igual se valida por coordenadas antes de registrarse."
//
// El 15/09 QA probó la otra cara. Preguntando SOLO POR NOMBRE, sin enviar ubicación, el bot
// confirmó cobertura en cuatro cantones donde no se opera:
//
//	CLIENTE  ¿Llegan a Paute?
//	BOT      Sí, atendemos en Azuay 👍 Paute está cubierto. ¿Cuál color prefieres?
//
// Es verdad que la geocerca nunca despacha un conductor fuera de zona. Pero el cliente recibe
// un SÍ, elige color, da su cédula y su nombre, comparte la ubicación — y recién ahí se le
// rechaza. Un no honesto al principio cuesta menos que un sí que se cae al final.

// afirmarCoberturaSinVerificar arma el estado del caso real: cliente que solo preguntó por un
// nombre, sin ubicación compartida ni verificación de geocerca.
func clienteQueSoloPregunto(t *testing.T) (*Agent, string) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()
	return ag, "593999400001"
}

func TestNoSeAfirmaCoberturaPorElNombreDeUnLugar(t *testing.T) {
	ag, from := clienteQueSoloPregunto(t)

	// Las redacciones reales del 15/09 y las variantes que produce el modelo.
	afirmaciones := []string{
		"Sí, atendemos en Azuay 👍 Paute está cubierto. Ahora bien, ¿cuál color prefieres?",
		"Sí, atendemos en Azuay 👍 Sígsig está cubierto. ¿Quieres hacer un pedido ahora?",
		"¡Claro! Gualaceo está dentro de nuestra cobertura 😊",
		"Sí llegamos a Santa Isabel, con gusto te atendemos.",
		"Perfecto, sí tenemos cobertura en esa zona.",
	}
	for _, reply := range afirmaciones {
		got := ag.revisarCoberturaAfirmada(from, reply)
		if got == reply {
			t.Errorf("el bot afirmó cobertura sin verificar y el texto pasó intacto: %q", reply)
			continue
		}
		// Lo que debe salir en su lugar: pedir la ubicación, que es lo único que decide.
		if !strings.Contains(strings.ToLower(got), "ubicaci") {
			t.Errorf("el reemplazo no pide la ubicación: %q", got)
		}
	}
}

// Lo que NO debe tocar. El candado tiene que ser quirúrgico: si se pasa de celoso, rompe
// conversaciones normales y acabamos quitándolo.
func TestElCandadoDeCoberturaAfirmadaNoSeMeteDondeNoDebe(t *testing.T) {
	ag, from := clienteQueSoloPregunto(t)

	intactos := []string{
		// Pedir la ubicación ya es la respuesta correcta: taparla sería un bucle.
		"¡Claro que sí! 😊 Atendemos en AZUAY (BANOS, BELLAVISTA y más). Para confirmarte si " +
			"llegamos justo a tu dirección, compárteme tu ubicación por WhatsApp 📎.",
		// Un "sí" que no habla de cobertura.
		"Sí, tenemos gas de 15 kg en BLANCO, AMARILLO, NARANJA y AZUL 😊",
		"Sí, puedo agendarte la entrega para mañana a las 10:00.",
		// Confirmar un pedido YA registrado no es afirmar cobertura.
		"¡Listo! Tu pedido va en camino con Nelson 🚚",
		// Preguntar no es afirmar.
		"¿Quieres que revise si llegamos a tu dirección? Compárteme tu ubicación 📎",
	}
	for _, reply := range intactos {
		if got := ag.revisarCoberturaAfirmada(from, reply); got != reply {
			t.Errorf("el candado tocó un texto que no afirmaba cobertura:\n  entrada: %q\n  salida:  %q", reply, got)
		}
	}
}

// EL CANDADO TIENE QUE ESTAR CABLEADO AL TURNO, no solo existir.
//
// Los tests de arriba llaman a revisarCoberturaAfirmada directamente: pasan igual aunque nadie
// lo invoque en HandleMessage. Se comprobó desconectando la llamada en agent.go — seguían en
// verde. Este lee el archivo y exige que la llamada esté puesta, junto a su hermana la negativa.
func TestElCandadoDeCoberturaAfirmadaEstaCableado(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("no se pudo leer agent.go: %v", err)
	}
	texto := string(src)
	if !strings.Contains(texto, "reply = a.revisarCoberturaAfirmada(from, reply)") {
		t.Error("revisarCoberturaAfirmada no se aplica a la respuesta en agent.go: el candado " +
			"existe pero no lo usa nadie, así que el bot puede seguir prometiendo cobertura")
	}
	// Las dos caras van juntas: si alguien quita una, que el test lo diga.
	if !strings.Contains(texto, "reply = a.revisarNegativaDeCobertura(from, reply)") {
		t.Error("revisarNegativaDeCobertura ya no se aplica: se perdió la otra cara del candado")
	}
}

// Si la ubicación YA se verificó por coordenadas y cayó DENTRO, el "sí" es verdad y tiene que
// poder decirse. Sin esta excepción el bot no podría confirmarle la cobertura a un cliente que
// acaba de compartir su ubicación — justo cuando ya la sabe de verdad.
func TestConUbicacionVerificadaElSiPasaIntacto(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()
	const from = "593999400002"

	// Ubicación compartida en esta conversación: la geocerca ya decidió (y la dejó pasar,
	// porque fueraDeCobertura habría cortado el turno antes de llegar al modelo).
	store.SetLocation(from, -2.9, -79.0)

	reply := "¡Sí! Llegamos a tu dirección sin problema 😊 ¿Cuántos cilindros necesitas?"
	if got := ag.revisarCoberturaAfirmada(from, reply); got != reply {
		t.Errorf("con ubicación ya verificada el sí debe pasar intacto:\n  entrada: %q\n  salida:  %q", reply, got)
	}
}
