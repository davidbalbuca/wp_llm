package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// LA HORA DE UNA ENTREGA SE TOCA, NO SE ESCRIBE.
//
// Pedido del dueño (18/09): sugerir opciones para que el cliente solo dé click.
//
// ⚠️ ESTO TOCA UNA REGLA QUE EXISTE POR UN INCIDENTE REAL. El prompt dice "nunca ofrezcas horas
// como opciones" desde el 02/09, cuando un cliente compartió su ubicación a las 22:49 y el bot le
// agendó una entrega para las 06:00 que él NUNCA pidió: la hora se la inventó el modelo.
//
// La distinción que hace esto seguro: lo prohibido es que **el MODELO** invente una hora. Este
// menú lo calcula el CÓDIGO con el reloj y el horario configurado, y la hora que sale es la que el
// cliente TOCÓ. Los tests de abajo defienden justamente esa frontera.

// agenteConHorario arma un agente con el horario del negocio configurado.
func agenteConHorario(t *testing.T, inicio, fin string) (*Agent, conversation.Store) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: inicio, BotHorarioFin: fin}
	return ag, store
}

// agenteQuePuedeAgendar añade lo que hace falta para que programar_entrega llegue hasta el final:
// catálogo y formas de pago.
//
// Es imprescindible para que los tests de la guarda midan LA GUARDA. Sin el catálogo,
// programar_entrega falla con "no puedo consultar el catálogo" y el test pasa sin haber ejercido
// nada — es exactamente cómo el primer mutante de este archivo sobrevivió.
func agenteQuePuedeAgendar(t *testing.T) (*Agent, conversation.Store) {
	t.Helper()
	ag, store := agenteConHorario(t, "07:00", "19:00")
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	return ag, store
}

// clienteListoParaAgendar le deja todo lo necesario: ubicación, datos y un pedido esperando
// conductor. Así lo ÚNICO que puede frenar la programación es la guarda que se está probando.
func clienteListoParaAgendar(store conversation.Store, from string) {
	store.SetLocation(from, -2.898, -79.002)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "David Espinoza"})
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 2, IDTipoPago: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO",
		Identificacion: "0105566777", Nombres: "David Espinoza",
	})
}

func aLas(h, m int) time.Time {
	// Un día cualquiera: lo que importa es la hora del día.
	return time.Date(2026, 9, 18, h, m, 0, 0, zonaEcuador)
}

// Las horas sugeridas salen del HORARIO CONFIGURADO, no de una lista fija.
func TestLasHorasSugeridasRespetanElHorarioDelNegocio(t *testing.T) {
	ag, _ := agenteConHorario(t, "07:00", "19:00")

	for _, h := range ag.horasSugeridas(aLas(9, 0)) {
		hora := extraerHora(h)
		mins := parseHoraHHMM(hora)
		if mins < parseHoraHHMM("07:00") || mins >= parseHoraHHMM("19:00") {
			t.Errorf("se sugirió %q, fuera del horario de atención: el cliente elegiría una hora "+
				"que nadie puede cumplir", h)
		}
	}
}

// NO se ofrece una hora que ya pasó ni una inmediata: a las 14:58 no se puede prometer las 15:00.
func TestNoSeOfrecenHorasQueNoSePuedenCumplir(t *testing.T) {
	ag, _ := agenteConHorario(t, "07:00", "19:00")
	ahora := aLas(14, 58)

	for _, h := range ag.horasSugeridas(ahora) {
		if !strings.HasPrefix(h, "hoy") {
			continue // las de mañana siempre caben
		}
		mins := parseHoraHHMM(extraerHora(h))
		momento := aLas(mins/60, mins%60)
		if momento.Sub(ahora) < margenParaPrepararse {
			t.Errorf("se sugirió %q cuando son las %s: no da tiempo a prepararla y el cliente se "+
				"queda esperando algo que no llega", h, ahora.Format("15:04"))
		}
	}
}

// Tarde, cuando hoy ya no cabe nada, se sigue con MAÑANA. Una lista vacía no le sirve a nadie.
func TestDeNocheSeOfrecenLasHorasDeManana(t *testing.T) {
	ag, _ := agenteConHorario(t, "07:00", "19:00")

	horas := ag.horasSugeridas(aLas(22, 30))
	if len(horas) == 0 {
		t.Fatal("a las 22:30 no se ofreció ninguna hora: el cliente que escribe de noche se queda " +
			"sin poder agendar de un toque")
	}
	for _, h := range horas {
		if !strings.HasPrefix(h, "mañana") {
			t.Errorf("a las 22:30 se ofreció %q: hoy ya no se puede entregar", h)
		}
	}
}

// SIEMPRE se puede escapar del menú. Sin esta salida sería una jaula: el negocio atiende 12 horas
// y en el menú caben cuatro.
func TestSiempreSePuedeElegirOtraHora(t *testing.T) {
	ag, _ := agenteConHorario(t, "07:00", "19:00")

	opciones := ag.OpcionesDeHora(aLas(10, 0))
	if len(opciones) == 0 {
		t.Fatal("no se ofreció ninguna opción")
	}
	if opciones[len(opciones)-1] != BotonOtraHora {
		t.Errorf("falta la salida %q: quien quiera las 16:45 no podría pedirla", BotonOtraHora)
	}
}

// El menú no puede ser largo: una lista de diez horas se lee peor que escribir la hora, y deja de
// ser un atajo para volverse otra decisión.
func TestElMenuDeHorasNoAbruma(t *testing.T) {
	ag, _ := agenteConHorario(t, "07:00", "19:00")

	if n := len(ag.OpcionesDeHora(aLas(7, 0))); n > maxHorasSugeridas+1 {
		t.Errorf("el menú tiene %d opciones: demasiadas para elegir de un vistazo", n)
	}
}

// Con el horario mal configurado NO se inventa nada: sin menú, el cliente escribe la hora como
// siempre. Mejor eso que ofrecerle horas imposibles.
func TestConHorarioInvalidoNoSeOfreceMenu(t *testing.T) {
	for _, caso := range [][2]string{{"", ""}, {"19:00", "07:00"}, {"abc", "def"}} {
		ag, _ := agenteConHorario(t, caso[0], caso[1])
		if opciones := ag.OpcionesDeHora(aLas(10, 0)); len(opciones) > 0 {
			t.Errorf("con horario %v se ofrecieron horas: %v", caso, opciones)
		}
	}
}

// El botón se traduce de vuelta a hora y día sin ambigüedad.
func TestElBotonSeTraduceAHoraYDia(t *testing.T) {
	casos := []struct {
		boton    string
		hora     string
		esManana bool
	}{
		{"hoy 15:00", "15:00", false},
		{"mañana 07:00", "07:00", true},
		{"mañana 18:00", "18:00", true},
	}
	for _, c := range casos {
		hora, esManana, ok := horaDeBoton(c.boton)
		if !ok {
			t.Errorf("no se pudo leer el botón %q", c.boton)
			continue
		}
		if hora != c.hora || esManana != c.esManana {
			t.Errorf("%q se leyó como hora=%q mañana=%v; se esperaba %q/%v",
				c.boton, hora, esManana, c.hora, c.esManana)
		}
	}
}

// Y lo que NO es un botón de hora no se interpreta como tal: "Otra hora" es una salida, no una
// hora, y un mensaje cualquiera tampoco.
func TestLoQueNoEsUnBotonDeHoraNoSeInterpreta(t *testing.T) {
	for _, texto := range []string{BotonOtraHora, "hola", "quiero 2 cilindros", "a las 7", ""} {
		if _, _, ok := horaDeBoton(texto); ok {
			t.Errorf("%q se tomó por un botón de hora", texto)
		}
	}
}

// ═══ LA FRONTERA QUE PROTEGE EL INCIDENTE DEL 02/09 ═══
//
// Sin el menú pendiente, una hora suelta NO se agenda. Es la garantía de que no se repite la
// entrega fantasma: la hora tiene que venir de un botón que el cliente tocó en ESTE momento.
func TestSinMenuPendienteNoSeAgendaNadaSolo(t *testing.T) {
	const from = "593999100001"
	ag, store := agenteQuePuedeAgendar(t)

	// EL CLIENTE TIENE TODO LISTO. Es imprescindible montarlo así: la primera versión de este test
	// dejaba al cliente vacío y pasaba porque programar_entrega fallaba por "falta la cantidad",
	// no por la guarda — el mutante que la quitaba SOBREVIVIÓ. Aquí lo único que puede detenerlo
	// es la guarda. Se usa MAÑANA para que la hora sea futura corra a la hora que corra el test.
	clienteListoParaAgendar(store, from)

	_, manejado := ag.ResponderHoraProgramada(from, "mañana 15:00")

	if manejado {
		t.Error("se agendó una entrega sin haberle ofrecido el menú: es exactamente el incidente " +
			"del 02/09 (una entrega que el cliente nunca pidió)")
	}
	if store.TieneProgramacionViva(from) {
		t.Error("quedó una programación viva que nadie pidió")
	}
}

// Y CON el menú ofrecido, el mismo cliente SÍ agenda: si no, el test de arriba pasaría por
// cualquier motivo (falta un dato, el catálogo no responde) sin probar la guarda.
func TestConElMenuOfrecidoLaHoraElegidaSiSeAgenda(t *testing.T) {
	const from = "593999100004"
	ag, store := agenteQuePuedeAgendar(t)
	clienteListoParaAgendar(store, from)
	store.SetEligiendoHora(from)

	// Se elige una hora de MAÑANA: siempre es futura, sea cual sea la hora a la que corra el test.
	respuesta, manejado := ag.ResponderHoraProgramada(from, "mañana 15:00")

	if !manejado {
		t.Fatal("con el menú ofrecido y todos los datos, la hora elegida no agendó nada: el " +
			"cliente toca un botón y no pasa nada")
	}
	if !store.TieneProgramacionViva(from) {
		t.Error("no quedó ninguna programación viva tras elegir la hora")
	}
	if store.EligiendoHora(from) {
		t.Error("sigue esperando que elija hora después de haber elegido")
	}
	if !strings.Contains(respuesta, "15:00") {
		t.Errorf("no se le confirmó la hora que eligió: %q", respuesta)
	}
}

// "Otra hora" saca del menú y le pide la hora escrita: el flujo de siempre, intacto.
func TestOtraHoraDevuelveAlFlujoEscrito(t *testing.T) {
	const from = "593999100002"
	ag, store := agenteConHorario(t, "07:00", "19:00")
	store.SetEligiendoHora(from)

	respuesta, manejado := ag.ResponderHoraProgramada(from, BotonOtraHora)
	if !manejado {
		t.Fatal("no se atendió la salida del menú")
	}
	if store.EligiendoHora(from) {
		t.Error("sigue esperando que elija del menú tras pedir otra hora")
	}
	if !strings.Contains(respuesta, "07:00") || !strings.Contains(respuesta, "19:00") {
		t.Errorf("no se le dijo el horario para que elija: %q", respuesta)
	}
}

// Si con el menú abierto escribe otra cosa (una pregunta), NO se interpreta: lo atiende el modelo
// y la espera sigue en pie.
func TestConElMenuAbiertoUnaPreguntaLaAtiendeElModelo(t *testing.T) {
	const from = "593999100003"
	ag, store := agenteConHorario(t, "07:00", "19:00")
	store.SetEligiendoHora(from)

	if _, manejado := ag.ResponderHoraProgramada(from, "¿y cuánto cuesta el envío?"); manejado {
		t.Error("se tomó una pregunta como elección de hora")
	}
	if !store.EligiendoHora(from) {
		t.Error("se perdió la espera del menú: su elección posterior ya no se resolvería en código")
	}
}

// Las piezas están CABLEADAS: el menú se ofrece al elegir "Programar" y el botón se resuelve en el
// webhook. Un test que solo llame a las funciones pasa igual aunque nadie las invoque.
func TestElMenuDeHorasEstaCableado(t *testing.T) {
	if !archivoContiene(t, "menus.go", "a.OfrecerHorasParaProgramar(from)") {
		t.Error("no se ofrecen horas al elegir 'Programar': el cliente seguiría teniendo que escribirla")
	}
	if !archivoContiene(t, "../../cmd/bot/main.go", "ag.ResponderHoraProgramada(inc.From, inc.Text)") {
		t.Error("el botón de hora no se resuelve en el webhook: su elección se iría al modelo")
	}
}
