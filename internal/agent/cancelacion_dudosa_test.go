package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// 17/10, Blanca: "Gracias  no deseo" con el pedido #491 en camino. El bot se despidió y el pedido
// siguió. Ahora se le pregunta con el botón de cancelar.
func TestIncidente_NoDeseoConPedidoEnCaminoSePregunta(t *testing.T) {
	const from = "593984861856"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 491)
	ag := agentDePrueba(nil, store)
	var opciones []string
	ag.enviarMenu = func(_, _ string, o []string) error { opciones = o; return nil }

	if !ag.PreguntarSiCancela(from, "Gracias  no deseo") {
		t.Fatal("no se preguntó si cancela")
	}
	if len(opciones) != 2 || opciones[0] != BotonCancelarPedido || opciones[1] != BotonLoEspero {
		t.Errorf("opciones = %v", opciones)
	}
	// No canceló nada: el pedido sigue hasta que el cliente toque el botón.
	if _, sigue := store.GetActivePedido(from); !sigue {
		t.Error("el pedido se dio por cancelado sin que el cliente lo confirmara")
	}
	// El botón "Cancelar pedido" lo resuelve el interceptor que ya existía.
	if normalizarRespuesta(opciones[0]) != normalizarRespuesta(BotonCancelarPedido) {
		t.Error("el botón no es el que reconoce ResponderCancelacion")
	}
	if msg, ok := ag.ResponderLoEspero(from, "Lo espero"); !ok || !strings.Contains(msg, "en camino") {
		t.Errorf("Lo espero: %q %v", msg, ok)
	}
}

func TestNoDeseoSinPedidoEnCaminoVaAlModelo(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.enviarMenu = func(_, _ string, _ []string) error { t.Error("mandó menú sin pedido en camino"); return nil }
	if ag.PreguntarSiCancela("593900000001", "no deseo") {
		t.Error("se hizo cargo sin pedido en camino")
	}
}

func TestQueSuenaACancelarYQueNo(t *testing.T) {
	for _, s := range []string{"Gracias  no deseo", "no deseo, gracias", "ya no", "Ya no lo necesito",
		"mejor no", "no quiero", "ya no me interesa", "Disculpe ya no deseo"} {
		if !dudaDeCancelar(s) {
			t.Errorf("%q debería preguntar si cancela", s)
		}
	}
	// Un "no" suelto contesta otra pregunta; una pregunta no es una orden; y lo demás es charla.
	for _, s := range []string{"no", "No gracias", "gracias", "¿ya no hay?", "no deseo amarillo, deseo azul",
		"no quiero esperar mucho", "no tengo cambio"} {
		if dudaDeCancelar(s) {
			t.Errorf("%q no debería preguntar si cancela", s)
		}
	}
}

// 17/10, Blanca: "Disculpe esta en 3.50" era el PRECIO. El candado lo leyó como las 3:50.
func TestIncidente_UnPrecioNoEsUnaHora(t *testing.T) {
	for _, s := range []string{"Disculpe esta en 3.50", "3.50", "a 3.50 el gas?", "cuesta 3.50",
		"$3.50", "son 7.00 en total", "me cobraron 3.50"} {
		if h, ambigua := horaAmbigua(s); ambigua {
			t.Errorf("%q se tomó por la hora %d; era un precio", s, h)
		}
	}
	// Y una hora con punto sigue siendo una hora si viene como hora.
	for _, s := range []string{"a las 3.50", "para las 4.30"} {
		if _, ambigua := horaAmbigua(s); !ambigua {
			t.Errorf("%q dejó de detectarse como hora ambigua", s)
		}
	}
}
