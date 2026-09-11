package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// La CANCELACIÓN se resuelve en código, no por el modelo.
//
// El dato que motivó esto (producción, 11/09): veces que el modelo llamó cancelar_pedido = 0;
// veces que el candado de frases tuvo que forzarlo = 6. Todas las cancelaciones de clientes
// reales las salvó el paracaídas, que se dispara leyendo el texto del modelo y falla si elige
// otras palabras — como el 09/09, cuando el cliente insistió tres veces con el pedido vivo.

// backendQueCancela levanta un backend falso que acepta login + cancelOrder y cuenta las
// cancelaciones que le llegan de verdad.
func backendQueCancela(t *testing.T, fallaCancelacion bool) (*Agent, conversation.Store, *int) {
	t.Helper()
	llamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "cancelOrder"):
			llamadas++
			if fallaCancelacion {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"codigo":1,"mensaje":"error interno del backend"}`))
				return
			}
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(srv.URL)
	return ag, store, &llamadas
}

// clienteConPedidoVivo deja al cliente con un pedido activo y cuenta para cancelarlo.
func clienteConPedidoVivo(store conversation.Store, from string) {
	store.SetActivePedido(from, 512)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
}

// EL CASO REAL: el cliente escribe "Cancelar pedido" y el CÓDIGO lo cancela — sin que el
// modelo tenga que acordarse de llamar la herramienta, y sin depender de cómo redacte.
func TestIncidente_LaCancelacionNoDependeDelModelo(t *testing.T) {
	const from = "593992883555" // David, el de las tres cancelaciones del 11/09
	ag, store, llamadas := backendQueCancela(t, false)
	clienteConPedidoVivo(store, from)

	reply, manejado := ag.ResponderCancelacion(from, "Cancelar pedido")
	if !manejado {
		t.Fatal("\"Cancelar pedido\" no se resolvió en código: la cancelación seguiría dependiendo " +
			"de que el modelo llame la herramienta (0 de 6 veces en producción)")
	}
	if *llamadas != 1 {
		t.Fatalf("el backend recibió %d cancelaciones; esperaba 1", *llamadas)
	}
	if _, sigue := store.GetActivePedido(from); sigue {
		t.Error("el pedido sigue activo tras cancelar")
	}
	if !strings.Contains(reply, "cancelé tu pedido") {
		t.Errorf("no se le confirmó la cancelación al cliente: %q", reply)
	}
}

// Todas las formas en que la gente pide cancelar, incluido el botón.
func TestCancelacionReconoceLasFormasDelCliente(t *testing.T) {
	for i, texto := range []string{
		"Cancelar pedido", "cancelar", "Cancela mi pedido", "anula el pedido",
		"ya no lo quiero", "quiero cancelar", "cancélame por favor el pedido",
		"mejor cancela", "ya no lo necesito",
	} {
		from := "5939990006" + string(rune('0'+i%10))
		ag, store, llamadas := backendQueCancela(t, false)
		clienteConPedidoVivo(store, from)

		if _, manejado := ag.ResponderCancelacion(from, texto); !manejado {
			t.Errorf("%q no se resolvió en código", texto)
			continue
		}
		if *llamadas != 1 {
			t.Errorf("%q: el backend no recibió la cancelación (%d llamadas)", texto, *llamadas)
		}
	}
}

// REGLA DURA: lo ambiguo NO se cancela. Cancelar el pedido de quien no lo pidió es peor que
// dejar pasar un mensaje al modelo.
func TestCancelacionNoActuaSobreLoAmbiguo(t *testing.T) {
	const from = "593999000700"
	for _, texto := range []string{
		"¿puedo cancelar mi pedido?",        // pregunta
		"¿me cancelas?",                     // pregunta
		"cancela la programación",           // otra herramienta (cancelar_programacion)
		"quiero cancelar mi entrega agendada", // programación, no pedido
		"el repartidor canceló?",            // pregunta sobre un tercero
		"hola",
		"gracias",
		"quiero 2 blancos",
	} {
		ag, store, llamadas := backendQueCancela(t, false)
		clienteConPedidoVivo(store, from)

		if _, manejado := ag.ResponderCancelacion(from, texto); manejado {
			t.Errorf("%q se tomó como orden de cancelar; debía ir al modelo", texto)
		}
		if *llamadas != 0 {
			t.Errorf("%q: se canceló un pedido que el cliente no pidió cancelar", texto)
		}
	}
}

// Sin pedido activo no se intercepta: "cancelar" puede referirse a otra cosa.
func TestSinPedidoActivoLaCancelacionVaAlModelo(t *testing.T) {
	const from = "593999000701"
	ag, store, llamadas := backendQueCancela(t, false)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"}) // sin pedido activo

	if _, manejado := ag.ResponderCancelacion(from, "cancelar pedido"); manejado {
		t.Error("sin pedido activo no hay nada que cancelar en código")
	}
	if *llamadas != 0 {
		t.Error("se llamó al backend sin pedido que cancelar")
	}
}

// Si el backend NO cancela, al cliente se le dice la verdad y queda un ticket. Nunca se le
// confirma una cancelación que no ocurrió (el fallo del 09/09).
func TestSiElBackendNoCancelaSeDiceLaVerdad(t *testing.T) {
	const from = "593999000702"
	ag, store, _ := backendQueCancela(t, true) // el backend rechaza la cancelación
	clienteConPedidoVivo(store, from)

	reply, manejado := ag.ResponderCancelacion(from, "cancelar pedido")
	if !manejado {
		t.Fatal("el interceptor debía hacerse cargo igualmente")
	}
	if strings.Contains(reply, "cancelé tu pedido") {
		t.Errorf("se le confirmó una cancelación que NO ocurrió: %q", reply)
	}
	if !strings.Contains(reply, "problema al cancelar") {
		t.Errorf("no se le dijo la verdad al cliente: %q", reply)
	}
	if _, sigue := store.GetActivePedido(from); !sigue {
		t.Error("el pedido debía seguir activo: el backend no lo canceló")
	}
}

// GUARD ESTRUCTURAL: la cancelación tiene que estar cableada en cmd/bot como los demás
// interceptores. Sin esto, alguien puede borrar la llamada y los tests de arriba seguirían
// verdes mientras en producción la cancelación vuelve a depender del modelo — que es
// exactamente lo que pasó durante semanas.
func TestLaCancelacionEstaCableadaEnElWebhook(t *testing.T) {
	src, err := os.ReadFile("../../cmd/bot/main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	cuerpo := string(src)
	if !strings.Contains(cuerpo, "ag.ResponderCancelacion(") {
		t.Fatal("cmd/bot NO llama a ResponderCancelacion: la cancelación volvería a depender de " +
			"que el modelo llame la herramienta (0 de 6 veces en producción, 11/09)")
	}
	// Y debe ir ANTES de HandleMessage: si se cablea después, el modelo ya contestó.
	iCancel := strings.Index(cuerpo, "ag.ResponderCancelacion(")
	iModelo := strings.Index(cuerpo, "ag.HandleMessage(")
	if iModelo > 0 && iCancel > iModelo {
		t.Error("ResponderCancelacion está cableado DESPUÉS de HandleMessage: el modelo contestaría primero")
	}
}
