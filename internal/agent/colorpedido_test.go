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

// Dueño, 18/10: "un cliente pide AMARILLO, le llega a un repartidor amarillo que no lo acepta, no
// hay otro amarillo cerca pero sí uno con BLANCO a 1 km: antes de ampliar a toda la zona, que le
// ofrezca el blanco". El backend avisa por /internal/order-color-offer; aquí se pregunta y se
// resuelve la respuesta.

type backendColorPedido struct {
	mu      sync.Mutex
	estado  string // lo que responde pedidoCambioColor ("" = error HTTP)
	cambios []string
}

func nuevoBackendColorPedido(t *testing.T, estado string) (*backendColorPedido, *httptest.Server) {
	t.Helper()
	b := &backendColorPedido{estado: estado}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":1,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "pedidoCambioColor"):
			b.cambios = append(b.cambios, string(cuerpo))
			if b.estado == "" {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"codigo":-1,"mensaje":"El pedido no existe o no es tuyo"}`))
				return
			}
			w.Write([]byte(`{"codigo":1,"mensaje":"ok","resultado":{"idpedido":501,"estado":"` + b.estado +
				`","color":"BLANCO"}}`))
		default:
			w.Write([]byte(`{"codigo":1,"mensaje":"ok","resultado":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return b, srv
}

const clienteAmarillo = "593984111222"

var blancoParaEl501 = OfertaColorPedido{IDPedido: 501, ColorActual: "AMARILLO", ColorAlterno: "BLANCO",
	IDColorAlterno: 1, Minutos: 4}

func clienteConPedidoAmarillo(t *testing.T, srv *httptest.Server) (*Agent, conversation.Store, *[]string) {
	t.Helper()
	store := conversation.NewMemStore()
	store.SetAccount(clienteAmarillo, conversation.Account{Username: "u", Password: "p"})
	store.SetActivePedido(clienteAmarillo, 501)
	store.SetLastOrder(clienteAmarillo, conversation.LastOrder{Producto: "GAS 15KG", Color: "AMARILLO", Cantidad: 1})
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(srv.URL)
	var menus []string
	ag.enviarMenu = func(_, cuerpo string, opciones []string) error {
		menus = append(menus, cuerpo+" ["+strings.Join(opciones, " / ")+"]")
		return nil
	}
	t.Cleanup(func() { QuitarPorConfirmar(ag.cfg, clienteAmarillo) })
	return ag, store, &menus
}

func TestSeLeOfreceElBlancoSinNombrarAlRepartidor(t *testing.T) {
	_, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, menus := clienteConPedidoAmarillo(t, srv)

	if !ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501) {
		t.Fatal("no se le ofreció el blanco")
	}
	want := "No encuentro repartidor con cilindro AMARILLO cerca, pero tengo uno con BLANCO a unos 4 min. " +
		"¿Te sirve el BLANCO? (mismo precio) [Sí, BLANCO / No, espero AMARILLO]"
	if len(*menus) != 1 || (*menus)[0] != want {
		t.Fatalf("menú = %v\nquiero %q", *menus, want)
	}
	sw, hay := store.GetPendingColorSwap(clienteAmarillo)
	if !hay || sw.IDPedido != 501 || sw.IDColorAlterno != 1 || sw.ColorOriginal != "AMARILLO" {
		t.Fatalf("la oferta no quedó pendiente con el pedido: %+v", sw)
	}
	// Con la oferta sin contestar, el menú de esperar y el otro color alterno no se le encima.
	store.SetPendingWait(clienteAmarillo, conversation.PendingWait{IDProducto: 1, IDColor: 2, ColorNombre: "AMARILLO"})
	if _, manejado := ag.ResponderMenuEspera(clienteAmarillo, "no"); manejado {
		t.Error("el \"no\" lo tomó el menú de esperar, no la oferta de color")
	}
}

func TestSinMinutosNoSeInventaUnTiempo(t *testing.T) {
	o := blancoParaEl501
	o.Minutos = 0
	if txt := TextoColorDelPedido(o); strings.Contains(txt, "min") {
		t.Errorf("sin minutos del backend no se promete un tiempo: %q", txt)
	}
}

func TestSiDiceQueSiSeCambiaElPedidoEnElBackend(t *testing.T) {
	b, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, _ := clienteConPedidoAmarillo(t, srv)
	ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)

	reply, manejado := ag.ResponderColorDelPedido(clienteAmarillo, "Sí, BLANCO")
	if !manejado || reply != "Listo, cambié tu pedido a BLANCO ✅. Estoy confirmando con el repartidor 🚚" {
		t.Fatalf("respuesta = %q (%v)", reply, manejado)
	}
	b.mu.Lock()
	if len(b.cambios) != 1 || !strings.Contains(b.cambios[0], `"idpedido":501`) || !strings.Contains(b.cambios[0], `"idcolor":1`) {
		t.Errorf("no se le pidió al backend cambiar el 501 a blanco: %v", b.cambios)
	}
	b.mu.Unlock()
	if _, hay := store.GetPendingColorSwap(clienteAmarillo); hay {
		t.Error("la oferta quedó pendiente después de contestar")
	}
	if !estaPorConfirmar(ag.cfg, clienteAmarillo) {
		t.Error("el nuevo repartidor todavía no aceptó: tiene que quedar por confirmar")
	}
	if lo, _ := store.GetLastOrder(clienteAmarillo); lo.Color != "BLANCO" {
		t.Errorf("\"repetir lo mismo\" seguiría diciendo %s", lo.Color)
	}
}

func TestSiSuRepartidorYaAceptoSeLeDiceQueVaEnCamino(t *testing.T) {
	_, srv := nuevoBackendColorPedido(t, "ya_acepto")
	ag, store, _ := clienteConPedidoAmarillo(t, srv)
	ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)
	reply, _ := ag.ResponderColorDelPedido(clienteAmarillo, "si")
	if reply != "¡Buenas noticias! Tu repartidor de AMARILLO ya aceptó y va en camino 🚚" {
		t.Fatalf("respuesta = %q", reply)
	}
	if lo, _ := store.GetLastOrder(clienteAmarillo); lo.Color != "AMARILLO" {
		t.Errorf("no se cambió nada: el último pedido sigue AMARILLO, no %s", lo.Color)
	}
}

func TestSiYaNoSePuedeSigueBuscandoElSuyo(t *testing.T) {
	for _, estado := range []string{"vencido", "sin_repartidor", ""} {
		_, srv := nuevoBackendColorPedido(t, estado)
		ag, _, _ := clienteConPedidoAmarillo(t, srv)
		ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)
		reply, manejado := ag.ResponderColorDelPedido(clienteAmarillo, "Sí, BLANCO")
		if !manejado || reply != "Ese repartidor ya no está disponible; sigo buscando tu AMARILLO 🙏" {
			t.Errorf("backend %q: respuesta = %q", estado, reply)
		}
		if estaPorConfirmar(ag.cfg, clienteAmarillo) {
			t.Errorf("backend %q: no se cambió nada, no tiene por qué quedar por confirmar", estado)
		}
	}
}

func TestSiDiceQueNoNoSeTocaNada(t *testing.T) {
	b, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, _ := clienteConPedidoAmarillo(t, srv)
	for _, texto := range []string{"No, espero AMARILLO", "no"} {
		ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)
		reply, manejado := ag.ResponderColorDelPedido(clienteAmarillo, texto)
		if !manejado || reply != "Perfecto, sigo buscando tu AMARILLO 🙏" {
			t.Errorf("%q: respuesta = %q", texto, reply)
		}
		if _, hay := store.GetPendingColorSwap(clienteAmarillo); hay {
			t.Errorf("%q: la oferta quedó pendiente", texto)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cambios) != 0 {
		t.Errorf("con el no se llamó al backend: %v", b.cambios)
	}
}

// "Cancelar" con la oferta en pie es cancelar el pedido: no es un "no, espero el mío".
func TestCancelarNoLoTomaLaOferta(t *testing.T) {
	_, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, _ := clienteConPedidoAmarillo(t, srv)
	ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)
	for _, texto := range []string{"cancelar", "ya no quiero", "¿y cuánto se demora el blanco?"} {
		if _, manejado := ag.ResponderColorDelPedido(clienteAmarillo, texto); manejado {
			t.Errorf("%q lo tomó la oferta de color", texto)
		}
		if _, manejado := ag.ResponderCambioColor(clienteAmarillo, texto); manejado {
			t.Errorf("%q lo tomó ResponderCambioColor como si fuera la oferta de una búsqueda", texto)
		}
	}
	if _, hay := store.GetPendingColorSwap(clienteAmarillo); !hay {
		t.Error("la oferta se perdió por un mensaje que no la contestaba")
	}
}

// El "sí" a la oferta de un pedido NO registra otro pedido (eso hace la oferta de la búsqueda).
func TestResponderCambioColorDelegaEnLaDelPedido(t *testing.T) {
	b, srv := nuevoBackendColorPedido(t, "ok")
	ag, _, _ := clienteConPedidoAmarillo(t, srv)
	ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501)
	reply, manejado := ag.ResponderCambioColor(clienteAmarillo, "Sí, BLANCO")
	if !manejado || !strings.HasPrefix(reply, "Listo, cambié tu pedido a BLANCO") {
		t.Fatalf("respuesta = %q", reply)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cambios) != 1 {
		t.Errorf("no pasó por pedidoCambioColor: %v", b.cambios)
	}
}

func TestNoSeLeOfreceSiElPedidoYaNoEsElSuyoOTieneOtraOferta(t *testing.T) {
	_, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, menus := clienteConPedidoAmarillo(t, srv)
	store.SetActivePedido(clienteAmarillo, 777) // un operador le hizo otro pedido
	if ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501) {
		t.Error("se le ofreció un cambio para un pedido ya reemplazado")
	}
	store.SetActivePedido(clienteAmarillo, 501)
	store.SetPendingColorSwap(clienteAmarillo, conversation.PendingColorSwap{ColorOriginal: "AMARILLO",
		ColorAlterno: "AZUL", DuranteEspera: true})
	if ag.OfrecerColorDelPedido(clienteAmarillo, blancoParaEl501) {
		t.Error("se le encimó la oferta sobre otra sin contestar")
	}
	if len(*menus) != 0 {
		t.Errorf("salieron menús: %v", *menus)
	}
	if _, manejado := ag.ResponderColorDelPedido(clienteAmarillo, "si"); manejado {
		t.Error("ResponderColorDelPedido tomó el sí de la oferta de la búsqueda")
	}
}

func TestLaOfertaViejaYaNoSeIntercepta(t *testing.T) {
	_, srv := nuevoBackendColorPedido(t, "ok")
	ag, store, _ := clienteConPedidoAmarillo(t, srv)
	store.SetPendingColorSwap(clienteAmarillo, conversation.PendingColorSwap{ColorOriginal: "AMARILLO",
		ColorAlterno: "BLANCO", IDColorAlterno: 1, IDPedido: 501,
		OfrecidoAt: time.Now().Add(-vigenciaColorPedido - time.Minute).Unix()})
	if _, manejado := ag.ResponderColorDelPedido(clienteAmarillo, "si"); manejado {
		t.Error("se interceptó un sí de hace más de media hora")
	}
	if _, hay := store.GetPendingColorSwap(clienteAmarillo); hay {
		t.Error("la oferta vieja no se limpió")
	}
}

func TestAlTerminarElPedidoSeOlvidaLaOferta(t *testing.T) {
	store := conversation.NewMemStore()
	store.SetPendingColorSwap(clienteAmarillo, conversation.PendingColorSwap{IDPedido: 501})
	OlvidarColorDelPedido(store, clienteAmarillo, 502)
	if _, hay := store.GetPendingColorSwap(clienteAmarillo); !hay {
		t.Error("se borró la oferta de OTRO pedido")
	}
	OlvidarColorDelPedido(store, clienteAmarillo, 501)
	if _, hay := store.GetPendingColorSwap(clienteAmarillo); hay {
		t.Error("la oferta del pedido terminado quedó viva")
	}
}

func TestBotonesCaben(t *testing.T) {
	for _, c := range []string{"AMARILLO", "BLANCO", "NARANJA", "AZUL", "VERDE OSCURO METALIZADO"} {
		for _, b := range []string{botonSiColorPedido(c), botonNoColorPedido(c)} {
			if n := len([]rune(b)); n > 20 {
				t.Errorf("%q tiene %d caracteres: WhatsApp lo corta a 20", b, n)
			}
		}
	}
}
