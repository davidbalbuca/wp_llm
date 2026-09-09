package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El destino que se le muestra al cliente es el NOMBRE que él le puso ("Casa"), no la calle:
// es lo que reconoce de un vistazo.
func TestDestinoPrefiereElNombreQuePusoElCliente(t *testing.T) {
	last := conversation.LastOrder{Alias: "Casa", Direccion: "Av. Solano 123"}
	if got := last.Destino(); got != "Casa" {
		t.Errorf("con nombre puesto por el cliente se muestra ese nombre, no la calle: %q", got)
	}
}

// Sin nombre, la calle sirve: "a Av. Solano 123" es mejor que no decir nada.
func TestDestinoCaeALaCalleSiNoHayNombre(t *testing.T) {
	last := conversation.LastOrder{Direccion: "Av. Solano 123"}
	if got := last.Destino(); got != "Av. Solano 123" {
		t.Errorf("sin nombre se muestra la calle: %q", got)
	}
}

// REGLA DURA: sin ubicación guardada NO se inventa un destino. Los pedidos anteriores a esta
// feature no la tienen, y decirle "te lo envío a X" sin saberlo sería peor que preguntar.
func TestDestinoVacioCuandoNoSeSabeDondeSeEntrego(t *testing.T) {
	if got := (conversation.LastOrder{}).Destino(); got != "" {
		t.Errorf("sin datos de entrega el destino debe ser vacío, no inventado: %q", got)
	}
}

// El texto del pedido nombra cantidad, color y destino.
func TestDescribirPedidoNombraCantidadColorYDestino(t *testing.T) {
	txt := describirPedido(conversation.LastOrder{Cantidad: 2, Color: "BLANCO", Alias: "Casa"})
	for _, esperado := range []string{"2", "BLANCO", "Casa"} {
		if !strings.Contains(txt, esperado) {
			t.Errorf("falta %q en el texto del pedido: %q", esperado, txt)
		}
	}
}

// NO-REGRESIÓN: un pedido viejo (sin ubicación) se describe sin arrastrar un " a " colgando.
func TestDescribirPedidoViejoNoMencionaDestino(t *testing.T) {
	if txt := describirPedido(conversation.LastOrder{Cantidad: 1, Color: "BLANCO"}); txt != "1 BLANCO" {
		t.Errorf("un pedido sin ubicación se describe solo con cantidad y color: %q", txt)
	}
}
