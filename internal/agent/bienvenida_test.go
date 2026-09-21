package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// EL BOT SE PRESENTA SIEMPRE AL EMPEZAR UNA CONVERSACIÓN.
//
// Pedido de David (21/09): "ahí debe lanzar siempre siempre siempre un mensaje de bienvenida
// como contando lo que hace Ubi — si alguien escribe, debe decirle hola soy Ubi, te conecto con
// el repartidor más cercano en minutos, no importa el color de gas, lo buscamos y te lo
// llevamos, alguna cosa así".
//
// LO QUE PASABA (captura del 20/09, 593990364311): el cliente escribe "Deseo pedir GAS 😄" y el
// bot contesta DIRECTO con "¿Qué color de cilindro prefieres? BLANCO / AMARILLO / NARANJA /
// AZUL". Nunca dice quién es ni qué hace.
//
// Eso cuesta de dos formas. El que llega por un anuncio no sabe con quién está hablando, y el
// que viene con una duda ("¿traen a mi zona?", "¿cuánto cuesta?") recibe un formulario en vez de
// una respuesta. Un saludo de dos líneas contesta las dos cosas antes de que las pregunten.
//
// POR QUÉ EN CÓDIGO Y NO EN EL PROMPT. Es la lección que este proyecto ya aprendió cuatro veces:
// el prompt PIDE y el modelo a veces no obedece. "Siempre siempre siempre" no se puede dejar a
// que el modelo se acuerde.

// clienteQueVuelveTrasLargoSilencio deja el estado de quien escribe por primera vez en el día.
func clienteQueVuelveTrasLargoSilencio(store conversation.Store, from string) {
	store.TouchActivity(from)
	forzar, ok := store.(interface {
		ForzarUltimaActividad(string, time.Time)
	})
	if ok {
		forzar.ForzarUltimaActividad(from, time.Now().Add(-3*time.Hour))
	}
}

func TestAlEmpezarLaConversacionElBotSePresenta(t *testing.T) {
	const from = "593990364311"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteQueVuelveTrasLargoSilencio(store, from)

	saludo, hay := ag.SaludoDeBienvenida(from)

	if !hay {
		t.Fatal("no se saluda a quien empieza una conversación: el cliente no sabe con quién habla")
	}
	bajo := normalizar(saludo)
	// Los tres puntos que pidió David, cada uno por su motivo.
	if !strings.Contains(bajo, "ubi") {
		t.Errorf("el saludo no dice quién es: %q", saludo)
	}
	if !strings.Contains(bajo, "repartidor") && !strings.Contains(bajo, "reparto") {
		t.Errorf("el saludo no explica que conectamos con un repartidor: %q", saludo)
	}
	if !strings.Contains(bajo, "color") {
		t.Errorf("el saludo no dice que da igual el color/marca del cilindro, que es lo que más "+
			"frena a quien tiene un cilindro de otra marca en casa: %q", saludo)
	}
}

// A QUIEN ESTÁ A MITAD DE UNA CONVERSACIÓN NO SE LE SALUDA OTRA VEZ.
//
// Sin esto el bot se presentaría en cada mensaje, que es peor que no presentarse: parece roto.
func TestAMitadDeLaConversacionNoSeVuelveASaludar(t *testing.T) {
	const from = "593999950001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.TouchActivity(from) // acaba de escribir

	if _, hay := ag.SaludoDeBienvenida(from); hay {
		t.Error("se saludó a quien ya estaba conversando: el bot se presentaría en cada mensaje")
	}
}

// Y AL QUE TIENE UN PEDIDO EN CURSO TAMPOCO, aunque vuelva tras un silencio largo: está
// esperando su gas, no empezando de cero. Saludarlo con la presentación le haría pensar que el
// bot se olvidó de su pedido.
func TestConPedidoEnCursoNoSeSaluda(t *testing.T) {
	const from = "593999950002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteQueVuelveTrasLargoSilencio(store, from)
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO",
	})

	if _, hay := ag.SaludoDeBienvenida(from); hay {
		t.Error("se saludó con la presentación a alguien que tiene un pedido esperando repartidor")
	}
}

// El cliente de SIEMPRE también recibe el saludo, pero por su nombre: ya nos conoce, y tratarlo
// como a un desconocido es un retroceso.
func TestAlClienteConocidoSeLeSaludaPorSuNombre(t *testing.T) {
	const from = "593999950003"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteQueVuelveTrasLargoSilencio(store, from)
	store.SetProfile(from, conversation.Profile{
		Identificacion: "0105566777", Nombres: "David Espinoza",
	})

	saludo, hay := ag.SaludoDeBienvenida(from)
	if !hay {
		t.Fatal("no se saludó al cliente conocido")
	}
	if !strings.Contains(saludo, "David") {
		t.Errorf("no se le saluda por su nombre, y lo conocemos: %q", saludo)
	}
}

// El saludo es CORTO. Es lo primero que ve el cliente: un párrafo largo se salta entero, y
// entonces no sirvió de nada.
func TestElSaludoEsCorto(t *testing.T) {
	const from = "593999950004"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteQueVuelveTrasLargoSilencio(store, from)

	saludo, _ := ag.SaludoDeBienvenida(from)
	if n := len([]rune(saludo)); n > 320 {
		t.Errorf("el saludo tiene %d caracteres: demasiado para lo primero que se lee.\n%q", n, saludo)
	}
}

// Y ESTÁ CABLEADO en el webhook: si no se llama, no saluda nadie.
func TestElSaludoEstaCableado(t *testing.T) {
	src := leerSinComentarios(t, "../../cmd/bot/main.go")
	if !strings.Contains(src, "SaludoDeBienvenida(") {
		t.Error("el webhook no llama a SaludoDeBienvenida: el bot existe pero no se presenta nunca")
	}
}
