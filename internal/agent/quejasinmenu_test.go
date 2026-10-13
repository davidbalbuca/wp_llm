package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// Edison (12/10): "Nunka llego" con su pedido recién cancelado. El modelo derivó al equipo (ticket
// #119) y le habló de su pedido; el candado del fantasma lo tomó como un pedido sin registrar y le
// mandó el menú de colores, como si quisiera pedir otra vez.
func TestTrasDerivarUnaQuejaNoSeLeMandaElMenuDeColores(t *testing.T) {
	const from = "593986243375"
	const dicho = "Lamento mucho la demora, Edison 🙏 Tu pedido está confirmado en nuestro registro y ya avisé al equipo para que te contacte."
	if !afirmaPedidoConfirmado(dicho) {
		t.Fatal("precondición: el texto tiene que parecer un pedido confirmado para el candado")
	}
	store := conversation.NewMemStore()
	fake := &modeloQueLlamaYAfirma{texto: dicho}
	ag := agentIncidente(fake, store)
	var menus []string
	ag.enviarMenu = func(_, cuerpo string, opciones []string) error {
		menus = append(menus, cuerpo)
		return nil
	}

	res, err := ag.HandleMessage(context.Background(), from, "Nunka llego")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if res.MenuEnviado || len(menus) != 0 {
		t.Errorf("tras derivar la queja se le mandó un menú: %v", menus)
	}
	// Lo que reciba lo deciden los demás candados (el pedido ya no existe); lo que importa es que
	// reciba una respuesta y no un formulario de pedido nuevo.
	if strings.TrimSpace(res.Texto) == "" || strings.Contains(strings.ToLower(res.Texto), "color es tu cilindro") {
		t.Errorf("respuesta vacía o de pedido nuevo: %q", res.Texto)
	}
}
