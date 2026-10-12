package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Dueño, 12/10: "antes de asignar a William [el camión de Ubi, que lleva blanco] debe preguntar
// que hay un blanco, si desea realizar el cambio". Jessi pidió amarillo y nadie lo tomó.

type backendRespaldo struct {
	mu         sync.Mutex
	estado     string // lo que devuelve estadoBusquedaConductor
	cambios    []string
	decisiones []string
}

func nuevoBackendRespaldo(t *testing.T) (*backendRespaldo, *httptest.Server) {
	t.Helper()
	b := &backendRespaldo{estado: "SIN_CONDUCTOR"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "respaldoCambioColor"):
			b.cambios = append(b.cambios, string(cuerpo))
			b.estado = "ASIGNADO"
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idbusqueda":85,"estado":"ASIGNADO"}}`))
		case strings.Contains(r.URL.Path, "decisionBusquedaConductor"):
			b.decisiones = append(b.decisiones, string(cuerpo))
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idbusqueda":85,"estado":"ESPERANDO"}}`))
		default:
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return b, srv
}

var blancoDelCamion = &georoutes.AlternativaRespaldo{IDColor: 1, Color: "BLANCO", Minutos: 16, Conductor: "William"}

func jessiEsperandoAmarillo(t *testing.T, srv *httptest.Server) (*Agent, conversation.Store, *[]string) {
	t.Helper()
	const from = "593967883780"
	store := conversation.NewMemStore()
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetPendingWait(from, conversation.PendingWait{IDCategoria: 2, IDProducto: 1, IDColor: 2, Cantidad: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "AMARILLO", IDBusqueda: 85})
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(srv.URL)
	var menus []string
	ag.enviarMenu = func(_, cuerpo string, opciones []string) error {
		menus = append(menus, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
		return nil
	}
	return ag, store, &menus
}

func TestSinConductorElCamionOfreceElColorEquivalenteAntesDeEsperar(t *testing.T) {
	_, srv := nuevoBackendRespaldo(t)
	ag, store, menus := jessiEsperandoAmarillo(t, srv)
	const from = "593967883780"

	if !ag.ofrecerColorDelRespaldo(from, 85, blancoDelCamion) {
		t.Fatal("no se ofreció el color del camión")
	}
	if len(*menus) != 1 || !strings.Contains((*menus)[0], "AMARILLO") || !strings.Contains((*menus)[0], "BLANCO") ||
		!strings.Contains((*menus)[0], "16 min") || !strings.Contains((*menus)[0], "Sí, blanco") {
		t.Fatalf("la oferta no dice qué pidió, qué le llevamos y cuándo: %v", *menus)
	}
	// Con la oferta sin contestar, no se le manda encima el "¿deseas esperar?".
	if !ag.ofrecerColorDelRespaldo(from, 85, blancoDelCamion) || len(*menus) != 1 {
		t.Errorf("se le repitió la oferta o se le dejaría pasar el menú de esperar: %v", *menus)
	}
	if w, _ := store.GetPendingWait(from); !w.RespaldoOfrecido {
		t.Error("la espera no quedó marcada: se le volvería a ofrecer")
	}
}

func TestSiAceptaSeLeAsignaAlCamionEnBlanco(t *testing.T) {
	b, srv := nuevoBackendRespaldo(t)
	ag, store, _ := jessiEsperandoAmarillo(t, srv)
	const from = "593967883780"
	ag.ofrecerColorDelRespaldo(from, 85, blancoDelCamion)

	reply, manejado := ag.ResponderCambioColor(from, "Sí, blanco")
	if !manejado || !strings.Contains(strings.ToLower(reply), "camión") {
		t.Fatalf("no se resolvió el sí: %q", reply)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cambios) != 1 || !strings.Contains(b.cambios[0], `"idcolor":1`) || !strings.Contains(b.cambios[0], `"idbusqueda":85`) {
		t.Errorf("no se le pidió al backend el cambio a blanco de la búsqueda 85: %v", b.cambios)
	}
	if w, _ := store.GetPendingWait(from); w.ColorNombre != "BLANCO" || w.IDColor != 1 {
		t.Errorf("la espera sigue en amarillo: el aviso del repartidor diría el color equivocado: %+v", w)
	}
}

func TestSiNoQuiereSigueEsperandoElSuyo(t *testing.T) {
	b, srv := nuevoBackendRespaldo(t)
	ag, store, _ := jessiEsperandoAmarillo(t, srv)
	const from = "593967883780"
	ag.ofrecerColorDelRespaldo(from, 85, blancoDelCamion)

	reply, manejado := ag.ResponderCambioColor(from, BotonSeguirEsperando)
	if !manejado || !strings.Contains(strings.ToLower(reply), "amarillo") {
		t.Fatalf("no le confirma que seguimos con su amarillo: %q", reply)
	}
	b.mu.Lock()
	if len(b.cambios) != 0 || len(b.decisiones) != 1 || !strings.Contains(b.decisiones[0], `"esperar":true`) {
		t.Errorf("con el no tiene que aceptar la espera y no cambiar el color: cambios=%v decisiones=%v", b.cambios, b.decisiones)
	}
	b.mu.Unlock()
	w, _ := store.GetPendingWait(from)
	if w.RondaEspera != 1 || time.Since(time.Unix(w.UltimaRondaAt, 0)) > time.Minute {
		t.Errorf("no contó como la primera ronda: le llegaría enseguida el ¿deseas esperar?: %+v", w)
	}
}

func TestSinAlternativaVaElMenuDeEsperarDeSiempre(t *testing.T) {
	_, srv := nuevoBackendRespaldo(t)
	ag, _, menus := jessiEsperandoAmarillo(t, srv)
	if ag.ofrecerColorDelRespaldo("593967883780", 85, nil) || len(*menus) != 0 {
		t.Errorf("sin alternativa del camión no debía ofrecerse nada: %v", *menus)
	}
}

// El "no" a la oferta del camión NO puede caer en el menú de esperar (que va antes en el webhook
// y lo tomaría como "cancelar").
func TestElNoALaOfertaNoCancelaLaEspera(t *testing.T) {
	_, srv := nuevoBackendRespaldo(t)
	ag, store, _ := jessiEsperandoAmarillo(t, srv)
	const from = "593967883780"
	ag.ofrecerColorDelRespaldo(from, 85, blancoDelCamion)
	for _, texto := range []string{"no", "sí", BotonSeguirEsperando} {
		if _, manejado := ag.ResponderMenuEspera(from, texto); manejado {
			t.Errorf("%q lo tomó el menú de esperar, no la oferta del camión", texto)
		}
	}
	if _, sigue := store.GetPendingWait(from); !sigue {
		t.Error("se canceló la espera")
	}
}
