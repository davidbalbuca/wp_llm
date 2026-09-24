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

// SI EL CLIENTE SE BAJA, EL BACKEND TIENE QUE ENTERARSE.
//
// Con BOT_USAR_BUSQUEDA la búsqueda de repartidor la lleva el backend, y el bot solo guarda el
// id para poder cerrarla. El problema es que hay CINCO caminos por los que el bot borra su
// espera, y solo dos le avisaban:
//
//	cancelar               ✅ decidirEsperaBackend(false)
//	reprogramar            ✅ cerrarBusquedaBackend
//	rechazar color alterno ❌
//	cambio de dirección    ❌  ← el peor
//	cierre de conversación ❌
//
// El del cambio de dirección es el más caro: el bot cancela el pedido y lo vuelve a registrar
// en la ubicación nueva, pero la búsqueda ANTERIOR sigue viva. El backend puede asignarle un
// repartidor a un pedido que ya no existe y mandarlo a la dirección vieja.
//
// La regla ahora es una sola: BORRAR LA ESPERA DEL BOT ES CERRAR LA BÚSQUEDA DEL BACKEND. Un
// único punto de salida (cerrarEsperaYBusqueda), para que un camino nuevo no vuelva a olvidarlo.

// espiaDeBusqueda cuenta las llamadas a cancelarBusquedaConductor.
type espiaDeBusqueda struct {
	mu        sync.Mutex
	cerradas  []int
	servidor  *httptest.Server
	respuesta string
}

func nuevoEspiaDeBusqueda(t *testing.T) *espiaDeBusqueda {
	t.Helper()
	esp := &espiaDeBusqueda{}
	esp.servidor = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			// El envoltorio {codigo,mensaje,resultado} es el del backend real: sin él el Login
			// falla y el test pasaría por el camino de error, sin llegar a cerrar nada.
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "cancelarBusquedaConductor"):
			esp.mu.Lock()
			esp.cerradas = append(esp.cerradas, 1)
			esp.mu.Unlock()
			w.Write([]byte(`{"estado":"CANCELADO"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(esp.servidor.Close)
	return esp
}

func (e *espiaDeBusqueda) cuantasCerradas() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cerradas)
}

// agenteConBusquedaAbierta arma un agente con una espera viva que tiene búsqueda abierta en el backend.
func agenteConBusquedaAbierta(t *testing.T, esp *espiaDeBusqueda, from string, idbusqueda int) (*Agent, conversation.Store) {
	t.Helper()
	store := conversation.NewMemStore()
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 1, IDBusqueda: idbusqueda,
	})
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	return ag, store
}

// El punto único: borrar la espera cierra la búsqueda del backend.
func TestCerrarLaEsperaCierraLaBusquedaDelBackend(t *testing.T) {
	const from = "593999200001"
	esp := nuevoEspiaDeBusqueda(t)
	ag, store := agenteConBusquedaAbierta(t, esp, from, 77)

	ag.cerrarEsperaYBusqueda(from, "prueba")

	if esp.cuantasCerradas() != 1 {
		t.Errorf("no se cerró la búsqueda en el backend (%d llamadas): el backend seguiría "+
			"buscando repartidor para un pedido que el cliente ya no espera", esp.cuantasCerradas())
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la espera del bot no se borró")
	}
}

// Sin búsqueda en el backend (camino viejo, BOT_USAR_BUSQUEDA=false) no se llama a nadie: no
// hay nada que cerrar y una llamada de más sería un error en los logs de producción.
func TestSinBusquedaAbiertaNoSeLlamaAlBackend(t *testing.T) {
	const from = "593999200002"
	esp := nuevoEspiaDeBusqueda(t)
	ag, store := agenteConBusquedaAbierta(t, esp, from, 0) // idbusqueda = 0: sin búsqueda

	ag.cerrarEsperaYBusqueda(from, "prueba")

	if esp.cuantasCerradas() != 0 {
		t.Errorf("se llamó al backend sin búsqueda abierta (%d veces)", esp.cuantasCerradas())
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la espera del bot no se borró")
	}
}

// Y que los TRES caminos que no avisaban ahora usen el punto único. Se verifica sobre el
// código, sin comentarios: el nombre de la función aparece en la explicación de cada uno y un
// Contains sobre el texto crudo pasaría aunque la llamada se hubiera borrado.
func TestLosCaminosQueBorranLaEsperaCierranLaBusqueda(t *testing.T) {
	archivos := map[string]string{
		"coloralterno.go":    "rechazar el color alterno",
		"cambiodireccion.go": "cambiar la dirección con pedido en ruta",
		"cierreciclo.go":     "cerrar el ciclo de la conversación",
	}
	for archivo, que := range archivos {
		src := codigoSinComentariosDelAgente(t, archivo)
		if !strings.Contains(src, "cerrarEsperaYBusqueda(") {
			t.Errorf("%s (%s) borra la espera sin cerrar la búsqueda: el backend seguiría "+
				"buscando repartidor", archivo, que)
		}
		// Y que no quede el ClearPendingWait suelto, que es la forma de saltarse el cierre.
		if strings.Contains(src, "store.ClearPendingWait(") {
			t.Errorf("%s todavía borra la espera por su cuenta: usar cerrarEsperaYBusqueda", archivo)
		}
	}
}
