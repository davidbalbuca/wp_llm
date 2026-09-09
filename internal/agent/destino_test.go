package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// backendConDirecciones levanta un backend falso que devuelve las direcciones dadas en
// getDirectionsClient. `cuerpo` es el JSON del array de direcciones.
func backendConDirecciones(t *testing.T, cuerpo string) *georoutes.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "getDirectionsClient") {
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":` + cuerpo + `}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return georoutes.NewClient(srv.URL)
}

// El destino se busca por COORDENADAS, no por posición en la lista. Un cliente puede tener
// varias direcciones guardadas (Casa, Trabajo, y la 'WhatsApp' que el bot pisa en cada pedido):
// quedarse con la primera le diría "te lo envío a Trabajo" un pedido que fue a su casa.
func TestElDestinoSaleDeLaDireccionQueCoincideConElPedido(t *testing.T) {
	const from = "593999000090"
	// Trabajo está lejos (~1 km); Casa es donde se hizo el pedido.
	gr := backendConDirecciones(t, `[
        {"id":1,"alias":"Trabajo","direccion":"Av. Las Americas 500","latitude":-2.888,"longitude":-79.002},
        {"id":2,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.898,"longitude":-79.002}
    ]`)
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.gr = gr

	alias, direccion := ag.destinoDelPedido(from, "jwt", -2.898, -79.002)
	if alias != "Casa" {
		t.Errorf("se tomó la dirección equivocada: alias=%q (el pedido fue a Casa)", alias)
	}
	if direccion != "Av. Solano 123" {
		t.Errorf("calle equivocada: %q", direccion)
	}
}

// 'WhatsApp' es el alias INTERNO que pone el backend, no un nombre que el cliente eligió:
// nunca puede mostrarse ("te lo envío a WhatsApp" no significa nada). Sí se aprovecha su calle.
func TestElAliasInternoWhatsAppNoSeLeMuestraAlCliente(t *testing.T) {
	const from = "593999000091"
	gr := backendConDirecciones(t, `[
        {"id":1,"alias":"WhatsApp","direccion":"Av. Solano 123","latitude":-2.898,"longitude":-79.002}
    ]`)
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.gr = gr

	alias, direccion := ag.destinoDelPedido(from, "jwt", -2.898, -79.002)
	if alias != "" {
		t.Errorf("se le mostraría al cliente el alias interno del backend: %q", alias)
	}
	if direccion != "Av. Solano 123" {
		t.Errorf("la calle de esa dirección sí sirve y se perdió: %q", direccion)
	}
}

// El backend guarda un texto de respaldo con las coordenadas cuando Google no reconoce la
// calle. Ese texto no se le puede mostrar a nadie: es peor que no decir el destino.
func TestLaDireccionDeRespaldoConCoordenadasNoSeMuestra(t *testing.T) {
	const from = "593999000092"
	gr := backendConDirecciones(t, `[
        {"id":1,"alias":"WhatsApp","direccion":"Ubicación compartida por WhatsApp (-2.898000, -79.002000)","latitude":-2.898,"longitude":-79.002}
    ]`)
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.gr = gr

	alias, direccion := ag.destinoDelPedido(from, "jwt", -2.898, -79.002)
	if alias != "" || direccion != "" {
		t.Errorf("el texto de respaldo con coordenadas llegó al cliente: alias=%q dir=%q", alias, direccion)
	}
}

// REGLA DURA: si el pedido se hizo en un sitio nuevo, NO se le asigna el nombre de otra
// dirección guardada. El cliente confirmaría un envío a una dirección equivocada.
func TestUbicacionNuevaNoHeredaElNombreDeOtraDireccion(t *testing.T) {
	const from = "593999000093"
	gr := backendConDirecciones(t, `[
        {"id":1,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.898,"longitude":-79.002}
    ]`)
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.gr = gr

	// Pedido desde un sitio a ~1 km de Casa.
	alias, direccion := ag.destinoDelPedido(from, "jwt", -2.888, -79.002)
	if alias != "" || direccion != "" {
		t.Errorf("una ubicación nueva heredó los datos de Casa: alias=%q dir=%q", alias, direccion)
	}
}

// Best-effort: el pedido YA está registrado. Si la consulta de direcciones falla, se guarda sin
// destino y el cliente no se entera — nunca un error.
func TestSiFallaLaConsultaDeDireccionesElPedidoNoSeRompe(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.gr = georoutes.NewClient("http://127.0.0.1:1") // backend muerto

	alias, direccion := ag.destinoDelPedido("593999000094", "jwt", -2.898, -79.002)
	if alias != "" || direccion != "" {
		t.Errorf("con el backend caído no puede inventarse un destino: alias=%q dir=%q", alias, direccion)
	}
}
