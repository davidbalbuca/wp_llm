package agent

import (
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO JOHN (06/10, #370): pidió 2 blancos, quedó en espera y el sistema se lo asignó. La ficha del
// pedido a medio armar quedó viva y a cada respuesta se le pegaba "¿Seguimos con tu pedido?",
// aunque su pedido ya iba en camino.
func TestConPedidoEnCaminoNoSeLePreguntaSiSeguimos(t *testing.T) {
	const from = "593962968093"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})
	store.SetActivePedido(from, 370)
	ag := agentIncidente(nil, store)

	dijo := "¡Perfecto, John! 😊 Tu pedido sigue en camino con Juan, tranquilo. En cuanto llegue te aviso por aquí mismo 🚚🙏"
	if ag.turnoQuedaColgado(&turno{}, from, dijo, dijo) {
		t.Error("tiene un pedido en camino: no está a medio pedir, no hay que preguntarle si seguimos")
	}
}
