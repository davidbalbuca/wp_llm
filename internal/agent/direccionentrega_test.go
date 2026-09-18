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

// EL CLIENTE TIENE QUE SABER A DÓNDE VA SU GAS.
//
// Caso real del 15/09: el cliente compartió seis ubicaciones a lo largo de la tarde (Gualaceo,
// Molleturo, Cuenca, Cumbe...). Al pedir "1 blanco y 2 amarillos" el bot tomó la última en
// silencio y pasó directo a buscar repartidor. El pedido quedó registrado en la vía a Molleturo,
// a 35 km de Cuenca, y el cliente nunca vio esa dirección: confirmó color y cantidad, nada más.
//
// La confirmación llevaba repartidor, placa, valor y enlace de seguimiento. Todo menos el único
// dato que no se puede deshacer. Estos tests fijan que el destino se arme legible; que viaje en
// la confirmación se comprueba en TestLaConfirmacionDelPedidoNombraLaDireccion.

func TestDestinoLegible(t *testing.T) {
	casos := []struct {
		nombre   string
		alias    string
		calle    string
		esperado string
	}{
		{
			// Las dos cosas: el alias le dice cuál de sus direcciones es, la calle confirma que
			// la guardó bien. Por separado cada una deja una duda distinta.
			nombre:   "alias y calle",
			alias:    "Casa",
			calle:    "Av. Solano 123",
			esperado: "Casa (Av. Solano 123)",
		},
		{
			// Guardó la ubicación con nombre pero el backend no resolvió la calle.
			nombre:   "solo alias",
			alias:    "Casa",
			calle:    "",
			esperado: "Casa",
		},
		{
			// Pin nuevo, sin guardar: la calle es lo único que identifica el sitio.
			nombre:   "solo calle",
			alias:    "",
			calle:    "Av. Las Américas 500",
			esperado: "Av. Las Américas 500",
		},
		{
			// Sin nada legible se calla: "-2.802490, -79.304192" no le dice al cliente si eso
			// es su casa o una vía en el Cajas. Decir coordenadas es peor que no decir nada.
			nombre:   "sin datos no inventa coordenadas",
			alias:    "",
			calle:    "",
			esperado: "",
		},
		{
			// Espacios sueltos del backend no deben producir "Casa ()" ni un destino en blanco.
			nombre:   "espacios no cuentan como dato",
			alias:    "  ",
			calle:    "  ",
			esperado: "",
		},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := destinoLegible(c.alias, c.calle); got != c.esperado {
				t.Errorf("destinoLegible(%q, %q) = %q; esperaba %q", c.alias, c.calle, got, c.esperado)
			}
		})
	}
}

// La confirmación que se le pasa al modelo tiene que NOMBRAR la dirección, y hacerlo antes que
// el repartidor: es el dato que el cliente necesita para detener el pedido si está equivocado.
//
// Este test EJERCE registrarPedido de verdad (backend falso incluido). La primera versión armaba
// el mensaje dentro del propio test y pasaba aunque la línea de pedido.go estuviera desactivada:
// un candado falso. Se comprobó apagando el `if` con `false &&` — así el test sí muere.
func TestLaConfirmacionDelPedidoNombraLaDireccion(t *testing.T) {
	const from = "593999300001"
	ag, store, _ := backendQueRegistraConDireccion(t)
	clienteListoParaPedir(store, from)

	mensaje := ag.registrarPedido(&turno{}, from, map[string]any{"color": "BLANCO", "cantidad": 2})

	if !strings.Contains(mensaje, "Av. Solano 123") {
		t.Errorf("la confirmación no nombra la dirección: el cliente aprueba un pedido sin saber "+
			"a dónde va. Mensaje: %q", mensaje)
	}
	posDireccion := strings.Index(mensaje, "Dirección de entrega")
	posRepartidor := strings.Index(mensaje, "Repartidor:")
	if posDireccion == -1 {
		t.Fatalf("falta la etiqueta de dirección de entrega en la confirmación: %q", mensaje)
	}
	if posRepartidor != -1 && posDireccion > posRepartidor {
		t.Error("la dirección va después del repartidor; debe encabezar: es el dato que no se puede deshacer")
	}
}

// backendQueRegistraConDireccion es backendQueRegistra con una dirección guardada que COINCIDE
// con las coordenadas del pedido, para que destinoDelPedido la resuelva y haya algo que nombrar.
func backendQueRegistraConDireccion(t *testing.T) (*Agent, conversation.Store, *[][]map[string]any) {
	t.Helper()
	var capturas [][]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			// Misma coordenada que clienteListoParaPedir: así destinoDelPedido la reconoce.
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[
                {"id":1,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.9,"longitude":-79.0}
            ]}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			var body struct {
				Productos []map[string]any `json:"productos"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturas = append(capturas, body.Productos)
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idpedido":901,"conductorasignado":"Nelson","placa":"ABC123","total":9.5}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"},
		}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	return ag, store, &capturas
}
