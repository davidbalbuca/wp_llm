package agent

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// CANCELAR BUSCA EL PEDIDO DE VERDAD, NO SOLO EL QUE EL BOT RECUERDA.
//
// Pedido del dueño (17/09): al cancelar hay que "verificar si hay algún pedido vigente del
// cliente y cancelarlo".
//
// Antes, sin ActivePedido en memoria, ResponderCancelacion devolvía false y el turno se iba al
// modelo — que respondía algo amable y no cancelaba nada. Pero que el bot no lo recuerde no
// significa que no exista: el bot pudo reiniciarse, la sesión pudo cerrarse por inactividad, o el
// pedido pudo hacerse desde la app móvil. El cliente se quedaba sin poder cancelar por WhatsApp
// teniendo un conductor en camino.
//
// Ahora se le pregunta al backend con el mismo endpoint que usa la app para "mis pedidos"
// (getOrdersHistoryClient?estado=1), y si hay algo vivo se cancela.

// backendConPedidoVigente levanta un backend falso que reporta un pedido EN CAMINO y registra si
// se llamó a cancelOrder.
func backendConPedidoVigente(t *testing.T, idPedido int) (*Agent, conversation.Store, *bool) {
	t.Helper()
	var seCancelo bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "getOrdersHistoryClient"):
			// Solo se responde al filtro por EN CAMINO (estado=1), como hace el backend real.
			if r.URL.Query().Get("estado") != "1" {
				w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[]}`))
				return
			}
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[
                {"idpedido":` + strconv.Itoa(idPedido) + `,"alias":"Casa","direccion":"Av. Solano 123",
                 "conductor":"Nelson","estado_pedido":"En camino","fecha":"2026-09-17"}
            ]}`))
		case strings.Contains(r.URL.Path, "cancelOrder"):
			seCancelo = true
			w.Write([]byte(`{"codigo":0,"mensaje":"Pedido cancelado","resultado":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(srv.URL)
	return ag, store, &seCancelo
}

// Sin pedido en memoria pero CON uno vivo en el backend: se cancela.
func TestSeCancelaElPedidoVivoAunqueElBotNoLoRecuerde(t *testing.T) {
	const from = "593999800001"
	ag, store, seCancelo := backendConPedidoVigente(t, 942)
	// El cliente tiene cuenta, pero el bot NO tiene anotado ningún pedido activo.
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	if _, hay := store.GetActivePedido(from); hay {
		t.Fatal("el montaje no debía dejar un pedido activo en memoria")
	}

	respuesta, manejado := ag.ResponderCancelacion(from, "cancelar mi pedido")
	if !manejado {
		t.Fatal("no se tomó el turno: el cliente con un pedido en camino se queda sin poder cancelar")
	}
	if !*seCancelo {
		t.Error("no se llamó a cancelOrder: el pedido sigue vivo en el backend")
	}
	if !strings.Contains(strings.ToLower(respuesta), "cancel") {
		t.Errorf("la respuesta no confirma la cancelación: %q", respuesta)
	}
}

// Sin pedido en ninguna parte —ni anotado ni en el backend— NO se inventa una cancelación: no se
// llama a cancelOrder y se le dice al cliente la verdad, en código.
//
// Antes este test exigía que el turno fuera al modelo. Eso cambió el 26/09 tras el ticket #56: al
// ceder, el modelo llamaba a la herramienta igual y convertía "no hay cuenta" en un "error
// técnico" con un pedido inventado. Lo que no cambia —y es lo que este test cuida— es que sin
// pedido vigente NADIE toca cancelOrder.
func TestSinPedidoEnNingunaParteNoSeCancelaNada(t *testing.T) {
	const from = "593999800002"
	// idPedido 0 => el backend responde lista vacía para cualquier filtro.
	ag, store, seCancelo := backendConPedidoVigente(t, 0)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	msg, manejado := ag.ResponderCancelacion(from, "cancelar mi pedido")
	if !manejado {
		t.Error("el turno debe resolverse en código en vez de cederse (ticket #56)")
	}
	if *seCancelo {
		t.Error("se llamó a cancelOrder sin pedido vigente")
	}
	if afirmaCancelado(msg) {
		t.Errorf("se le confirma una cancelación que no ocurrió: %q", msg)
	}
}

// Y al cancelar, el ciclo se cierra: la conversación siguiente arranca limpia.
func TestCancelarDejaLaConversacionLimpia(t *testing.T) {
	const from = "593999800003"
	ag, store, _ := backendConPedidoVigente(t, 943)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.AppendUser(from, "quiero 2 blancos")
	store.AppendModel(from, "¡Listo! va en camino")

	if _, manejado := ag.ResponderCancelacion(from, "cancelar mi pedido"); !manejado {
		t.Fatal("no se tomó el turno de cancelación")
	}
	// Queda SOLO el turno de la cancelación (se limpia antes de escribirlo), no el pedido viejo.
	for _, c := range store.History(from) {
		for _, p := range c.Parts {
			if strings.Contains(strings.ToLower(p.Text), "2 blancos") {
				t.Error("el historial conserva el pedido cancelado: la próxima conversación lo arrastra")
			}
		}
	}
}
