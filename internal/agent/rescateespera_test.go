package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// Jessi (12/10): el modelo dijo "dame un segundito que registro tu pedido" sin registrarlo; el
// rescate lo registró, no había repartidor cerca y le preguntó "sí/no" para esperar, cuando la
// búsqueda del backend YA estaba corriendo. Contestó "Si" y el bot no tenía nada que decidir.
func TestRescateConBusquedaNoPreguntaSiEspera(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg.UsarBusquedaBackend = true
	msg := ag.mensajeRescateEnEspera([]conversation.ItemPedido{{Color: "AMARILLO", Cantidad: 1}})
	bajo := strings.ToLower(msg)
	for _, prohibido := range []string{"¿deseas", "\"sí\"", "\"no\""} {
		if strings.Contains(bajo, prohibido) {
			t.Errorf("con la búsqueda corriendo se le preguntó si espera (%q): %q", prohibido, msg)
		}
	}
	if !strings.Contains(bajo, "amarillo") || !strings.Contains(bajo, "buscando") {
		t.Errorf("no le confirma su pedido ni que estamos buscando: %q", msg)
	}
}

// Sin la búsqueda del backend la espera la decide el cliente, como siempre.
func TestRescateSinBusquedaOfreceEsperar(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg.UsarBusquedaBackend = false
	msg := ag.mensajeRescateEnEspera([]conversation.ItemPedido{{Color: "AMARILLO", Cantidad: 1}})
	if !strings.Contains(msg, "¿Deseas") {
		t.Errorf("sin búsqueda del backend no se le ofreció esperar: %q", msg)
	}
}
