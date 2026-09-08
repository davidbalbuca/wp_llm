package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El interceptor del cambio de color: la respuesta a un botón es un conjunto cerrado y la
// resuelve código. "Sí" registra con el color alterno EN ESE TURNO; "no" deja el pedido en
// gestión manual; cualquier otra cosa va al modelo.

func agentConCambioPendiente(store conversation.Store, from string) *Agent {
	store.SetPendingColorSwap(from, conversation.PendingColorSwap{
		ColorOriginal: "NARANJA", ColorAlterno: "AZUL", Cantidad: 2,
	})
	// El PendingWait del pedido original, como lo deja registrarPedido antes de la oferta.
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 3, Cantidad: 2, IDTipoPago: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "NARANJA",
	})
	return agentDePrueba(nil, store)
}

// "Sí" intenta registrar el pedido con el color alterno en ese mismo turno (el backend de
// prueba está muerto, así que el registro falla; lo que se mide es QUE SE INTENTA en código
// y que el pendiente se consume).
func TestCambioColorAceptar(t *testing.T) {
	const from = "593999000060"
	store := conversation.NewMemStore()
	ag := agentConCambioPendiente(store, from)

	reply, manejado := ag.ResponderCambioColor(from, "Sí, azul")
	if !manejado {
		t.Fatal("\"Sí, azul\" no se resolvió en código: dependería de que el modelo llame la tool")
	}
	if _, sigue := store.GetPendingColorSwap(from); sigue {
		t.Error("la oferta sigue pendiente tras aceptar: se le volvería a ofrecer")
	}
	if reply == "" {
		t.Error("el cliente no recibió el desenlace del registro")
	}
}

// También las formas naturales de decir que sí.
func TestCambioColorAceptarConSiNatural(t *testing.T) {
	const from = "593999000061"
	store := conversation.NewMemStore()
	ag := agentConCambioPendiente(store, from)

	if _, manejado := ag.ResponderCambioColor(from, "dale"); !manejado {
		t.Error("\"dale\" con una oferta pendiente debía aceptar el cambio")
	}
}

// "No, gracias" deja el pedido original en gestión manual y se despide mencionando el color
// que el cliente quería.
func TestCambioColorRechazar(t *testing.T) {
	const from = "593999000062"
	store := conversation.NewMemStore()
	ag := agentConCambioPendiente(store, from)

	reply, manejado := ag.ResponderCambioColor(from, "No, gracias")
	if !manejado {
		t.Fatal("\"No, gracias\" no se resolvió en código")
	}
	if _, sigue := store.GetPendingColorSwap(from); sigue {
		t.Error("la oferta sigue pendiente tras rechazar")
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la espera del pedido original sigue viva: el bot seguiría buscando un conductor que no existe")
	}
	if !strings.Contains(strings.ToLower(reply), "naranja") {
		t.Errorf("la despedida no menciona el color que el cliente quería: %q", reply)
	}
}

// Preguntas y matices van al modelo: ahí hay conversación, no un botón.
func TestCambioColorDejaPasarLoQueNoEsOpcion(t *testing.T) {
	const from = "593999000063"
	store := conversation.NewMemStore()
	ag := agentConCambioPendiente(store, from)

	for _, texto := range []string{"¿y cuánto cuesta el azul?", "mejor mándame 3", "hola", ""} {
		if _, manejado := ag.ResponderCambioColor(from, texto); manejado {
			t.Errorf("%q se resolvió en código; debía ir al modelo", texto)
		}
	}
}

// Sin oferta pendiente el interceptor no actúa: un "sí" cualquiera no puede registrar nada.
func TestCambioColorNoActuaSinOferta(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	if _, manejado := ag.ResponderCambioColor("593999000064", "sí"); manejado {
		t.Error("se resolvió un cambio de color que nunca se ofreció")
	}
}

// El botón del sí nunca excede los ~20 caracteres de WhatsApp, ni con colores largos.
func TestBotonSiAlternoCorto(t *testing.T) {
	for _, color := range []string{"AZUL", "AMARILLO", "UN COLOR LARGUISIMO INVENTADO"} {
		if b := botonSiAlterno(color); len(b) > 20 {
			t.Errorf("botón de %q mide %d chars (max 20): %q", color, len(b), b)
		}
	}
}
