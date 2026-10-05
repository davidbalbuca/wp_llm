package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// SIN CÉDULA NI AUTORIZACIÓN (decisión del 04/10). El bot ya no pide cédula ni el permiso de
// datos para tomar un pedido: el backend registra al cliente por su teléfono de WhatsApp. El
// consentimiento se conserva apagado (BOT_PEDIR_CONSENTIMIENTO); ver consentimiento_test.go para
// cómo funciona encendido.

// Un cliente nuevo, sin cédula ni nombre escritos, con su nombre de perfil de WhatsApp: el pedido
// NO se detiene pidiendo datos personales, sigue al siguiente paso (el catálogo).
func TestSinCedulaElPedidoNoSeDetienePidiendoDatos(t *testing.T) {
	const from = "593999900051"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetProfile(from, conversation.Profile{PerfilWhatsApp: "Jorge Luna 🪽"})
	store.SetLocation(from, -2.885136, -78.986911)

	tur := &turno{}
	salida := ag.registrarPedido(tur, from, map[string]any{"color": "BLANCO", "cantidad": 1})

	bajo := strings.ToLower(salida)
	if strings.Contains(bajo, "cédula") || strings.Contains(bajo, "cedula") || strings.Contains(bajo, "autoriz") {
		t.Fatalf("se detuvo el pedido para pedir cédula o autorización: %q", salida)
	}
	if tur.ultimoPedido.faltaDato != "" {
		t.Fatalf("quedó marcado un dato faltante (%q) aunque ya no hace falta", tur.ultimoPedido.faltaDato)
	}
}

// Sin nombre en ningún lado (ni escrito ni de WhatsApp) se pide SOLO el nombre, nunca la cédula.
func TestSinNingunNombreSePideSoloElNombre(t *testing.T) {
	const from = "593999900052"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetLocation(from, -2.885136, -78.986911)

	tur := &turno{}
	salida := ag.registrarPedido(tur, from, map[string]any{"color": "BLANCO", "cantidad": 1})

	if !strings.Contains(strings.ToLower(salida), "nombre") {
		t.Fatalf("sin ningún nombre debía pedirse el nombre: %q", salida)
	}
	if strings.Contains(strings.ToLower(salida), "cédula") {
		t.Errorf("se pidió la cédula: %q", salida)
	}
	if tur.ultimoPedido.faltaDato != "nombre" {
		t.Errorf("faltaDato = %q, se esperaba \"nombre\"", tur.ultimoPedido.faltaDato)
	}
}

// Con el consentimiento APAGADO no se intercepta nada, y una negativa grabada cuando estaba
// encendido tampoco bloquea: si no, a quien se negó antes no habría forma de reabrirle el bot.
func TestConsentimientoApagadoNoBloqueaNiIntercepta(t *testing.T) {
	const from = "593999900053"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store) // PedirConsentimiento=false: el valor por defecto
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})
	store.SetConsentimientoPendiente(from)

	if ag.consentimientoNiega(from) {
		t.Error("con el consentimiento apagado, una negativa vieja sigue bloqueando el pedido")
	}
	if _, manejado := ag.ResponderConsentimiento(from, BotonAceptoDatos); manejado {
		t.Error("con el consentimiento apagado, el interceptor tomó el turno")
	}
	const peticion = "Para tu factura, ¿me compartes tu número de cédula?"
	if salida := ag.revisarPeticionDeCedula(&turno{}, from, peticion); salida != peticion {
		t.Errorf("con el consentimiento apagado, el candado cambió el mensaje: %q", salida)
	}
}

// "cancelar" con la búsqueda de repartidor abierta (todavía sin pedido creado) cancela la
// BÚSQUEDA. El 04/10 en el simulador respondía "no tienes ningún pedido en curso" y la búsqueda
// seguía viva, aunque el bot acababa de decirle "si quieres cancelarlo, escríbeme cancelar".
func TestCancelarConLaBusquedaAbiertaCancelaLaBusqueda(t *testing.T) {
	const from = "593999900054"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetPendingWait(from, conversation.PendingWait{
		IDProducto: 1, IDColor: 2, Cantidad: 1, ColorNombre: "AMARILLO", IDBusqueda: 65,
	})

	salida, manejado := ag.ResponderCancelacion(from, "cancelar")

	if !manejado {
		t.Fatal("el 'cancelar' con la búsqueda abierta no se resolvió en código")
	}
	if strings.Contains(strings.ToLower(salida), "no tienes ningún pedido") {
		t.Fatalf("se le dijo que no tenía pedido a quien estaba esperando repartidor: %q", salida)
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la búsqueda/espera sigue viva después de cancelarla")
	}
}

// 05/10: perfiles que no sirven como nombre ("J.L🪽", "@sd2", "😀"): el pedido no se registra con
// eso; se le pregunta el nombre, diciéndole para qué (el repartidor lo necesita para ubicarlo).
func TestPerfilQueNoEsNombreSePreguntaElNombre(t *testing.T) {
	for _, perfil := range []string{"J.L🪽", "@sd2", "😀", "castelar_1963@hotmail.com"} {
		const from = "593999900055"
		store := conversation.NewMemStore()
		ag := agentDePrueba(nil, store)
		store.SetProfile(from, conversation.Profile{PerfilWhatsApp: perfil})
		store.SetLocation(from, -2.885136, -78.986911)

		tur := &turno{}
		salida := ag.registrarPedido(tur, from, map[string]any{"color": "BLANCO", "cantidad": 1})
		if !strings.Contains(salida, PreguntaNombre) || tur.ultimoPedido.faltaDato != "nombre" {
			t.Errorf("%q: debía preguntarse el nombre: %q", perfil, salida)
		}
		// Y si el modelo le pasa el mismo perfil como nombre, tampoco cuenta.
		tur = &turno{}
		salida = ag.registrarPedido(tur, from, map[string]any{"color": "BLANCO", "cantidad": 1, "nombres_completos": perfil})
		if tur.ultimoPedido.faltaDato != "nombre" {
			t.Errorf("%q pasado como nombre no debía aceptarse: %q", perfil, salida)
		}
	}
}

// El nombre que el cliente da queda guardado: la próxima vez no se le pregunta.
func TestElNombreQueDaElClienteSeGuarda(t *testing.T) {
	const from = "593999900056"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetProfile(from, conversation.Profile{PerfilWhatsApp: "@sd2"})
	store.SetLocation(from, -2.885136, -78.986911)

	ag.registrarPedido(&turno{}, from, map[string]any{"color": "BLANCO", "cantidad": 1, "nombres_completos": "Sandra Duchi"})

	if got := conversation.NombreUsable(store, from); got != "Sandra Duchi" {
		t.Errorf("el nombre dado no quedó guardado: %q", got)
	}
}
