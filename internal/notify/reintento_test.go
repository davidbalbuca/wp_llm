package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// telegramQueFalla corta la conexión en las primeras `fallos` llamadas a sendMessage (lo que el
// bot ve como error de red) y después contesta bien. Cuenta cada método.
type telegramQueFalla struct {
	mu      sync.Mutex
	fallos  int
	metodos map[string]int
}

func nuevoTelegramQueFalla(t *testing.T, fallos int) (*telegramQueFalla, *httptest.Server) {
	t.Helper()
	tg := &telegramQueFalla{fallos: fallos, metodos: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metodo := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		tg.mu.Lock()
		tg.metodos[metodo]++
		cortar := metodo == "sendMessage" && tg.fallos > 0
		if cortar {
			tg.fallos--
		}
		tg.mu.Unlock()
		if cortar {
			panic(http.ErrAbortHandler) // conexión cortada: error de red del lado del bot
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	t.Cleanup(srv.Close)
	return tg, srv
}

func (tg *telegramQueFalla) veces(metodo string) int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return tg.metodos[metodo]
}

func sinEsperas(t *testing.T) {
	antes := esperasReintento
	esperasReintento = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { esperasReintento = antes })
}

// El 19/10 Telegram tardó de más 3 veces y esos avisos (uno era un "Nuevo pedido") se perdieron:
// un error de red ahora se reintenta.
func TestUnCorteDeRedSeReintentaYElAvisoSale(t *testing.T) {
	sinEsperas(t)
	tg, srv := nuevoTelegramQueFalla(t, 2)
	n := &Notifier{chatID: "-1", cliente: srv.Client(), base: srv.URL}

	n.enviar(5, "📱 Nuevo pedido", false)

	if got := tg.veces("sendMessage"); got != 3 {
		t.Fatalf("sendMessage se llamó %d veces; con 2 cortes tenía que salir al tercer intento", got)
	}
}

// Los reintentos tienen tope: si Telegram sigue caído no se insiste para siempre.
func TestSiTelegramSigueCaidoSeRindeTrasLosReintentos(t *testing.T) {
	sinEsperas(t)
	tg, srv := nuevoTelegramQueFalla(t, 10)
	n := &Notifier{chatID: "-1", cliente: srv.Client(), base: srv.URL}

	n.enviar(5, "aviso", false)

	if got, want := tg.veces("sendMessage"), 1+len(esperasReintento); got != want {
		t.Fatalf("sendMessage se llamó %d veces, se esperaban %d (1 + reintentos)", got, want)
	}
}

// El hilo de pedidos ya existía como "📱 Pedidos de la App": se renombra una sola vez por arranque,
// no en cada aviso.
func TestElHiloDePedidosSeRenombraUnaVez(t *testing.T) {
	tg, srv := nuevoTelegramQueFalla(t, 0)
	store := conversation.NewMemStore()
	store.SetTelegramThread(claveHiloPedidosApp, 1417)
	n := &Notifier{chatID: "-1", store: store, cliente: srv.Client(), base: srv.URL}

	for i := 0; i < 3; i++ {
		if id := n.hiloPedidosAppID(); id != 1417 {
			t.Fatalf("hilo %d, se esperaba el guardado (1417)", id)
		}
	}
	if got := tg.veces("editForumTopic"); got != 1 {
		t.Fatalf("editForumTopic se llamó %d veces, tenía que ser 1", got)
	}
	if got := tg.veces("createForumTopic"); got != 0 {
		t.Fatalf("se creó un hilo nuevo (%d): tenía que usar el que ya existe", got)
	}
}
