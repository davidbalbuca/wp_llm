package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Los pedidos del FLUJO MANUAL (conductor sin app) no los cierra nadie: el bot le pregunta al
// cliente si ya le entregaron. Estos tests fijan las cuatro decisiones de ese flujo:
//   1) un pedido manual PROGRAMA la pregunta; uno normal NO (lo cierra la app del conductor).
//   2) al vencer el plazo, la pregunta sale con los botones Sí/No y queda "preguntado".
//   3) "Sí" cierra el chequeo (y llama al backend).
//   4) "No" NO cierra el pedido y deja el chequeo listo para un "ya llegó" posterior.

// agenteConMenuCapturado devuelve un Agent cuyo menú se guarda en memoria en vez de mandarse por
// WhatsApp, para poder leer lo que el cliente vería.
func agenteConMenuCapturado(t *testing.T) (*Agent, conversation.Store, func() (string, []string, bool)) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	var mu sync.Mutex
	var ultimoCuerpo string
	var ultimasOpciones []string
	var hubo bool
	ag.enviarMenu = func(from, cuerpo string, opciones []string) error {
		mu.Lock()
		defer mu.Unlock()
		ultimoCuerpo, ultimasOpciones, hubo = cuerpo, opciones, true
		return nil
	}
	leer := func() (string, []string, bool) {
		mu.Lock()
		defer mu.Unlock()
		return ultimoCuerpo, ultimasOpciones, hubo
	}
	return ag, store, leer
}

func TestPedidoNormalNoProgramaChequeoDeEntrega(t *testing.T) {
	const from = "593999600001"
	ag, store, _ := agenteConMenuCapturado(t)

	ag.programarChequeoEntrega(from, 100, "Juan Perez", "normal")

	if _, hay := store.GetPendingDeliveryCheck(from); hay {
		t.Error("un pedido en modo normal NO debe programar chequeo de entrega (lo cierra la app del conductor)")
	}
	if ag.chequeosArrancados.Load() != 0 {
		t.Error("no debió arrancar ninguna goroutine de chequeo para un pedido normal")
	}
}

func TestPedidoManualProgramaYPreguntaLaEntrega(t *testing.T) {
	const from = "593999600002"
	ag, store, leerMenu := agenteConMenuCapturado(t)
	// Plazo diminuto: la goroutine debe preguntar casi al instante.
	ag.cfg.ChequeoEntrega = 20 * time.Millisecond

	ag.programarChequeoEntrega(from, 240, "Angel Vizuete", "sin_tracking")

	if _, hay := store.GetPendingDeliveryCheck(from); !hay {
		t.Fatal("un pedido manual debe dejar un chequeo de entrega pendiente")
	}

	// Esperar a que la goroutine dispare la pregunta.
	esperarHasta(t, time.Second, func() bool {
		_, _, hubo := leerMenu()
		return hubo
	})

	cuerpo, opciones, _ := leerMenu()
	if !strings.Contains(cuerpo, "240") {
		t.Errorf("la pregunta debe nombrar el pedido #240: %q", cuerpo)
	}
	if len(opciones) != 2 || opciones[0] != BotonEntregaSi || opciones[1] != BotonEntregaNo {
		t.Errorf("la pregunta debe ofrecer [Sí / No]: %v", opciones)
	}
	chk, _ := store.GetPendingDeliveryCheck(from)
	if !chk.Preguntado {
		t.Error("tras preguntar, el chequeo debe quedar marcado como 'preguntado'")
	}
}

func TestClienteConfirmaEntregaCierraElChequeo(t *testing.T) {
	const from = "593999600003"
	ag, store, _ := agenteConMenuCapturado(t)
	// Chequeo ya PREGUNTADO: es la precondición para que la respuesta se interprete.
	store.SetPendingDeliveryCheck(from, conversation.PendingDeliveryCheck{
		PedidoID: 240, Conductor: "Angel", ModoDatos: "sin_tracking", Preguntado: true,
	})

	reply, manejado := ag.ResponderChequeoEntrega(from, "Sí, ya llegó")
	if !manejado {
		t.Fatal("el chequeo de entrega debió tomar el turno ante un 'sí'")
	}
	if !strings.Contains(strings.ToLower(reply), "gracias") {
		t.Errorf("la confirmación debe agradecer al cliente: %q", reply)
	}
	if _, sigue := store.GetPendingDeliveryCheck(from); sigue {
		t.Error("tras confirmar la entrega, el chequeo debe quedar limpio")
	}
}

func TestClienteDiceQueNoLeHanEntregadoMantieneElPedidoAbierto(t *testing.T) {
	const from = "593999600004"
	ag, store, _ := agenteConMenuCapturado(t)
	store.SetPendingDeliveryCheck(from, conversation.PendingDeliveryCheck{
		PedidoID: 240, Conductor: "Angel", ModoDatos: "sin_tracking", Preguntado: true,
	})

	reply, manejado := ag.ResponderChequeoEntrega(from, "Todavía no")
	if !manejado {
		t.Fatal("el chequeo debió tomar el turno ante un 'no'")
	}
	if !strings.Contains(strings.ToLower(reply), "avis") {
		t.Errorf("ante un 'no' se le debe pedir que avise cuando le entreguen: %q", reply)
	}
	chk, sigue := store.GetPendingDeliveryCheck(from)
	if !sigue {
		t.Fatal("un 'no' NO debe cerrar el chequeo: el pedido sigue pendiente de entrega")
	}
	if chk.Preguntado {
		t.Error("tras un 'no', 'preguntado' debe volver a false (ya no se espera un Sí/No inmediato)")
	}
	if !chk.EsperaYaLlego {
		t.Error("tras un 'no', el chequeo debe quedar a la espera de un 'ya llegó' futuro")
	}
}

// EL BUG QUE EL REVISOR ENCONTRÓ: tras decir "aún no", el "ya llegó" que el propio bot pidió tiene
// que cerrar el pedido. Antes el guard exigía Preguntado=true y ese mensaje se iba al modelo (que
// no sabe cerrar pedidos manuales), dejando el pedido EN_CAMINO para siempre.
func TestNoLuegoYaLlegoCierraElPedido(t *testing.T) {
	const from = "593999600007"
	ag, store, _ := agenteConMenuCapturado(t)
	store.SetPendingDeliveryCheck(from, conversation.PendingDeliveryCheck{
		PedidoID: 240, Conductor: "Angel", ModoDatos: "sin_tracking", Preguntado: true,
	})

	// 1) El cliente dice que aún no.
	if _, manejado := ag.ResponderChequeoEntrega(from, "Todavía no"); !manejado {
		t.Fatal("el 'no' debió tomar el turno")
	}
	// 2) Más tarde escribe "ya llegó" (frase específica, NO está en los afirmativos genéricos).
	reply, manejado := ag.ResponderChequeoEntrega(from, "ya llegó")
	if !manejado {
		t.Fatal("el 'ya llegó' posterior DEBE cerrar el pedido, no irse al modelo")
	}
	if !strings.Contains(strings.ToLower(reply), "gracias") {
		t.Errorf("al cerrar debe agradecer: %q", reply)
	}
	if _, sigue := store.GetPendingDeliveryCheck(from); sigue {
		t.Error("tras el 'ya llegó', el chequeo debe quedar limpio (pedido cerrado)")
	}
}

// Un mensaje que no es ni sí ni no (una pregunta) NO lo debe tomar el chequeo: es conversación.
func TestPreguntaDuranteElChequeoVaAlModelo(t *testing.T) {
	const from = "593999600005"
	ag, store, _ := agenteConMenuCapturado(t)
	store.SetPendingDeliveryCheck(from, conversation.PendingDeliveryCheck{
		PedidoID: 240, Preguntado: true,
	})

	if _, manejado := ag.ResponderChequeoEntrega(from, "¿cuánto cuesta otro cilindro?"); manejado {
		t.Error("una pregunta a mitad del chequeo debe ir al modelo, no ser tratada como sí/no")
	}
}

// Antes de preguntar (goroutine aún pendiente) un mensaje del cliente no debe cerrar el pedido.
func TestNoResuelveSiAunNoSePregunto(t *testing.T) {
	const from = "593999600006"
	ag, store, _ := agenteConMenuCapturado(t)
	store.SetPendingDeliveryCheck(from, conversation.PendingDeliveryCheck{
		PedidoID: 240, Preguntado: false,
	})

	if _, manejado := ag.ResponderChequeoEntrega(from, "sí"); manejado {
		t.Error("sin haber preguntado, el chequeo no debe interpretar un 'sí' como confirmación de entrega")
	}
}

func esperarHasta(t *testing.T, limite time.Duration, cond func() bool) {
	t.Helper()
	fin := time.Now().Add(limite)
	for time.Now().Before(fin) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("la condición no se cumplió dentro del plazo")
}
