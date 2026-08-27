package agent

import "testing"

// Lo que se prueba aquí es el clasificador: qué se considera un sí, qué un no, y sobre todo
// qué NO se considera ninguno de los dos. Un falso positivo registra un pedido que el cliente
// no pidió; un falso negativo lo devuelve al modelo, que es exactamente lo que falló tres
// veces en producción el 27/08.

func TestNormalizarRespuestaLimpiaTildesYSignos(t *testing.T) {
	casos := map[string]string{
		"¡Sí, dale!":  "si dale",
		"  SI  ":      "si",
		"Sí.":         "si",
		"¿si?":        "si",
		"No, gracias": "no gracias",
		"OK":          "ok",
		"Ya   no":     "ya no",
	}
	for entrada, esperado := range casos {
		if got := normalizarRespuesta(entrada); got != esperado {
			t.Errorf("normalizarRespuesta(%q) = %q, esperaba %q", entrada, got, esperado)
		}
	}
}

func TestRespuestasQueConfirman(t *testing.T) {
	// Estas son las que llegan de verdad por WhatsApp cuando el mensaje dice: Responde "Si".
	for _, texto := range []string{"Si", "sí", "SI", "Sí!", "si por favor", "Dale", "ok", "Listo", "confirmo", "👍"} {
		if !respuestasAfirmativas[normalizarRespuesta(texto)] {
			t.Errorf("%q debería contar como confirmación y no cuenta", texto)
		}
	}
}

func TestRespuestasQueCancelan(t *testing.T) {
	for _, texto := range []string{"No", "no gracias", "Ya no", "mejor no", "cancelar", "No quiero"} {
		if !respuestasNegativas[normalizarRespuesta(texto)] {
			t.Errorf("%q debería contar como negativa y no cuenta", texto)
		}
	}
}

func TestLosBotonesSiempreSeReconocen(t *testing.T) {
	// El aviso de una entrega agendada se manda con botones. Si el titulo del boton dejara de
	// caer en el clasificador, el cliente tocaria "Si, enviar" y no pasaria nada: exactamente el
	// bug que se esta arreglando. Por eso se registran solos en init(), y esto lo vigila.
	if !respuestasAfirmativas[normalizarRespuesta(BotonConfirmarEntrega)] {
		t.Fatalf("el botón %q no cuenta como confirmación", BotonConfirmarEntrega)
	}
	if !respuestasNegativas[normalizarRespuesta(BotonCancelarEntrega)] {
		t.Fatalf("el botón %q no cuenta como negativa", BotonCancelarEntrega)
	}
	// WhatsApp corta los títulos de botón a 20 caracteres: si se pasa, el texto que vuelve no
	// es el que se registró y deja de reconocerse.
	for _, b := range []string{BotonConfirmarEntrega, BotonCancelarEntrega} {
		if len([]rune(b)) > 20 {
			t.Errorf("el botón %q pasa de 20 caracteres y WhatsApp lo va a cortar", b)
		}
	}
}

func TestLoAmbiguoNoSeDecideEnCodigo(t *testing.T) {
	// Lo importante del diseño: si el cliente no dio un sí o un no limpio, NO se registra ni se
	// cancela nada por nuestra cuenta. Eso sí es conversación y va al modelo.
	ambiguos := []string{
		"si pero mañana",
		"si puedo cambiar el color?",
		"no sé, cuánto cuesta?",
		"no, mejor a las 3",
		"si me lo puedes traer a otra dirección",
		"cuanto cuesta",
		"ya llegó?",
	}
	for _, texto := range ambiguos {
		n := normalizarRespuesta(texto)
		if respuestasAfirmativas[n] || respuestasNegativas[n] {
			t.Errorf("%q NO debía resolverse en código (quedó como %q)", texto, n)
		}
	}
}
