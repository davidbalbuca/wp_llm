package agent

import (
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// archivoContiene dice si un archivo del paquete contiene un fragmento. Sirve para comprobar que
// una pieza está CABLEADA y no solo existe: un test que llama a la función directamente pasa
// igual aunque nadie la invoque en el flujo real (pasó con el candado de cobertura).
func archivoContiene(t *testing.T, archivo, fragmento string) bool {
	t.Helper()
	src, err := os.ReadFile(archivo)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", archivo, err)
	}
	return strings.Contains(string(src), fragmento)
}

// LA CONVERSACIÓN SIGUIENTE ARRANCA LIMPIA.
//
// Pedido del dueño (17/09): cuando el ciclo se cierra —el cliente canceló, o el pedido se entregó
// y ya calificó— la próxima conversación tiene que empezar como nueva, "tal como poner el comando
// clear", para que el flujo no confunda temas anteriores.
//
// Hasta ahora la memoria duraba la ventana completa de 24 h de WhatsApp y ningún evento la
// borraba (conversation.SessionGap, decisión de David de agosto). Esa regla sigue valiendo DENTRO
// de un pedido: si el cliente se calla diez minutos a mitad del flujo, no se pierde nada. Lo que
// cambia es el final: al cerrarse el ciclo, se limpia por ESTADO del negocio, no por reloj.

// clienteConCicloTerminado deja a un cliente con todo lo que acumula una conversación completa.
func clienteConCicloTerminado(t *testing.T) (*Agent, conversation.Store, string) {
	t.Helper()
	const from = "593999700001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	// Lo que se tiene que ir: rastro del pedido que acaba de cerrarse.
	store.AppendUser(from, "quiero 2 blancos")
	store.AppendModel(from, "¡Listo! Tu pedido va en camino 🚚")
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})
	store.SetPendingWait(from, conversation.PendingWait{IDProducto: 1, IDColor: 10, Cantidad: 2})
	store.MarcarFueraDeCobertura(from)

	// Lo que se tiene que quedar: quién es el cliente.
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "David Espinoza"})

	return ag, store, from
}

func TestAlCerrarElCicloLaConversacionQuedaLimpia(t *testing.T) {
	ag, store, from := clienteConCicloTerminado(t)

	ag.cerrarCicloDeConversacion(from, "prueba")

	if h := store.History(from); len(h) != 0 {
		t.Errorf("el historial no se limpió (%d turnos): el próximo \"hola\" arrastra el pedido "+
			"anterior y el modelo mezcla los dos", len(h))
	}
	if _, hay := store.GetPedidoEnCurso(from); hay {
		t.Error("la ficha del pedido sigue viva: su color y cantidad reaparecerían en el pedido siguiente")
	}
	if _, hay := store.GetPendingWait(from); hay {
		t.Error("la espera de repartidor sigue viva tras cerrar el ciclo")
	}
	if store.FueraDeCoberturaVerificado(from) {
		t.Error("el rechazo de zona sigue marcado: un \"no llegamos\" de ayer no puede seguir vigente")
	}
}

// Y lo que identifica al cliente NO se borra: si se fuera, el bot le volvería a pedir la cédula
// y el nombre en su próximo pedido. Eso sería un retroceso, no una limpieza.
func TestAlCerrarElCicloElClienteSigueSiendoConocido(t *testing.T) {
	ag, store, from := clienteConCicloTerminado(t)

	ag.cerrarCicloDeConversacion(from, "prueba")

	if _, hay := store.GetAccount(from); !hay {
		t.Error("se borró la cuenta del cliente: habría que crearla otra vez en el próximo pedido")
	}
	p, hay := store.GetProfile(from)
	if !hay || p.Identificacion == "" {
		t.Error("se borró el perfil: el bot le volvería a pedir cédula y nombre a un cliente conocido")
	}
}

// Los dos finales del ciclo tienen que cerrarlo: cancelar y entregar+calificar. Se comprueba
// sobre el archivo, porque un test que solo llame a la función pasa aunque nadie la invoque.
func TestElCierreDeCicloEstaCableadoEnLosDosFinales(t *testing.T) {
	casos := []struct {
		archivo string
		donde   string
	}{
		{"cancelacion.go", "al cancelar el pedido"},
		{"agent.go", "al registrar la calificación"},
	}
	for _, c := range casos {
		if !archivoContiene(t, c.archivo, "a.cerrarCicloDeConversacion(from,") {
			t.Errorf("no se cierra el ciclo %s (%s): la conversación siguiente arrancaría con el "+
				"pedido anterior pegado detrás", c.donde, c.archivo)
		}
	}
}
