package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Edison (12/10): "Disculpe ya no deseo nada" con William a la vuelta de su casa, y el bot
// canceló en el acto. Ahora se le pregunta primero.

func edisonConPedidoEnCamino(t *testing.T) (*Agent, conversation.Store, *[]string, *int) {
	t.Helper()
	const from = "593986243375"
	var mu sync.Mutex
	cancelados := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(strings.ToLower(r.URL.Path), "cancel"):
			cancelados++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		default:
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	store := conversation.NewMemStore()
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetActivePedido(from, 442)
	store.SetSeguimientoActivo(from, "https://ws11.geoware.lat/seguimiento/NDQy/")
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(srv.URL)
	var menus []string
	ag.enviarMenu = func(_, cuerpo string, opciones []string) error {
		menus = append(menus, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
		return nil
	}
	return ag, store, &menus, &cancelados
}

func TestConElRepartidorEnCaminoSePreguntaAntesDeCancelar(t *testing.T) {
	const from = "593986243375"
	ag, store, menus, cancelados := edisonConPedidoEnCamino(t)

	if _, manejado := ag.ResponderCancelacion(from, "cancela mi pedido"); !manejado {
		t.Fatal("la orden de cancelar no se atendió")
	}
	if len(*menus) != 1 || !strings.Contains((*menus)[0], "camino") || !strings.Contains((*menus)[0], "seguimiento") ||
		!strings.Contains((*menus)[0], BotonSiCancelar) {
		t.Fatalf("no se le preguntó con el dato de su repartidor y el enlace: %v", *menus)
	}
	if *cancelados != 0 {
		t.Error("se canceló sin que el cliente confirmara")
	}
	if id, _ := store.GetActivePedido(from); id != 442 {
		t.Errorf("el pedido dejó de estar activo: %d", id)
	}
}

func TestSiDecideEsperarElPedidoSigue(t *testing.T) {
	const from = "593986243375"
	ag, store, _, cancelados := edisonConPedidoEnCamino(t)
	ag.ResponderCancelacion(from, "cancelar")

	reply, manejado := ag.ResponderConfirmacionCancelacion(from, BotonNoLoEspero)
	if !manejado || !strings.Contains(reply, "camino") {
		t.Fatalf("no se resolvió el 'No, lo espero': %q", reply)
	}
	if *cancelados != 0 {
		t.Error("se canceló aunque dijo que esperaba")
	}
	if id, _ := store.GetActivePedido(from); id != 442 {
		t.Errorf("el pedido dejó de estar activo: %d", id)
	}
	// Ya contestó: la pregunta no queda colgada.
	if _, manejado := ag.ResponderConfirmacionCancelacion(from, "ok"); manejado {
		t.Error("la pregunta siguió pendiente después de contestar")
	}
}

func TestSiConfirmaSeCancela(t *testing.T) {
	const from = "593986243375"
	ag, store, _, cancelados := edisonConPedidoEnCamino(t)
	ag.ResponderCancelacion(from, "cancelar")

	reply, manejado := ag.ResponderConfirmacionCancelacion(from, BotonSiCancelar)
	if !manejado || !strings.Contains(strings.ToLower(reply), "cancel") {
		t.Fatalf("no se resolvió el 'Sí, cancelar': %q", reply)
	}
	if *cancelados != 1 {
		t.Errorf("el backend recibió %d cancelaciones, esperaba 1", *cancelados)
	}
	if _, hay := store.GetActivePedido(from); hay {
		t.Error("el pedido sigue activo después de confirmar")
	}
}

func TestSinPedidoEnCaminoNoSePregunta(t *testing.T) {
	ag, store, menus, _ := edisonConPedidoEnCamino(t)
	store.ClearActivePedido("593986243375")
	if _, preguntado := ag.preguntarAntesDeCancelar(&turno{}, "593986243375"); preguntado || len(*menus) != 0 {
		t.Errorf("sin pedido en camino no hay nada que preguntar: %v", *menus)
	}
}

// Lo que se le preguntó queda en el historial (el modelo tiene que saber que se le preguntó).
func TestLaPreguntaQuedaEnElHistorial(t *testing.T) {
	const from = "593986243375"
	ag, store, _, _ := edisonConPedidoEnCamino(t)
	ag.ResponderCancelacion(from, "cancelar")
	hist := store.History(from)
	if len(hist) == 0 || !strings.Contains(hist[len(hist)-1].Parts[0].Text, BotonNoLoEspero) {
		t.Errorf("el historial no tiene la pregunta: %v", hist)
	}
}
