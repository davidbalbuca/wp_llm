package agent

import (
	"testing"

	"wp-llm-gas/internal/conversation"
)

// Raúl (12/10): el operador le creó el #440 desde el chat. Después, en cada mensaje ("Es una casa
// de 3 pisos", "Calle del Sauce"...) el modelo volvía a llamar registrar_pedido con 2 amarillos;
// no se creaba otro pedido, pero la ficha quedaba llena y el panel mostraba "Registrando pedido"
// en un chat con el pedido ya entregado.
func TestConPedidoActivoLaLlamadaRepetidaNoDejaFichaLlena(t *testing.T) {
	const from = "593979026607"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 440)
	ag := agentDePrueba(nil, store)

	res := ag.runTool(&turno{}, from, "registrar_pedido", map[string]any{"color": "AMARILLO", "cantidad": 2})
	if res == "" {
		t.Fatal("la herramienta no respondió")
	}
	if p, hay := store.GetPedidoEnCurso(from); hay && !p.Vacio() {
		t.Errorf("quedó una ficha a medio armar con el pedido #440 ya hecho: %+v", p)
	}
	if id, _ := store.GetActivePedido(from); id != 440 {
		t.Errorf("se tocó el pedido activo: %d", id)
	}
}
