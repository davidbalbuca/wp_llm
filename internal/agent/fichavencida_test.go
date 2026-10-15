package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// storeConFichaDeHace devuelve la ficha con la antigüedad que se le diga (el store de memoria
// siempre la fecha "ahora" al guardarla).
type storeConFichaDeHace struct {
	conversation.Store
	hace time.Duration
}

func (s storeConFichaDeHace) GetPedidoEnCurso(phone string) (conversation.PedidoEnCurso, bool) {
	p, ok := s.Store.GetPedidoEnCurso(phone)
	if ok {
		p.UpdatedAt = time.Now().Add(-s.hace)
	}
	return p, ok
}

func TestFichaVigente(t *testing.T) {
	ahora := time.Now()
	amarillo := conversation.PedidoEnCurso{Color: "AMARILLO", Cantidad: 1, UpdatedAt: ahora.Add(-time.Hour)}
	if !fichaVigente(amarillo, ahora) {
		t.Error("una ficha de hace 1 hora sigue vigente: el cliente puede retomarla")
	}
	amarillo.UpdatedAt = ahora.Add(-3 * time.Hour)
	if fichaVigente(amarillo, ahora) {
		t.Error("una ficha de hace 3 horas ya venció")
	}
	vacia := conversation.PedidoEnCurso{Flujo: conversation.FlujoInmediato, UpdatedAt: ahora}
	if fichaVigente(vacia, ahora) {
		t.Error("una ficha vacía (solo dijo \"Hola\") no cuenta como pedido a medio armar")
	}
}

// CASO 04/10: volvió horas después con "hola quisiera un gas" y el bot registró el AMARILLO de la
// conversación anterior. Ahora la ficha vencida se descarta y la conversación empieza de cero.
func TestLaFichaVencidaNoSeReutiliza(t *testing.T) {
	const from = "593900900001"
	base := conversation.NewMemStore()
	base.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "AMARILLO", Cantidad: 1, Flujo: conversation.FlujoInmediato})
	clienteQueVuelveTrasLargoSilencio(base, from)
	ag := agentDePrueba(nil, storeConFichaDeHace{Store: base, hace: 3 * time.Hour})

	if !ag.EmpezarConversacionSiCorresponde(from) {
		t.Error("con la ficha vencida y nada más pendiente, la conversación debía empezar de cero")
	}
	if _, sigue := base.GetPedidoEnCurso(from); sigue {
		t.Error("la ficha vencida sigue guardada: el próximo \"quiero un gas\" la reutilizaría")
	}
}

// Una ficha RECIENTE se respeta: quien se fue 40 minutos a buscar algo retoma donde quedó.
func TestLaFichaRecienteSeRespeta(t *testing.T) {
	const from = "593900900002"
	base := conversation.NewMemStore()
	base.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2, Flujo: conversation.FlujoInmediato})
	clienteQueVuelveTrasLargoSilencio(base, from)
	ag := agentDePrueba(nil, storeConFichaDeHace{Store: base, hace: 40 * time.Minute})

	if ag.EmpezarConversacionSiCorresponde(from) {
		t.Error("se le borró el pedido a medio armar a quien volvió a los 40 minutos")
	}
	if _, sigue := base.GetPedidoEnCurso(from); !sigue {
		t.Error("la ficha reciente desapareció")
	}
}

// Las entregas AGENDADAS no dependen de la ficha: viven en su propia tabla y siguen contando como
// pendientes, aunque la ficha venza.
func TestLaFichaVencidaNoTocaUnaEntregaAgendada(t *testing.T) {
	const from = "593900900003"
	base := conversation.NewMemStore()
	base.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Hora: "16:30", Flujo: conversation.FlujoProgramacion})
	base.CreateScheduled(conversation.ScheduledOrder{Phone: from, Cantidad: 1, ColorNombre: "BLANCO",
		HoraPropuesta: time.Now().Add(20 * time.Hour).Unix()})
	clienteQueVuelveTrasLargoSilencio(base, from)
	ag := agentDePrueba(nil, storeConFichaDeHace{Store: base, hace: 3 * time.Hour})

	if ag.EmpezarConversacionSiCorresponde(from) {
		t.Error("se limpió la conversación de alguien con una entrega agendada")
	}
	if !base.TieneProgramacionViva(from) {
		t.Error("la entrega agendada desapareció")
	}
}

// Quien ya compró recibe, al abrir con un saludo, la OFERTA de repetir (con botones), no el
// pedido anterior registrado en silencio.
func TestAperturaOfreceRepetirElUltimoPedido(t *testing.T) {
	const from = "593900900004"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "AMARILLO", Cantidad: 1})
	var cuerpo string
	var opciones []string
	ag.enviarMenu = func(_ string, c string, o []string) error { cuerpo, opciones = c, o; return nil }
	ag.DejarBienvenidaPendiente(from, textoBienvenida(""))

	if _, manejado := ag.ResponderAperturaConColores(from, "hola quisiera un gas"); !manejado {
		t.Fatal("la apertura no se resolvió en código")
	}
	if !strings.Contains(cuerpo, "lo mismo de la última vez: 1 AMARILLO") {
		t.Errorf("no se le ofreció repetir su último pedido: %q", cuerpo)
	}
	if len(opciones) != 2 || opciones[0] != BotonRepetirPedido {
		t.Errorf("opciones = %v", opciones)
	}
	if p, hay := store.GetPedidoEnCurso(from); hay && !p.Vacio() {
		t.Errorf("se armó un pedido sin que el cliente lo confirmara: %+v", p)
	}
}

// CASO 04/10 (prod): el bot ofreció "¿cancelemos este pedido?" con un pedido a medio armar; el
// cliente dijo "Cancela" y recibió "no tienes ningún pedido en curso", y la ficha siguió viva.
func TestCancelarDescartaElPedidoAMedioArmar(t *testing.T) {
	const from = "593900900010"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 1, Flujo: conversation.FlujoProgramacion})

	salida, manejado := ag.ResponderCancelacion(from, "Cancela")

	if !manejado {
		t.Fatal("el cancelar no se resolvió en código")
	}
	if strings.Contains(strings.ToLower(salida), "no tienes ningún pedido") {
		t.Errorf("se le dijo que no tenía pedido a quien estaba armando uno: %q", salida)
	}
	if !strings.Contains(salida, "1 BLANCO") {
		t.Errorf("no se le dijo qué se canceló: %q", salida)
	}
	if _, sigue := store.GetPedidoEnCurso(from); sigue {
		t.Error("el pedido a medio armar sigue guardado")
	}
}

// Sin nada armado, la respuesta de siempre.
func TestCancelarSinNadaSigueDiciendoQueNoHayPedido(t *testing.T) {
	const from = "593900900011"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Flujo: conversation.FlujoInmediato}) // vacía

	salida, manejado := ag.ResponderCancelacion(from, "cancelar")
	if !manejado || !strings.Contains(strings.ToLower(salida), "no tienes ningún pedido") {
		t.Errorf("sin pedido armado debía decir que no hay pedido: %q", salida)
	}
}
