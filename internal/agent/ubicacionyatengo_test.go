package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// EL BOT PIDIÓ UNA UBICACIÓN QUE ACABABA DE RECIBIR.
//
// QA, 15/09 (H-02): mandó tres pines en el mismo minuto y el bot respondió al segundo con el
// texto genérico de "compárteme tu ubicación". Con los envíos espaciados un minuto, los tres
// funcionaron. QA lo atribuyó a turnos que se mezclan.
//
// Revisando el webhook, la concurrencia ya está resuelta: hay un mutex POR TELÉFONO
// (lockCliente, cmd/bot/main.go) tomado antes de leer o escribir el store, así que dos mensajes
// del mismo cliente no se pisan — de ahí que el pedido duplicado que QA temía no ocurriera.
//
// Lo que NO estaba cubierto es lo de después: la ubicación se guarda, el turno llega al modelo,
// y el modelo —que ve el bloque UBICACION recién puesto pero viene de tres mensajes seguidos—
// contesta igual "compárteme tu ubicación". El pin no se perdió: se ignoró al redactar. Es el
// mismo patrón de todos los demás candados (el modelo afirma o pide algo que contradice el
// estado), y se resuelve igual: comprobando el estado, no recordándoselo en el prompt.

func TestNoSePideLaUbicacionCuandoYaSeTiene(t *testing.T) {
	const from = "593999600001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()

	// El cliente acaba de compartir su ubicación en esta conversación.
	store.SetLocation(from, -2.9, -79.0)

	// Lo que el modelo respondió de todos modos.
	pedidos := []string{
		"Para continuar, compárteme tu ubicación por WhatsApp 📎",
		"Necesito tu ubicación para poder enviarte el pedido 😊",
		"¿Me compartes tu ubicación por favor?",
	}
	for _, reply := range pedidos {
		got := ag.revisarPedidoDeUbicacionRedundante(from, reply)
		if got == reply {
			t.Errorf("el bot pidió una ubicación que ya tenía y el texto pasó intacto: %q", reply)
		}
	}
}

// Sin ubicación guardada, pedirla es exactamente lo correcto: el candado no debe tocarlo.
func TestSinUbicacionSePuedePedirTranquilamente(t *testing.T) {
	const from = "593999600002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()

	reply := "Para continuar, compárteme tu ubicación por WhatsApp 📎"
	if got := ag.revisarPedidoDeUbicacionRedundante(from, reply); got != reply {
		t.Errorf("sin ubicación guardada, pedirla es correcto y no debe tocarse:\n  entrada: %q\n  salida:  %q", reply, got)
	}
}

// Con una ubicación VIEJA (de otra conversación) también se puede pedir: es justo lo que hace la
// guardia de dirección antes de registrar un pedido. Solo la ubicación FRESCA hace redundante el
// pedido.
func TestConUbicacionViejaSePuedePedirDeNuevo(t *testing.T) {
	const from = "593999600003"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()

	store.SetLocation(from, -2.9, -79.0)
	// Se envejece la ubicación más allá de la ventana de conversación.
	envejecerUbicacion(t, store, from)

	reply := "Para continuar, compárteme tu ubicación por WhatsApp 📎"
	if got := ag.revisarPedidoDeUbicacionRedundante(from, reply); got != reply {
		t.Errorf("con ubicación vieja pedirla de nuevo es correcto:\n  entrada: %q\n  salida:  %q", reply, got)
	}
}

// Cableado al turno, no solo existente. Misma lección que el candado de cobertura afirmada: los
// tests que llaman a la función directa pasan aunque nadie la invoque en HandleMessage.
func TestElCandadoDeUbicacionRedundanteEstaCableado(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("no se pudo leer agent.go: %v", err)
	}
	if !strings.Contains(string(src), "reply = a.revisarPedidoDeUbicacionRedundante(from, reply)") {
		t.Error("revisarPedidoDeUbicacionRedundante no se aplica a la respuesta en agent.go: el " +
			"candado existe pero no lo usa nadie")
	}
}

// Y no se mete con textos que no piden ubicación.
func TestElCandadoDeUbicacionNoTocaOtrosTextos(t *testing.T) {
	const from = "593999600004"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoConZonas()
	store.SetLocation(from, -2.9, -79.0)

	intactos := []string{
		"¡Listo! Tu pedido va en camino con Nelson 🚚",
		"¿De qué color lo necesitas: BLANCO, AMARILLO, NARANJA o AZUL?",
		"Tu ubicación ya la tengo, gracias 😊",
	}
	for _, reply := range intactos {
		if got := ag.revisarPedidoDeUbicacionRedundante(from, reply); got != reply {
			t.Errorf("el candado tocó un texto que no pedía ubicación:\n  entrada: %q\n  salida:  %q", reply, got)
		}
	}
}

// envejecerUbicacion deja la ubicación fuera de la ventana de conversación, para probar el caso
// de "la tengo, pero es de otra conversación". Usa el mismo gancho que direccion_test.go.
func envejecerUbicacion(t *testing.T, store conversation.Store, from string) {
	t.Helper()
	envejecer, ok := store.(interface {
		ForzarFechaUbicacion(phone string, cuando time.Time)
	})
	if !ok {
		t.Fatal("el store en memoria debería permitir envejecer la ubicación en pruebas")
	}
	envejecer.ForzarFechaUbicacion(from, time.Now().Add(-6*time.Hour))
}
