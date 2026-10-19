package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// 18/10 (dueño): al cliente se le presenta su repartidor recién cuando ACEPTA. Mientras tanto,
// "estoy confirmando con el repartidor más cercano": sin nombre, placa, tiempo ni enlace.
func backendPorConfirmar(t *testing.T, terminaOtra bool) (*Agent, conversation.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[]}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			res, _ := json.Marshal(map[string]any{"idpedido": 901, "conductorasignado": "Nelson Loja",
				"placa": "ABC123", "total": 3.5, "tiempo": "00:09:00", "seguimiento_token": "tok",
				"por_confirmar": true, "termina_otra_entrega": terminaOtra})
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":` + string(res) + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", SeguimientoBaseURL: "https://ws11.geoware.lat"}
	return ag, store
}

func TestPorConfirmarNoSePresentaAlRepartidor(t *testing.T) {
	const from = "593999000901"
	ag, store := backendPorConfirmar(t, false)
	clienteListoParaPedir(store, from)
	defer QuitarPorConfirmar(ag.cfg, from)

	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{"color": "BLANCO", "cantidad": float64(1)})
	if !tn.ultimoPedido.ok || !tn.ultimoPedido.PorConfirmar {
		t.Fatalf("el pedido no quedó registrado como por confirmar: %+v", tn.ultimoPedido)
	}
	for _, dato := range []string{"Nelson", "ABC123", "seguimiento/tok", "Tiempo estimado"} {
		if strings.Contains(salida, dato) {
			t.Errorf("al modelo se le pasó %q de un repartidor que no aceptó: %s", dato, salida)
		}
	}
	if !strings.Contains(salida, "TODAVÍA NO ACEPTÓ") || !strings.Contains(salida, "Valor a pagar: $3.50") {
		t.Errorf("falta la instrucción o el valor: %s", salida)
	}
	if tn.ultimoPedido.Conductor != "" || tn.ultimoPedido.Seguimiento != "" || tn.ultimoPedido.MinutosEntrega != 0 {
		t.Errorf("quedaron datos del repartidor: %+v", tn.ultimoPedido)
	}
	if enlace := store.GetSeguimientoActivo(from); enlace != "" {
		t.Errorf("quedó un enlace de seguimiento activo: %q", enlace)
	}
	// El mensaje del código tampoco lo presenta, y el candado corrige al modelo si lo hace.
	if msg := ag.mensajeDelPedido(tn, from); strings.Contains(msg, "va en camino") || !strings.Contains(msg, "confirmando") {
		t.Errorf("mensaje del pedido: %q", msg)
	}
	if got := ag.revisarRepartidorPrometido(from, "Tu repartidor es Nelson y llega en 9 min"); strings.Contains(got, "Nelson") {
		t.Errorf("el candado dejó pasar al repartidor que no aceptó: %q", got)
	}
}

func TestPorConfirmarTerminandoOtraEntrega(t *testing.T) {
	const from = "593999000902"
	ag, store := backendPorConfirmar(t, true)
	clienteListoParaPedir(store, from)
	defer QuitarPorConfirmar(ag.cfg, from)
	tn := &turno{}
	ag.runTool(tn, from, "registrar_pedido", map[string]any{"color": "BLANCO", "cantidad": float64(1)})
	if msg := ag.mensajeDelPedido(tn, from); !strings.Contains(msg, "terminando una entrega") {
		t.Errorf("no se le explica que está terminando otra entrega: %q", msg)
	}
}

func TestMensajesDelAnuncio(t *testing.T) {
	if m := MensajeRepartidorAcepto("Nelson", "ABC123", 9, ""); !strings.Contains(m, "aceptó") ||
		!strings.Contains(m, "Nelson") || strings.Contains(m, "nuevo repartidor") {
		t.Errorf("aviso de aceptado: %q", m)
	}
	if m := MensajeSinRepartidor(901); strings.Contains(m, "cancelado por el conductor") || !strings.Contains(m, "#901") {
		t.Errorf("cierre sin repartidor: %q", m)
	}
}
