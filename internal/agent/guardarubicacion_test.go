package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// backendDirecciones levanta un backend falso que devuelve `guardadas` en getDirectionsClient y
// registra los cuerpos que recibe en createDirectionClient (para poder auditarlos).
type backendDirecciones struct {
	mu       sync.Mutex
	creadas  []map[string]any
	rechazar bool
}

func (b *backendDirecciones) cliente(t *testing.T, guardadas string) *georoutes.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":` + guardadas + `}`))
		case strings.Contains(r.URL.Path, "createDirectionClient"):
			if b.rechazar {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"codigo":-1,"mensaje":"alias duplicado"}`))
				return
			}
			cuerpo, _ := io.ReadAll(r.Body)
			var m map[string]any
			json.Unmarshal(cuerpo, &m)
			b.mu.Lock()
			b.creadas = append(b.creadas, m)
			b.mu.Unlock()
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"id":9}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return georoutes.NewClient(srv.URL)
}

// agenteConCuenta arma un Agent con un cliente ya autenticado (tiene JWT), que es la
// precondición para poder guardar direcciones a su nombre.
func agenteConCuenta(t *testing.T, store conversation.Store, from string, gr *georoutes.Client) *Agent {
	t.Helper()
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	ag := agentDePrueba(nil, store)
	ag.gr = gr
	return ag
}

// REGLA DURA de la spec (B.3): `principal` va SIEMPRE en false. El backend, al recibir
// principal=true, APAGA la dirección principal que el cliente tenga de la app móvil
// (client_selectors.py: recorre sus direcciones y las pone en false). El bot no puede
// reconfigurarle la app por guardar una ubicación de WhatsApp.
func TestGuardarUbicacionNuncaCambiaLaDireccionPrincipal(t *testing.T) {
	const from = "593999000100"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	if _, manejado := ag.ResponderGuardarUbicacion(from, BotonGuardarCasa); !manejado {
		t.Fatal("el botón de guardar no se resolvió en código")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.creadas) != 1 {
		t.Fatalf("se esperaba UNA dirección creada, hubo %d", len(b.creadas))
	}
	if principal, _ := b.creadas[0]["principal"].(bool); principal {
		t.Error("se mandó principal=true: el backend desactivaría la dirección principal " +
			"que el cliente tiene en la app móvil")
	}
	if b.creadas[0]["alias"] != "Casa" {
		t.Errorf("alias equivocado: %v", b.creadas[0]["alias"])
	}
}

// El cliente puede escribir el nombre que quiera: el dueño pidió "que lo ponga, por ejemplo
// casa", así que los botones son un atajo y no una jaula.
func TestElClientePuedeEscribirElNombreQueQuiera(t *testing.T) {
	const from = "593999000101"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	reply, manejado := ag.ResponderGuardarUbicacion(from, "Casa de mi mamá")
	if !manejado {
		t.Fatal("un nombre escrito a mano debía aceptarse")
	}
	if !strings.Contains(reply, "Casa de mi mamá") {
		t.Errorf("la confirmación no nombra la dirección guardada: %q", reply)
	}
}

// Lo que NO es una etiqueta va al modelo. Guardar "cuanto cuesta el de quince" como nombre de
// una dirección es peor que no guardar nada: queda en su lista y se la ofreceríamos después.
func TestLoQueNoEsUnNombreDeDireccionVaAlModelo(t *testing.T) {
	const from = "593999000102"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))

	for _, texto := range []string{
		"cuanto cuesta el de quince kilos",  // una pregunta, no una etiqueta
		"mejor mandame dos blancos ahorita", // sigue pidiendo: le toca al modelo
		"0999123456",                        // un teléfono
		"¿que?",                             // una duda
		"",                                  // vacío
	} {
		store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})
		if _, manejado := ag.ResponderGuardarUbicacion(from, texto); manejado {
			t.Errorf("%q se guardó como nombre de dirección; debía ir al modelo", texto)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.creadas) != 0 {
		t.Errorf("se crearon %d direcciones con textos que no son nombres", len(b.creadas))
	}
}

// "No, gracias" cierra la oferta sin guardar nada, y el cliente sigue con su pedido.
func TestSiNoQuiereGuardarNoSeGuardaNada(t *testing.T) {
	const from = "593999000103"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	if _, manejado := ag.ResponderGuardarUbicacion(from, BotonNoGuardar); !manejado {
		t.Fatal("el \"no\" debía resolverse en código")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.creadas) != 0 {
		t.Error("se guardó una dirección que el cliente rechazó")
	}
	if _, sigue := store.GetPendingGuardarUbicacion(from); sigue {
		t.Error("la oferta quedó viva: se le volvería a preguntar")
	}
}

// Sin oferta pendiente, este interceptor no puede tocar nada: un "Casa" suelto en medio de una
// conversación normal le toca al modelo.
func TestSinOfertaPendienteNoSeInterceptaNada(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	if _, manejado := ag.ResponderGuardarUbicacion("593999000104", "Casa"); manejado {
		t.Error("se interceptó un mensaje sin oferta pendiente de guardar")
	}
}

// No se repregunta por una ubicación que el cliente YA nombró: preguntarle en cada pedido
// cansa y hace que deje de leer los menús.
func TestNoSeRepreguntaPorUnaUbicacionYaGuardada(t *testing.T) {
	const from = "593999000105"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	// Ya tiene "Casa" en ese punto exacto.
	gr := b.cliente(t, `[{"id":1,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.898,"longitude":-79.002}]`)
	ag := agenteConCuenta(t, store, from, gr)

	if ag.OfrecerGuardarUbicacion(from, -2.898, -79.002) {
		t.Error("se le volvió a preguntar por una ubicación que ya tiene guardada como Casa")
	}
	if _, hay := store.GetPendingGuardarUbicacion(from); hay {
		t.Error("quedó una oferta pendiente que nunca se le mostró")
	}
}

// La dirección interna 'WhatsApp' NO cuenta como "ya guardada": el backend la reemplaza en cada
// pedido, así que estar ahí no significa que el cliente tenga ese sitio guardado. Si contara, a
// cualquier cliente con pedidos previos no se le ofrecería nunca nombrar su casa — la feature
// entera quedaría muerta sin que ningún test se enterara.
func TestLaDireccionInternaDelBotNoCuentaComoGuardada(t *testing.T) {
	soloLaInterna := []georoutes.SavedDirection{
		{ID: 1, Alias: "WhatsApp", Direccion: "Av. Solano 123", Latitude: -2.898, Longitude: -79.002},
	}
	if nombre, ya := ubicacionYaNombrada(soloLaInterna, -2.898, -79.002); ya {
		t.Errorf("la dirección interna del bot se tomó como una guardada por el cliente (%q): "+
			"nunca se le ofrecería ponerle nombre a su casa", nombre)
	}
}

// Y al revés: una que el cliente SÍ nombró corta la oferta.
func TestUnaUbicacionQueElClienteYaNombroCortaLaOferta(t *testing.T) {
	guardadas := []georoutes.SavedDirection{
		{ID: 1, Alias: "WhatsApp", Direccion: "Av. Solano 123", Latitude: -2.898, Longitude: -79.002},
		{ID: 2, Alias: "Casa", Direccion: "Av. Solano 123", Latitude: -2.898, Longitude: -79.002},
	}
	nombre, ya := ubicacionYaNombrada(guardadas, -2.898, -79.002)
	if !ya || nombre != "Casa" {
		t.Errorf("no se reconoció la ubicación ya nombrada: nombre=%q ya=%v", nombre, ya)
	}
	// Una ubicación distinta (~1 km) sigue mereciendo la pregunta.
	if _, ya := ubicacionYaNombrada(guardadas, -2.888, -79.002); ya {
		t.Error("una ubicación nueva se dio por guardada: el cliente nunca podría nombrarla")
	}
}

// Si el backend rechaza la dirección, el cliente NO se entera: vino a pedir gas, no a
// administrar direcciones. Nunca un error técnico por un extra que falló.
func TestSiElBackendRechazaLaDireccionElClienteNoVeUnError(t *testing.T) {
	const from = "593999000107"
	b := &backendDirecciones{rechazar: true}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	reply, manejado := ag.ResponderGuardarUbicacion(from, BotonGuardarCasa)
	if !manejado {
		t.Fatal("el turno debía resolverse igual")
	}
	for _, feo := range []string{"error", "Error", "inconveniente técnico", "falló"} {
		if strings.Contains(reply, feo) {
			t.Errorf("el cliente vio un problema técnico por un extra que falló: %q", reply)
		}
	}
}

// Guardar el nombre actualiza el último pedido SI es la misma ubicación, para que "repetir"
// diga "a Casa" desde ya y no a partir del pedido siguiente.
func TestGuardarElNombreLoAplicaAlUltimoPedidoDeEsaUbicacion(t *testing.T) {
	const from = "593999000108"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	store.SetLastOrder(from, conversation.LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2,
		Latitude: -2.898, Longitude: -79.002,
	})
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	if _, manejado := ag.ResponderGuardarUbicacion(from, BotonGuardarCasa); !manejado {
		t.Fatal("el botón no se resolvió")
	}
	last, _ := store.GetLastOrder(from)
	if last.Destino() != "Casa" {
		t.Errorf("el último pedido no quedó con el nombre nuevo: %q", last.Destino())
	}
}

// ...pero NO se lo aplica a un pedido que fue a OTRO sitio: diría "te lo envío a Casa"
// apuntando a una dirección donde ese pedido nunca estuvo.
func TestElNombreNoSeLeAplicaAUnPedidoDeOtraUbicacion(t *testing.T) {
	const from = "593999000109"
	b := &backendDirecciones{}
	store := conversation.NewMemStore()
	ag := agenteConCuenta(t, store, from, b.cliente(t, `[]`))
	// El último pedido fue a ~1 km de aquí.
	store.SetLastOrder(from, conversation.LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2,
		Latitude: -2.888, Longitude: -79.002,
	})
	store.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.898, Longitude: -79.002})

	if _, manejado := ag.ResponderGuardarUbicacion(from, BotonGuardarCasa); !manejado {
		t.Fatal("el botón no se resolvió")
	}
	last, _ := store.GetLastOrder(from)
	if last.Destino() == "Casa" {
		t.Error("un pedido que fue a otra ubicación quedó etiquetado como Casa: " +
			"al repetirlo se le ofrecería la dirección equivocada")
	}
}
