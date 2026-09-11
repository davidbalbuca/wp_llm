package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Fase B.4 — usar las direcciones guardadas como destino (specs/direcciones-y-pedido-multicolor.md).
//
// El caso que esto arregla (David, 10/09): tenía una ubicación guardada, escribió "Ubicación
// tienda", "Tienda", tres veces, y el bot solo sabía pedirle el clip 📎 otra vez. Le enseñamos
// a nombrar sus lugares y no lo dejábamos usarlos.

// agentConDireccionesYPedido arma un agente con backend falso (direcciones + registro), un
// cliente con cuenta, y una ficha COMPLETA (el pedido ya está armado; falta el destino).
func agentConDireccionesYPedido(t *testing.T, from, direcciones string) (*Agent, conversation.Store, *int) {
	t.Helper()
	llamadasOrder := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":` + direcciones + `}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			llamadasOrder++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idpedido":901,"conductorasignado":"Nelson"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "David Espinoza"})
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{
		Color: "BLANCO", Cantidad: 1, Flujo: conversation.FlujoInmediato,
	})

	ag := agentDePrueba(nil, store)
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	return ag, store, &llamadasOrder
}

const direccionesDeDavid = `[
	{"id":1,"alias":"WhatsApp","direccion":"x","latitude":-2.90,"longitude":-79.00},
	{"id":2,"alias":"Tienda","direccion":"Av. Loja 456","latitude":-2.91,"longitude":-79.01},
	{"id":3,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.92,"longitude":-79.02}
]`

// INCIDENTE 10/09 — "Ubicación tienda" registra el pedido a Tienda, en código.
func TestIncidente_UbicacionTiendaResuelveElDestino(t *testing.T) {
	const from = "593999300001"
	for _, texto := range []string{"Ubicación tienda", "Tienda", "a la tienda", "envíalo a la tienda"} {
		ag, store, llamadas := agentConDireccionesYPedido(t, from, direccionesDeDavid)

		reply, manejado := ag.ResponderDireccionGuardada(from, texto)
		if !manejado {
			t.Errorf("%q no se resolvió en código: el cliente recibiría \"compárteme el 📎\" otra vez", texto)
			continue
		}
		if *llamadas != 1 {
			t.Errorf("%q: el pedido no se registró (%d llamadas a wppOrder)", texto, *llamadas)
		}
		if !strings.Contains(reply, "Tienda") {
			t.Errorf("%q: la respuesta no confirma el destino elegido: %q", texto, reply)
		}
		// La ubicación del pedido quedó con las coordenadas GUARDADAS de Tienda.
		loc, hay := store.GetLocation(from)
		if !hay || loc.Latitude != -2.91 || loc.Longitude != -79.01 {
			t.Errorf("%q: las coordenadas no son las de Tienda: %+v", texto, loc)
		}
	}
}

// REGLA DURA (B.4): un texto que NO es una dirección guardada jamás se vuelve destino. Ni
// direcciones escritas a mano, ni frases que mencionan el alias sin elegirlo.
func TestDireccionEscritaAManoNoEsUnDestino(t *testing.T) {
	const from = "593999300002"
	for _, texto := range []string{
		"Tarqui y Sucre frente al hotel", // dirección a mano: prohibida
		"la tienda queda lejos",          // menciona el alias dentro de una frase
		"¿me lo mandas a la tienda?",     // pregunta, no elección (lleva ?)
		"oficina",                        // un alias que NO tiene guardado
		"hola",
	} {
		ag, _, llamadas := agentConDireccionesYPedido(t, from, direccionesDeDavid)
		if _, manejado := ag.ResponderDireccionGuardada(from, texto); manejado {
			t.Errorf("%q se tomó como destino; el gas saldría a una dirección no verificada", texto)
		}
		if *llamadas != 0 {
			t.Errorf("%q: se registró un pedido sin destino válido", texto)
		}
	}
}

// Sin pedido completo no se intercepta nada: "Casa" a mitad de conversación es charla del
// modelo, no la elección de un destino.
func TestSinPedidoCompletoElAliasVaAlModelo(t *testing.T) {
	const from = "593999300003"
	ag, store, llamadas := agentConDireccionesYPedido(t, from, direccionesDeDavid)
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO"}) // sin cantidad

	if _, manejado := ag.ResponderDireccionGuardada(from, "Casa"); manejado {
		t.Error("sin pedido completo no hay destino que resolver: le toca al modelo")
	}
	if *llamadas != 0 {
		t.Error("se registró un pedido a medias")
	}
}

// Con una ubicación FRESCA no se intercepta: el destino ya está resuelto, y así el texto libre
// le queda al menú de "¿la guardo con un nombre?" (que corre después).
func TestConUbicacionFrescaNoSeIntercepta(t *testing.T) {
	const from = "593999300004"
	ag, store, _ := agentConDireccionesYPedido(t, from, direccionesDeDavid)
	store.SetLocation(from, -2.9, -79.0) // recién compartida

	if _, manejado := ag.ResponderDireccionGuardada(from, "Casa"); manejado {
		t.Error("con ubicación fresca el destino ya está: \"Casa\" es el nombre para guardarla")
	}
}

// El botón "Otra ubicación" pide el pin de siempre sin registrar nada.
func TestOtraUbicacionPideElPin(t *testing.T) {
	const from = "593999300005"
	ag, _, llamadas := agentConDireccionesYPedido(t, from, direccionesDeDavid)

	reply, manejado := ag.ResponderDireccionGuardada(from, BotonOtraUbicacion)
	if !manejado || !strings.Contains(reply, "ubicación") {
		t.Fatalf("el botón de otra ubicación debía pedir el pin: %q (manejado=%v)", reply, manejado)
	}
	if *llamadas != 0 {
		t.Error("no había destino elegido y se registró un pedido")
	}
}

// Sin direcciones guardadas (cliente nuevo, o solo la interna 'WhatsApp') nada cambia: el
// texto va al modelo y el prompt no ofrece ningún menú de direcciones.
func TestSinDireccionesGuardadasTodoSigueIgual(t *testing.T) {
	const from = "593999300006"
	ag, _, _ := agentConDireccionesYPedido(t, from,
		`[{"id":1,"alias":"WhatsApp","direccion":"x","latitude":-2.9,"longitude":-79.0}]`)

	if _, manejado := ag.ResponderDireccionGuardada(from, "Casa"); manejado {
		t.Error("sin direcciones con nombre no hay nada que interceptar")
	}
	if _, vol := ag.construirSistema(from); strings.Contains(vol, "DIRECCIONES GUARDADAS") {
		t.Error("el prompt ofrece direcciones que el cliente no tiene")
	}
}

// EL PROMPT — con direcciones guardadas y sin ubicación fresca, el modelo recibe la lista, la
// instrucción del menú y la prohibición de la dirección a mano.
func TestPromptOfreceLasDireccionesGuardadas(t *testing.T) {
	const from = "593999300007"
	ag, _, _ := agentConDireccionesYPedido(t, from, direccionesDeDavid)

	_, vol := ag.construirSistema(from)
	for _, quiero := range []string{"DIRECCIONES GUARDADAS", "Tienda", "Casa", BotonOtraUbicacion, "escrita a mano"} {
		if !strings.Contains(vol, quiero) {
			t.Errorf("el prompt no menciona %q:\n%s", quiero, vol)
		}
	}
	// La interna 'WhatsApp' nunca se le ofrece al cliente.
	if strings.Contains(vol, `"WhatsApp"`) {
		t.Errorf("el alias interno WhatsApp se ofreció como dirección:\n%s", vol)
	}
}
