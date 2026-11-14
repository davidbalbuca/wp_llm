package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Pedido del bot por ANILLOS (14/11, #1022): antes de wppOrder se le pregunta al backend si va por
// anillos (buscarConductor con solo_broadcast). Si sí, nadie queda asignado hasta que acepte.

type backendBroadcast struct {
	mu        sync.Mutex
	respuesta string // resultado de buscarConductor
	busquedas []map[string]any
	wppOrders int
}

func nuevoBackendBroadcast(t *testing.T, respuesta string) (*backendBroadcast, *Agent, conversation.Store) {
	t.Helper()
	b := &backendBroadcast{respuesta: respuesta}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "buscarConductor"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b.busquedas = append(b.busquedas, body)
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":` + b.respuesta + `}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[]}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			b.wppOrders++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idpedido":900,"conductorasignado":"Nelson","placa":"ABC123","total":3.25}}`))
		default:
			// login, direcciones: lo que necesita el flujo para llegar al pedido.
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	ag, store, _ := backendQueRegistra(t)
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg.UsarBusquedaBackend = true
	// La espera se queda dormida: aquí solo importa cómo arranca, no lo que consulta después.
	antes := intervaloBusqueda
	intervaloBusqueda = time.Hour
	t.Cleanup(func() { intervaloBusqueda = antes })
	return b, ag, store
}

func (b *backendBroadcast) contar() (busquedas []map[string]any, wppOrders int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]any(nil), b.busquedas...), b.wppOrders
}

func pedirUnBlanco(ag *Agent, from string) (*turno, string) {
	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{
		"items": []any{map[string]any{"color": "BLANCO", "cantidad": float64(1)}},
	})
	return tn, salida
}

func TestBroadcastNadieQuedaAsignadoHastaQueAcepte(t *testing.T) {
	const from = "593999300001"
	b, ag, store := nuevoBackendBroadcast(t, `{"idbusqueda":77,"estado":"BUSCANDO"}`)
	clienteListoParaPedir(store, from)

	tn, salida := pedirUnBlanco(ag, from)

	busquedas, wppOrders := b.contar()
	if wppOrders != 0 {
		t.Fatalf("con broadcast se llamó a wppOrder %d veces: el pedido nacería asignado", wppOrders)
	}
	if len(busquedas) != 1 || busquedas[0]["solo_broadcast"] != true {
		t.Fatalf("no se pidió la búsqueda con solo_broadcast: %v", busquedas)
	}
	if salida != mensajeConfirmandoRepartidor {
		t.Errorf("al cliente no se le dice que estamos confirmando con el más cercano: %q", salida)
	}
	if !tn.ultimoPedido.enEspera {
		t.Error("el pedido no quedó en espera de que alguien acepte")
	}
	w, ok := store.GetPendingWait(from)
	if !ok || w.IDBusqueda != 77 || w.ColorNombre != "BLANCO" {
		t.Errorf("la espera no sigue la búsqueda 77 del broadcast: %+v (ok=%v)", w, ok)
	}
}

func TestSinBroadcastElPedidoSigueComoSiempre(t *testing.T) {
	const from = "593999300002"
	b, ag, store := nuevoBackendBroadcast(t, `{"idbusqueda":null,"estado":"SIN_BROADCAST"}`)
	clienteListoParaPedir(store, from)

	tn, _ := pedirUnBlanco(ag, from)

	if _, wppOrders := b.contar(); wppOrders != 1 {
		t.Fatalf("sin broadcast el pedido debía ir por wppOrder una vez; fueron %d", wppOrders)
	}
	if !tn.ultimoPedido.ok || tn.ultimoPedido.IDPedido != 900 {
		t.Errorf("el pedido de siempre no quedó registrado: %+v", tn.ultimoPedido)
	}
}

func TestBackendQueAsignaEnElActoNoDuplicaElPedido(t *testing.T) {
	// Un backend que no conoce solo_broadcast responde como buscarConductor: si asignó, el pedido
	// YA existe y pedir otro con wppOrder sería duplicarlo.
	const from = "593999300003"
	b, ag, store := nuevoBackendBroadcast(t,
		`{"idbusqueda":78,"estado":"ASIGNADO","pedido":{"idpedido":901,"conductorasignado":"Nelson","placa":"ABC123","total":3.25}}`)
	clienteListoParaPedir(store, from)

	tn, _ := pedirUnBlanco(ag, from)

	if _, wppOrders := b.contar(); wppOrders != 0 {
		t.Fatalf("con el pedido ya asignado se pidió otro (%d wppOrder)", wppOrders)
	}
	if !tn.ultimoPedido.ok || tn.ultimoPedido.IDPedido != 901 {
		t.Errorf("el pedido asignado por la búsqueda no quedó como el pedido del cliente: %+v", tn.ultimoPedido)
	}
}

func TestSinLaBusquedaDelBackendNiSePregunta(t *testing.T) {
	const from = "593999300004"
	b, ag, store := nuevoBackendBroadcast(t, `{"idbusqueda":77,"estado":"BUSCANDO"}`)
	ag.cfg.UsarBusquedaBackend = false
	clienteListoParaPedir(store, from)

	pedirUnBlanco(ag, from)

	if busquedas, wppOrders := b.contar(); len(busquedas) != 0 || wppOrders != 1 {
		t.Errorf("con BOT_USAR_BUSQUEDA apagado debía ir directo a wppOrder: %d búsquedas, %d wppOrder",
			len(busquedas), wppOrders)
	}
}
