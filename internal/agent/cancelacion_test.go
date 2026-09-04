package agent

import "testing"

// El backend solo deja cancelar un pedido EN CAMINO. Si el conductor ya lo canceló desde su app
// (pasa seguido), CancelOrder devuelve "el pedido ya fue cancelado" — eso NO es un fallo, el
// cliente ya tiene lo que quería. yaEstabaCancelado los reconoce para no gritar "sigue vivo".
func TestYaEstabaCancelado(t *testing.T) {
	// Mensajes del backend que significan "ya no está vivo" → tratar como éxito.
	noEsFallo := []string{
		"El pedido ya fue cancelado",
		"anthropic HTTP 400: el pedido ya fue cancelado",
		"El pedido fue entregado",
		"El pedido no existe",
		// El propio texto de retorno de cancelarPedido para este caso:
		"El pedido del cliente YA estaba cancelado (lo canceló el conductor).",
	}
	for _, m := range noEsFallo {
		if !yaEstabaCancelado(m) {
			t.Errorf("debía reconocerse como ya-cancelado (no fallo): %q", m)
		}
	}

	// Fallos REALES → sí hay que alertar.
	fallos := []string{
		"connection refused",
		"anthropic HTTP 500: internal error",
		"timeout",
		"No se pudo autenticar al cliente",
	}
	for _, m := range fallos {
		if yaEstabaCancelado(m) {
			t.Errorf("FALSO POSITIVO, esto SÍ es un fallo real: %q", m)
		}
	}
}
