package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Oferta de color equivalente DURANTE la espera (11/10). Jaime pidio amarillo sin nadie cerca; un
// minuto despues se conecto un conductor con blanco (equivalente) y el bot siguio esperando sin
// ofrecerle el cambio, porque la oferta solo se hacia al registrar el pedido.

type backendDeAlternativas struct {
	mu           sync.Mutex
	alternativas string // JSON de la lista; "[]" = ninguna
	consultas    int
	cerradas     int
	servidor     *httptest.Server
}

func nuevoBackendDeAlternativas(t *testing.T, alternativas string) *backendDeAlternativas {
	t.Helper()
	b := &backendDeAlternativas{alternativas: alternativas}
	b.servidor = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "checkColorAlternatives"):
			b.consultas++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"alternativas":` + b.alternativas + `}}`))
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "cancelarBusquedaConductor"):
			b.cerradas++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"estado":"CANCELADO"}}`))
		default:
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		}
	}))
	t.Cleanup(b.servidor.Close)
	return b
}

func (b *backendDeAlternativas) contar() (consultas, cerradas int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consultas, b.cerradas
}

const blancoConConductor = `[{"idcolor":1,"color":"BLANCO","conductores":2}]`

// Un cliente esperando 1 AMARILLO con la busqueda 84 abierta, como Jaime.
func agenteEsperandoAmarillo(t *testing.T, b *backendDeAlternativas, from string) (*Agent, conversation.Store, *[]string) {
	t.Helper()
	store := conversation.NewMemStore()
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 2, IDProducto: 1, IDColor: 2, Cantidad: 1, IDTipoPago: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "AMARILLO", IDBusqueda: 84,
	})
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(b.servidor.URL)
	var menus []string
	ag.enviarMenu = func(_, cuerpo string, opciones []string) error {
		menus = append(menus, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
		return nil
	}
	return ag, store, &menus
}

func TestEsperaOfreceElColorEquivalenteCuandoAparece(t *testing.T) {
	const from = "593998015677"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, menus := agenteEsperandoAmarillo(t, b, from)

	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, time.Now())

	if len(*menus) != 1 {
		t.Fatalf("se esperaba UNA oferta de cambio, salieron %d", len(*menus))
	}
	if !strings.Contains((*menus)[0], "BLANCO") || !strings.Contains((*menus)[0], "AMARILLO") {
		t.Errorf("la oferta no dice qué pidió y qué se le ofrece: %q", (*menus)[0])
	}
	sw, hay := store.GetPendingColorSwap(from)
	if !hay || !sw.DuranteEspera || sw.ColorAlterno != "BLANCO" {
		t.Errorf("la oferta no quedó pendiente como 'durante la espera': %+v", sw)
	}
	if w, _ := store.GetPendingWait(from); !w.ColorAlternoOfrecido {
		t.Error("la espera no quedó marcada con la oferta hecha: se le repetiría")
	}
}

func TestEsperaOfreceUnaSolaVez(t *testing.T) {
	const from = "593998015678"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, menus := agenteEsperandoAmarillo(t, b, from)

	ahora := time.Now()
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, ahora)
	store.ClearPendingColorSwap(from) // contestó que no
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, ahora.Add(5*time.Minute))

	if len(*menus) != 1 {
		t.Errorf("se le insistió con el cambio de color: %d ofertas", len(*menus))
	}
}

func TestEsperaSinAlternativaNoMolesta(t *testing.T) {
	const from = "593998015679"
	b := nuevoBackendDeAlternativas(t, `[]`)
	ag, store, menus := agenteEsperandoAmarillo(t, b, from)

	ahora := time.Now()
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, ahora)
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, ahora.Add(5*time.Second))
	if len(*menus) != 0 {
		t.Errorf("sin alternativas no debía salir nada: %v", *menus)
	}
	if consultas, _ := b.contar(); consultas != 1 {
		t.Errorf("a los 5 s ya volvió a preguntar (%d consultas): debía esperar %s", consultas, chequeoColorAlterno)
	}
	// Pasado el intervalo sí vuelve a mirar: el conductor puede conectarse en cualquier momento.
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, ahora.Add(chequeoColorAlterno+time.Second))
	if consultas, _ := b.contar(); consultas != 2 {
		t.Errorf("pasado el intervalo no volvió a consultar (%d consultas)", consultas)
	}
	if _, hay := store.GetPendingColorSwap(from); hay {
		t.Error("quedó una oferta pendiente sin haberse ofrecido nada")
	}
}

func TestEsperaNoEnciamaElMenuDeEsperar(t *testing.T) {
	const from = "593998015680"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, menus := agenteEsperandoAmarillo(t, b, from)
	w, _ := store.GetPendingWait(from)
	w.EsperandoRespuesta = true // tiene sin contestar el "¿quieres seguir esperando?"
	store.SetPendingWait(from, w)

	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, time.Now())
	if len(*menus) != 0 {
		t.Errorf("se le mandó la oferta encima de otro menú sin contestar: %v", *menus)
	}
}

func TestEsperaPedidoDeDosColoresNoSeOfrece(t *testing.T) {
	const from = "593998015681"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, menus := agenteEsperandoAmarillo(t, b, from)
	w, _ := store.GetPendingWait(from)
	w.Items = []conversation.PendingWaitItem{{IDColor: 1, Cantidad: 1}, {IDColor: 2, Cantidad: 1}}
	store.SetPendingWait(from, w)

	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, time.Now())
	if len(*menus) != 0 {
		t.Errorf("a un pedido de dos colores se le ofreció cambiar uno: %v", *menus)
	}
}

// "No, espero el mío": sigue esperando su color, la búsqueda NO se cierra.
func TestEsperaNoAlCambioSigueEsperando(t *testing.T) {
	const from = "593998015682"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, _ := agenteEsperandoAmarillo(t, b, from)
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, time.Now())

	reply, manejado := ag.ResponderCambioColor(from, BotonSeguirEsperando)
	if !manejado {
		t.Fatal("\"No, espero el mío\" no se resolvió en código")
	}
	if _, sigue := store.GetPendingWait(from); !sigue {
		t.Error("se cerró la espera: el cliente dijo que seguía esperando su amarillo")
	}
	if _, cerradas := b.contar(); cerradas != 0 {
		t.Error("se canceló la búsqueda del backend aunque el cliente sigue esperando")
	}
	if !strings.Contains(strings.ToLower(reply), "amarillo") {
		t.Errorf("la respuesta no le confirma que seguimos buscando su amarillo: %q", reply)
	}
}

// "Sí, blanco": se cierra la búsqueda del amarillo ANTES de registrar el blanco.
func TestEsperaSiAlCambioCierraLaBusquedaOriginal(t *testing.T) {
	const from = "593998015683"
	b := nuevoBackendDeAlternativas(t, blancoConConductor)
	ag, store, _ := agenteEsperandoAmarillo(t, b, from)
	ag.revisarColorAlternoEnEspera(from, -2.857, -78.912, time.Now())

	if _, manejado := ag.ResponderCambioColor(from, "Sí, blanco"); !manejado {
		t.Fatal("\"Sí, blanco\" no se resolvió en código")
	}
	if _, cerradas := b.contar(); cerradas != 1 {
		t.Errorf("la búsqueda del amarillo no se cerró (%d): quedarían dos pedidos buscando conductor", cerradas)
	}
	if _, sigue := store.GetPendingColorSwap(from); sigue {
		t.Error("la oferta sigue pendiente tras aceptar")
	}
}
