package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// lineasDelPedidoVivo se alimenta de LastOrder o de PendingWait, nunca de
// PedidoEnCurso (que es la ficha del pedido NUEVO).
func TestLineasDelPedidoVivo(t *testing.T) {
	t.Run("LastOrder", func(t *testing.T) {
		store := conversation.NewMemStore()
		a := &Agent{store: store}
		store.SetLastOrder("593999", conversation.LastOrder{
			Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2,
		})
		lineas, ok := a.lineasDelPedidoVivo("593999")
		if !ok || len(lineas) != 1 || lineas[0].Color != "BLANCO" || lineas[0].Cantidad != 2 {
			t.Fatalf("LastOrder simple: ok=%v lineas=%v", ok, lineas)
		}
	})

	t.Run("LastOrder multicolor", func(t *testing.T) {
		store := conversation.NewMemStore()
		a := &Agent{store: store}
		store.SetLastOrder("593999", conversation.LastOrder{
			Producto: "GAS 15KG", Color: "AMARILLO", Cantidad: 1,
			Items: []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 2}, {Color: "AMARILLO", Cantidad: 1}},
		})
		lineas, ok := a.lineasDelPedidoVivo("593999")
		if !ok || len(lineas) != 2 {
			t.Fatalf("multicolor: ok=%v lineas=%v", ok, lineas)
		}
	})

	t.Run("PendingWait cuando no hay LastOrder", func(t *testing.T) {
		store := conversation.NewMemStore()
		a := &Agent{store: store}
		store.SetPendingWait("593999", conversation.PendingWait{
			IDProducto: 10, IDColor: 5, Cantidad: 1,
			ProductoNombre: "GAS 15KG", ColorNombre: "AZUL",
		})
		lineas, ok := a.lineasDelPedidoVivo("593999")
		if !ok || len(lineas) != 1 || lineas[0].Color != "AZUL" {
			t.Fatalf("PendingWait: ok=%v lineas=%v", ok, lineas)
		}
	})

	t.Run("sin nada no hay lineas", func(t *testing.T) {
		store := conversation.NewMemStore()
		a := &Agent{store: store}
		if _, ok := a.lineasDelPedidoVivo("593999"); ok {
			t.Fatal("sin LastOrder ni PendingWait debía dar ok=false")
		}
	})
}

// Sin pedido vivo no se intercepta nada: la nueva ubicación es para un pedido
// nuevo y debe seguir al modelo.
func TestCambiarDireccion_SinPedidoVivoNoHaceNada(t *testing.T) {
	store := conversation.NewMemStore()
	a := &Agent{store: store}
	if _, ok := a.CambiarDireccionPorUbicacion("593999", -2.9, -79.0); ok {
		t.Fatal("sin pedido vivo no debía interceptar la ubicación")
	}
}

// El paracaídas del "ya avisé al repartidor" debe detectar las dos frases
// reales que el modelo inventó el 12/09.
func TestAfirmaAvisoAlEquipo_DetectaRepartidor(t *testing.T) {
	casos := []string{
		"Gracias por compartirla 👍 Ya le aviso al equipo para que el repartidor vaya a tu nueva dirección.",
		"Perfecto, anotado 👍 Le paso esa referencia al repartidor para que te encuentre sin problema.",
		"Ya avisé al repartidor para que vaya a tu nueva ubicación.",
		"Le aviso al repartidor que te lleve a la nueva dirección.",
	}
	for _, txt := range casos {
		if !afirmaAvisoAlEquipo(txt) {
			t.Errorf("no detectó aviso al repartidor: %q", txt)
		}
	}
	// Ofrecimiento no es afirmación.
	if afirmaAvisoAlEquipo("¿Quieres que avise al repartidor?") {
		t.Error("un ofrecimiento no debía disparar el candado")
	}
}

// --- Integración: el flujo completo contra un backend falso ---

type contadoresCambio struct {
	cancelaciones int
	pedidos       int
	coberturas    int
}

// backendCambioDireccion cubre TODO el flujo del cambio: cobertura, cancelación del anterior y
// registro del nuevo. `cubierto` decide checkCoverage; `fallaReg` deja el wppOrder del pedido
// NUEVO sin conductor (espera); `fallaCancel` hace fallar la cancelación del anterior.
func backendCambioDireccion(t *testing.T, cubierto, fallaReg, fallaCancel bool) (*Agent, conversation.Store, *contadoresCambio) {
	t.Helper()
	c := &contadoresCambio{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "checkCoverage"):
			c.coberturas++
			if cubierto {
				w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"cubierto":true,"sector":"S","zona":"AZUAY"}}`))
			} else {
				w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"cubierto":false}}`))
			}
		case strings.Contains(r.URL.Path, "cancelOrder"):
			c.cancelaciones++
			if fallaCancel {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"codigo":1,"mensaje":"no se pudo cancelar"}`))
				return
			}
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[]}`))
		case strings.Contains(r.URL.Path, "checkColorAlternatives"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"alternativas":[]}}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			c.pedidos++
			if fallaReg {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"codigo":1,"mensaje":"No hay conductores disponibles"}`))
				return
			}
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idpedido":242,"conductorasignado":"Nelson","placa":"ABC123","total":6.5}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"},
		}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	return ag, store, c
}

// clienteConPedidoEnRuta deja al cliente con el pedido #241 confirmado y vivo, su cuenta y el
// LastOrder de ese pedido (2 x BLANCO): el estado exacto del caso 593959499118 tras registrar.
func clienteConPedidoEnRuta(store conversation.Store, from string) {
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "Cliente Prueba"})
	store.SetActivePedido(from, 241)
	store.SetLastOrder(from, conversation.LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2,
		Latitude: -2.898289, Longitude: -79.003578,
	})
}

// EL CASO REAL: pedido en ruta + ubicación nueva → se cancela el #241 y se genera el #242 a la
// nueva dirección. El cliente NO recibe "ya avisé al repartidor": recibe la verdad.
func TestCambioDireccion_CancelaYReRegistra(t *testing.T) {
	const from = "593959499118"
	ag, store, c := backendCambioDireccion(t, true, false, false)
	clienteConPedidoEnRuta(store, from)
	// cmd/bot guarda la ubicación nueva ANTES de llamar al interceptor; se replica aquí.
	store.SetLocation(from, -2.9100, -79.0200)

	reply, manejado := ag.CambiarDireccionPorUbicacion(from, -2.9100, -79.0200)
	if !manejado {
		t.Fatal("no se resolvió en código: la ubicación nueva caería al modelo, que inventaría " +
			"'ya avisé al repartidor' sin mover nada (caso 593959499118)")
	}
	if c.cancelaciones != 1 {
		t.Errorf("el pedido anterior se canceló %d veces; esperaba 1", c.cancelaciones)
	}
	if c.pedidos != 1 {
		t.Errorf("el pedido nuevo se registró %d veces; esperaba 1", c.pedidos)
	}
	if id, ok := store.GetActivePedido(from); !ok || id != 242 {
		t.Errorf("el pedido activo debería ser el nuevo #242; es %d (ok=%v)", id, ok)
	}
	if !strings.Contains(strings.ToLower(reply), "cancel") || !strings.Contains(strings.ToLower(reply), "nueva") {
		t.Errorf("al cliente no se le explicó el cambio (cancelé anterior + nueva dirección): %q", reply)
	}
	if strings.Contains(strings.ToLower(reply), "avisé al repartidor") || strings.Contains(strings.ToLower(reply), "le paso") {
		t.Errorf("el mensaje repite la mentira del incidente: %q", reply)
	}
}

// FUERA DE COBERTURA la nueva: NO se cancela el anterior (dejaría al cliente sin nada). El pedido
// viejo sigue vivo en su dirección original.
func TestCambioDireccion_NuevaFueraDeCoberturaNoCancela(t *testing.T) {
	const from = "593959499120"
	ag, store, c := backendCambioDireccion(t, false, false, false)
	clienteConPedidoEnRuta(store, from)

	reply, manejado := ag.CambiarDireccionPorUbicacion(from, -1.0, -78.0)
	if !manejado {
		t.Fatal("con pedido vivo debería hacerse cargo aunque la zona nueva no tenga cobertura")
	}
	if c.cancelaciones != 0 {
		t.Errorf("se canceló el anterior pese a que la zona nueva no tiene cobertura (%d)", c.cancelaciones)
	}
	if id, ok := store.GetActivePedido(from); !ok || id != 241 {
		t.Errorf("el pedido original #241 debería seguir vivo; activo=%d ok=%v", id, ok)
	}
	if !strings.Contains(strings.ToLower(reply), "no llegamos") {
		t.Errorf("no se le explicó que la zona nueva no tiene cobertura: %q", reply)
	}
}

// SI LA CANCELACIÓN DEL ANTERIOR FALLA: NO se re-registra (dos pedidos vivos sería peor). Se abre
// ticket y se es honesto; el pedido sigue en su dirección original.
func TestCambioDireccion_SiNoSeCancelaNoReRegistra(t *testing.T) {
	const from = "593959499121"
	ag, store, c := backendCambioDireccion(t, true, false, true)
	clienteConPedidoEnRuta(store, from)

	reply, manejado := ag.CambiarDireccionPorUbicacion(from, -2.91, -79.02)
	if !manejado {
		t.Fatal("debería hacerse cargo del turno para no dejar caer la mentira al modelo")
	}
	if c.pedidos != 0 {
		t.Errorf("se intentó registrar el nuevo pese a que el anterior no se canceló (%d)", c.pedidos)
	}
	if id, ok := store.GetActivePedido(from); !ok || id != 241 {
		t.Errorf("el #241 debería seguir vivo al no poder cancelarlo; activo=%d ok=%v", id, ok)
	}
	if len(store.ListTickets(conversation.TicketAbierto, 10)) == 0 {
		t.Error("no se abrió ticket cuando la cancelación falló")
	}
	_ = reply
}

// CANCELA EL ANTERIOR PERO EL NUEVO NO ENCUENTRA CONDUCTOR: queda en espera; al cliente se le dice
// la verdad, no que su gas ya va en camino.
func TestCambioDireccion_NuevoSinConductorQuedaEnEspera(t *testing.T) {
	const from = "593959499122"
	ag, store, c := backendCambioDireccion(t, true, true, false)
	clienteConPedidoEnRuta(store, from)
	store.SetLocation(from, -2.91, -79.02)

	reply, manejado := ag.CambiarDireccionPorUbicacion(from, -2.91, -79.02)
	if !manejado {
		t.Fatal("debería hacerse cargo del turno")
	}
	if c.cancelaciones != 1 {
		t.Errorf("el anterior debía cancelarse una vez; fueron %d", c.cancelaciones)
	}
	if _, hay := store.GetPendingWait(from); !hay {
		t.Error("el pedido nuevo sin conductor debería quedar en espera (PendingWait)")
	}
	if !strings.Contains(strings.ToLower(reply), "buscando") {
		t.Errorf("no se le dijo al cliente que se está buscando repartidor: %q", reply)
	}
}

// GUARD ESTRUCTURAL: el interceptor tiene que estar cableado en cmd/bot y ANTES de HandleMessage,
// en los TRES caminos por los que entra una ubicación (pin nativo, coordenadas en texto, link
// corto de Maps). Sin esto, alguien borra una llamada y el bug vuelve en silencio por ese camino.
func TestCambioDireccion_CableadoEnElWebhook(t *testing.T) {
	src, err := os.ReadFile("../../cmd/bot/main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	cuerpo := string(src)
	if n := strings.Count(cuerpo, "ag.CambiarDireccionPorUbicacion("); n < 3 {
		t.Fatalf("CambiarDireccionPorUbicacion cableado %d veces; se esperan 3 (pin, texto, link corto)", n)
	}
	iCambio := strings.Index(cuerpo, "ag.CambiarDireccionPorUbicacion(")
	iModelo := strings.Index(cuerpo, "ag.HandleMessage(")
	if iModelo > 0 && iCambio > iModelo {
		t.Error("CambiarDireccionPorUbicacion está DESPUÉS de HandleMessage: el modelo contestaría primero")
	}
}
