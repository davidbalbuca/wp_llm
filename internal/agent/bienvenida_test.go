package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
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
	// 04/10: el gancho reemplaza a "no importa la marca" (no hay marca; el color va en el menú).
	if !strings.Contains(bajo, "se te acabo el gas") {
		t.Errorf("el saludo no abre con el gancho \"¿Se te acabó el gas?\": %q", saludo)
	}
	if strings.Contains(bajo, "marca") {
		t.Errorf("el saludo ya no habla de marcas: %q", saludo)
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

// FUERA DE HORARIO EL SALUDO YA LO DICE (05/10). A las 20:52 el saludo prometía "te lo llevamos
// en minutos" y recién después del color el modelo decía que ya habíamos cerrado.
func agenteSaludoConHorario() *Agent {
	return &Agent{cfg: config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "20:30",
		BotHorarioFinPorDia: "7=19:00", BotDiasLaborables: "1-7"}}
}

func TestDespuesDelCierreElSaludoOfreceAgendarParaManana(t *testing.T) {
	a := agenteSaludoConHorario()
	lunes2052 := time.Date(2026, 10, 5, 20, 52, 0, 0, zonaEcuador)

	txt := a.textoBienvenidaSegunHorario("", lunes2052)

	if strings.Contains(txt, "en minutos") {
		t.Errorf("de noche no se puede prometer la entrega en minutos: %q", txt)
	}
	for _, debe := range []string{"*UbiGas*, tu repartidor aquísito", "ya cerramos", "hasta las 20:30",
		"puedo agendar para mañana desde las 07:00"} {
		if !strings.Contains(txt, debe) {
			t.Errorf("falta %q en: %q", debe, txt)
		}
	}
}

func TestElDomingoAvisaElCierreDelDomingo(t *testing.T) {
	a := agenteSaludoConHorario()
	domingo1930 := time.Date(2026, 10, 4, 19, 30, 0, 0, zonaEcuador)

	if txt := a.textoBienvenidaSegunHorario("", domingo1930); !strings.Contains(txt, "hasta las 19:00") {
		t.Errorf("el domingo cierra a las 19:00: %q", txt)
	}
}

func TestDeMadrugadaSeAgendaParaHoy(t *testing.T) {
	a := agenteSaludoConHorario()
	madrugada := time.Date(2026, 10, 6, 5, 40, 0, 0, zonaEcuador)

	txt := a.textoBienvenidaSegunHorario("", madrugada)
	if !strings.Contains(txt, "desde las 07:00") || strings.Contains(txt, "mañana") {
		t.Errorf("de madrugada abrimos HOY a las 07:00: %q", txt)
	}
}

func TestSiMananaNoSeTrabajaDiceElDia(t *testing.T) {
	a := agenteSaludoConHorario()
	a.cfg.BotDiasLaborables = "1-6" // domingo cerrado
	sabado2100 := time.Date(2026, 10, 10, 21, 0, 0, 0, zonaEcuador)

	if txt := a.textoBienvenidaSegunHorario("", sabado2100); !strings.Contains(txt, "agendar para el lunes") {
		t.Errorf("el domingo no se trabaja: tiene que decir el lunes: %q", txt)
	}
}

func TestEnHorarioElSaludoNoCambia(t *testing.T) {
	a := agenteSaludoConHorario()
	lunes10 := time.Date(2026, 10, 5, 10, 0, 0, 0, zonaEcuador)

	if got, want := a.textoBienvenidaSegunHorario("Ana", lunes10), textoBienvenidaA("Ana", lunes10); got != want {
		t.Errorf("en horario el saludo es el de siempre\ngot  %q\nwant %q", got, want)
	}
}

func TestElAvisoDeCierreNoAfirmaQueYaEstaAgendado(t *testing.T) {
	a := agenteSaludoConHorario()
	for _, hora := range []time.Time{
		time.Date(2026, 10, 5, 20, 52, 0, 0, zonaEcuador), time.Date(2026, 10, 6, 5, 40, 0, 0, zonaEcuador)} {
		if txt := a.textoBienvenidaSegunHorario("", hora); afirmaProgramado(txt) || afirmaPedidoConfirmado(txt) {
			t.Errorf("el saludo ofrece agendar, no afirma que ya está: %q", txt)
		}
	}
}
