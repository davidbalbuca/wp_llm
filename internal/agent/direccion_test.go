package agent

import (
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Lo que se prueba aquí es que NUNCA se dé por confirmada una dirección que el cliente no
// confirmó. Un falso positivo manda el gas a otra casa.

func TestSoloElBotonExactoConfirmaLaDireccion(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	store.SetPedidoEsperandoDireccion("593999", "BLANCO", 2)

	// Estas NO pueden contar como confirmación: llevan un "sí" pero dicen otra cosa.
	for _, texto := range []string{
		"sí pero a la casa de mi mamá",
		"si, pero en la oficina",
		"sí a otra dirección",
		"cuál dirección?",
		"la de siempre no, la nueva",
		"ok pero cambio de lugar",
	} {
		if _, manejado := a.ConfirmarDireccion("593999", texto); manejado {
			t.Errorf("%q NO podía darse por confirmada: se habría entregado en la dirección vieja", texto)
		}
	}
}

func TestElBotonDeOtraDireccionBorraLaUbicacionVieja(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	store.SetLocation("593999", -2.9, -79.0)
	store.SetDireccionTexto("593999", "Mariscal Sucre 1203")
	store.SetPedidoEsperandoDireccion("593999", "BLANCO", 1)

	reply, manejado := a.ConfirmarDireccion("593999", BotonOtraDireccion)
	if !manejado {
		t.Fatal("el botón de otra dirección debía resolverse en código")
	}
	if reply == "" {
		t.Fatal("hay que pedirle la ubicación nueva")
	}
	// Si la vieja no se borra, el siguiente intento se la vuelve a proponer.
	if _, hay := store.GetLocation("593999"); hay {
		t.Error("la ubicación vieja tenía que borrarse al pedir otra dirección")
	}
	if _, _, sigue := store.GetPedidoEsperandoDireccion("593999"); sigue {
		t.Error("el pedido no podía quedar en pausa después de responder")
	}
}

func TestSinPausaNoSeMeteEnLaConversacion(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	// Nadie preguntó nada: un "Sí, la misma" suelto no debe disparar ningún pedido.
	if _, manejado := a.ConfirmarDireccion("593999", BotonMismaDireccion); manejado {
		t.Fatal("sin un pedido en pausa no hay nada que confirmar")
	}
}

func TestUbicacionRecienCompartidaNoSeConfirma(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	store.SetLocation("593999", -2.9, -79.0)
	if !a.ubicacionEsDeAhora("593999") {
		t.Fatal("una ubicación recién compartida es de esta conversación: no hay que preguntar nada")
	}
}

func TestUbicacionViejaSiSeConfirma(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	store.SetLocation("593999", -2.9, -79.0)
	// El caso real: pidió en la mañana y vuelve en la tarde desde otro lado. El helper vive
	// solo en el store en memoria, no en la interfaz: es de pruebas y no tiene por qué estar
	// disponible en producción.
	envejecer, ok := store.(interface {
		ForzarFechaUbicacion(string, time.Time)
	})
	if !ok {
		t.Fatal("el store en memoria debería permitir envejecer la ubicación en pruebas")
	}
	envejecer.ForzarFechaUbicacion("593999", time.Now().Add(-6*time.Hour))
	if a.ubicacionEsDeAhora("593999") {
		t.Fatal("una ubicación de hace 6 h NO es de esta conversación: hay que confirmarla")
	}
}
