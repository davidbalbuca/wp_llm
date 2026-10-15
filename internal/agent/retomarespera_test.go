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

// Jessi (12/10): aceptó esperar a las 07:36; a las 07:43 el deploy reinició el bot y la goroutine
// que consultaba su búsqueda murió con el proceso. La espera seguía guardada, pero ya nadie le
// iba a mandar ni la siguiente ronda ni el aviso de repartidor.

type backendDeBusqueda struct {
	mu          sync.Mutex
	estado      string
	abiertas    int // llamadas a buscarConductor (abrir una búsqueda NUEVA)
	consultadas []string
}

func nuevoBackendDeBusqueda(t *testing.T, estado string) (*backendDeBusqueda, *httptest.Server) {
	t.Helper()
	b := &backendDeBusqueda{estado: estado}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "buscarConductor"):
			b.abiertas++
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idbusqueda":999,"estado":"BUSCANDO"}}`))
		case strings.Contains(r.URL.Path, "estadoBusquedaConductor"):
			cuerpo := make([]byte, r.ContentLength)
			r.Body.Read(cuerpo)
			b.consultadas = append(b.consultadas, string(cuerpo))
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idbusqueda":85,"estado":"` + b.estado + `"}}`))
		default:
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return b, srv
}

func TestAlArrancarSeRetomaLaBusquedaQueYaExistia(t *testing.T) {
	antes := intervaloBusqueda
	intervaloBusqueda = 20 * time.Millisecond
	defer func() { intervaloBusqueda = antes }()

	const from = "593967883780"
	b, srv := nuevoBackendDeBusqueda(t, georoutes.BusquedaCancelado)
	store := conversation.NewMemStore()
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetLocation(from, -2.88, -79.06)
	w := esperaDePrueba()
	w.IDBusqueda = 85
	w.RondaEspera = 1
	store.SetPendingWait(from, w)

	ag := agentDePrueba(nil, store)
	ag.cfg.UsarBusquedaBackend = true
	ag.gr = georoutes.NewClient(srv.URL)

	if n := ag.RetomarEsperas(); n != 1 {
		t.Fatalf("se retomaron %d esperas, había 1", n)
	}
	// La búsqueda vuelve CANCELADO: la goroutine limpia la espera y termina.
	limite := time.Now().Add(2 * time.Second)
	for {
		if _, sigue := store.GetPendingWait(from); !sigue {
			break
		}
		if time.Now().After(limite) {
			t.Fatal("nadie volvió a consultar la búsqueda: la espera quedó colgada")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.abiertas != 0 {
		t.Errorf("se abrió una búsqueda NUEVA al retomar (%d): el cliente tendría dos pedidos", b.abiertas)
	}
	if len(b.consultadas) == 0 || !strings.Contains(b.consultadas[0], `"idbusqueda":85`) {
		t.Errorf("no se consultó la búsqueda 85 que ya tenía: %v", b.consultadas)
	}
}

func TestSinEsperasNoSeRetomaNada(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	if n := ag.RetomarEsperas(); n != 0 {
		t.Errorf("se retomaron %d esperas sin haber ninguna", n)
	}
}
