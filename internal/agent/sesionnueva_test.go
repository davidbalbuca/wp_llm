package agent

import (
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// LA CONVERSACIÓN ARRANCA LIMPIA CUANDO NO HAY NADA PENDIENTE.
//
// Pedido del dueño (18/09): "lo de poner clear al iniciar las conversaciones".
//
// Aquí hay dos errores posibles y son MUY distintos de gravedad:
//
//   - No limpiar cuando tocaba → el cliente arrastra el pedido de ayer. Molesto.
//   - Limpiar cuando NO tocaba → se le borra el pedido a alguien que lo está haciendo. Le cuesta
//     la venta al negocio y el cliente tiene que empezar de cero sin entender por qué.
//
// Por eso la mayoría de los tests de abajo son del segundo tipo: cada estado "en curso" tiene el
// suyo. Si mañana se añade un estado nuevo al flujo y nadie lo suma a tienePendiente, el test de
// cobertura (el último) lo caza.

// clienteQueVuelve deja a un cliente que escribió hace mucho y no tiene nada pendiente.
func clienteQueVuelve(t *testing.T) (*Agent, conversation.Store, string) {
	t.Helper()
	const from = "593999300001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	// Rastro del pedido anterior, que terminó sin pasar por ningún final.
	store.AppendUser(from, "quiero 2 blancos")
	store.AppendModel(from, "¡Listo! Tu pedido va en camino 🚚")
	// Y su identidad, que NO se puede perder.
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "David Espinoza"})

	envejecer(t, store, from, 2*time.Hour)
	return ag, store, from
}

// envejecer simula que el último mensaje del cliente fue hace `hace`. El hook vive solo en el
// store de memoria (mismo patrón que ForzarFechaUbicacion), así que se llega por type assertion.
func envejecer(t *testing.T, store conversation.Store, from string, hace time.Duration) {
	t.Helper()
	reloj, ok := store.(interface {
		ForzarUltimaActividad(phone string, cuando time.Time)
	})
	if !ok {
		t.Fatal("el store de pruebas no permite envejecer la última actividad")
	}
	reloj.ForzarUltimaActividad(from, time.Now().Add(-hace))
}

// EL CASO PRINCIPAL: vuelve tras horas, sin nada pendiente → arranca de cero.
func TestVolverSinNadaPendienteLimpiaLaConversacion(t *testing.T) {
	ag, store, from := clienteQueVuelve(t)

	if !ag.EmpezarConversacionSiCorresponde(from) {
		t.Fatal("no se limpió la conversación de un cliente que volvió tras 2 h sin nada pendiente: " +
			"su próximo \"hola\" arrastra el pedido anterior y el modelo mezcla los dos")
	}
	if h := store.History(from); len(h) != 0 {
		t.Errorf("el historial no se limpió (%d turnos)", len(h))
	}
}

// Y su identidad NO se pierde: si se fuera, habría que pedirle otra vez cédula y nombre, que es
// justo el retroceso que este proyecto lleva meses evitando.
func TestAlEmpezarDeCeroElClienteSigueSiendoConocido(t *testing.T) {
	ag, store, from := clienteQueVuelve(t)

	ag.EmpezarConversacionSiCorresponde(from)

	if _, hay := store.GetAccount(from); !hay {
		t.Error("se borró la cuenta del cliente")
	}
	if p, hay := store.GetProfile(from); !hay || p.Identificacion == "" {
		t.Error("se borró el perfil: se le volvería a pedir la cédula a un cliente conocido")
	}
}

// Si escribió hace un momento, es LA MISMA conversación: no se toca nada aunque no haya pedido.
// Sin esto, un cliente que pregunta el precio, lo piensa y responde perdería el contexto.
func TestEscribirSeguidoNoLimpiaNada(t *testing.T) {
	ag, store, from := clienteQueVuelve(t)
	envejecer(t, store, from, 1*time.Minute)

	if ag.EmpezarConversacionSiCorresponde(from) {
		t.Error("se limpió la conversación de alguien que escribió hace un minuto")
	}
	if len(store.History(from)) == 0 {
		t.Error("se perdió el historial de una conversación en curso")
	}
}

// LOS QUE NO SE PUEDEN TOCAR: cada estado "a mitad de algo" tiene su caso. Aquí el fallo es caro,
// así que se comprueban TODOS, no una muestra.
func TestNoSeLimpiaANadieQueEsteAMitadDeAlgo(t *testing.T) {
	casos := []struct {
		que    string
		montar func(s conversation.Store, from string)
		porQue string
	}{
		{
			que:    "pedido en camino",
			montar: func(s conversation.Store, from string) { s.SetActivePedido(from, 942) },
			porQue: "tiene gas en camino; borrarle el contexto le impide preguntar por él o cancelarlo",
		},
		{
			que: "espera de repartidor",
			montar: func(s conversation.Store, from string) {
				s.SetPendingWait(from, conversation.PendingWait{IDProducto: 1, Cantidad: 2})
			},
			porQue: "está esperando que le asignen repartidor",
		},
		{
			que: "pedido a medio armar",
			montar: func(s conversation.Store, from string) {
				s.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})
			},
			porQue: "ya eligió color y cantidad; perderlos le obliga a repetirlo todo",
		},
		{
			que: "pedido esperando dirección",
			montar: func(s conversation.Store, from string) {
				s.SetPedidoEsperandoDireccion(from, []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}})
			},
			porQue: "el pedido está en pausa esperando que confirme a dónde va",
		},
		{
			que:    "pedido en pausa por verificación",
			montar: func(s conversation.Store, from string) { s.SetOrderDraft(from, conversation.OrderDraft{Cantidad: 1}) },
			porQue: "su pedido espera el código de verificación",
		},
		{
			que: "verificación pendiente",
			montar: func(s conversation.Store, from string) {
				s.SetPendingVerification(from, conversation.Account{Username: "u"})
			},
			porQue: "el próximo mensaje suyo es el código, no una conversación nueva",
		},
		{
			que: "calificación pendiente",
			montar: func(s conversation.Store, from string) {
				s.SetPendingRating(from, conversation.PendingRating{PedidoID: 5, Conductor: "Nelson"})
			},
			porQue: "se le pidió calificar y su próximo mensaje puede ser la nota",
		},
		{
			que: "oferta de color sin responder",
			montar: func(s conversation.Store, from string) {
				s.SetPendingColorSwap(from, conversation.PendingColorSwap{Cantidad: 1})
			},
			porQue: "se le ofreció otro color y su respuesta quedaría sin contexto",
		},
		{
			que: "oferta de guardar ubicación",
			montar: func(s conversation.Store, from string) {
				s.SetPendingGuardarUbicacion(from, conversation.PendingGuardarUbicacion{Latitude: -2.9, Longitude: -79.0})
			},
			porQue: "se le preguntó si guarda su ubicación y va a contestar",
		},
		{
			que:    "consentimiento sin responder",
			montar: func(s conversation.Store, from string) { s.SetConsentimientoPendiente(from) },
			porQue: "se le pidió autorizar sus datos; su sí/no debe resolverse en código",
		},
		{
			que:    "menú de horas sin responder",
			montar: func(s conversation.Store, from string) { s.SetEligiendoHora(from) },
			porQue: "está eligiendo a qué hora quiere su entrega agendada",
		},
	}

	for _, c := range casos {
		t.Run(c.que, func(t *testing.T) {
			ag, store, from := clienteQueVuelve(t)
			c.montar(store, from)

			if ag.EmpezarConversacionSiCorresponde(from) {
				t.Errorf("se limpió la conversación de un cliente con %s: %s", c.que, c.porQue)
			}
			if len(store.History(from)) == 0 {
				t.Errorf("se perdió el historial de un cliente con %s", c.que)
			}
		})
	}
}

// NO-REGRESIÓN DE COBERTURA: si mañana se añade un estado "en curso" al flujo y nadie lo suma a
// tienePendiente, el bot empezará a borrarle la conversación a clientes que están a mitad de algo
// —y en silencio—. Este test obliga a que cada estado nuevo pase por aquí.
func TestTienePendienteCubreTodosLosEstadosEnCurso(t *testing.T) {
	// Los Get* del Store que representan "algo en curso". Si se añade uno nuevo y no está en
	// tienePendiente, este test falla y hay que decidir explícitamente qué hacer con él.
	estados := []string{
		"GetActivePedido", "GetPendingWait", "GetPedidoEnCurso", "GetPedidoEsperandoDireccion",
		"GetOrderDraft", "GetPendingVerification", "GetPendingRating", "GetPendingColorSwap",
		"GetPendingGuardarUbicacion", "GetConfirmingSchedule", "TieneProgramacionViva",
		"ConsentimientoPendiente", "EligiendoHora",
	}
	for _, e := range estados {
		if !archivoContiene(t, "sesionnueva.go", e+"(from)") {
			t.Errorf("tienePendiente no comprueba %s: a un cliente con ese estado se le borraría "+
				"la conversación mientras está a mitad de algo", e)
		}
	}
}

// Y está CABLEADO en el webhook: un test que solo llame a la función pasa igual aunque nadie la
// invoque al recibir un mensaje.
func TestElArranqueLimpioEstaCableadoEnElWebhook(t *testing.T) {
	if !archivoContiene(t, "../../cmd/bot/main.go", "ag.EmpezarConversacionSiCorresponde(inc.From)") {
		t.Error("no está cableado en el webhook: la conversación seguiría arrastrando el pedido " +
			"anterior durante las 24 h de la ventana de WhatsApp")
	}
}
