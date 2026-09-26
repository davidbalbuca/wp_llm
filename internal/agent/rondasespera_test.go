package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// PERDIMOS A CARLOS (593959545411, 23-sep 17:45) POR PREGUNTARLE UNA SOLA VEZ.
//
//	17:48  pedido registrado, sin conductor → el backend empieza a buscar
//	17:51  (+3 min) el bot: "¿Deseas esperar?"  [Esperar / Programar / Cancelar]
//	17:52  Carlos: "Esperar"
//	17:57  (+5 min) "no hay ningún repartidor disponible. Intenta más tarde."
//
// Ocho minutos y una sola pregunta. Carlos dijo que sí esperaba —estaba dispuesto— y el bot lo
// despidió sin volver a consultarle. No es un error técnico: es una decisión de producto que
// estaba escrita en el código como un booleano (`PreguntoEspera`) y un `5 * time.Minute`.
//
// Lo que se pide ahora:
//   - la espera dura lo que diga BOT_ESPERA_RONDA_MIN (15 min por defecto), no 5;
//   - se le pregunta hasta DOS veces si quiere seguir esperando;
//   - en la TERCERA solo quedan [Reprogramar / Cancelar], con disculpas;
//   - si cancela, se le despide con amabilidad.

func esperaDePrueba() conversation.PendingWait {
	return conversation.PendingWait{IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 1}
}

// La ronda decide QUÉ se le ofrece. Es la regla de negocio entera en una función, y por eso se
// prueba sola: las tres primeras rondas tienen que dar opciones distintas.
func TestQueSeLeOfreceEnCadaRonda(t *testing.T) {
	casos := []struct {
		ronda    int
		opciones []string
		nota     string
	}{
		{0, []string{"Esperar", "Programar", "Cancelar"}, "primera vez: puede esperar"},
		{1, []string{"Esperar", "Programar", "Cancelar"}, "segunda: todavía puede esperar"},
		{2, []string{"Reprogramar", "Cancelar"}, "tercera: ya no se le hace esperar más"},
		{3, []string{"Reprogramar", "Cancelar"}, "de la tercera en adelante, igual"},
	}
	for _, c := range casos {
		got := opcionesDeLaRonda(c.ronda)
		if len(got) != len(c.opciones) {
			t.Errorf("ronda %d (%s): %d opciones, esperaba %d — %v", c.ronda, c.nota, len(got), len(c.opciones), got)
			continue
		}
		for i := range got {
			if got[i] != c.opciones[i] {
				t.Errorf("ronda %d (%s): opciones %v, esperaba %v", c.ronda, c.nota, got, c.opciones)
				break
			}
		}
	}
}

// En la última ronda NO puede seguir apareciendo "Esperar": es justo lo que no queremos
// hacerle a alguien que ya esperó media hora.
func TestEnLaUltimaRondaYaNoSeOfreceEsperar(t *testing.T) {
	for _, op := range opcionesDeLaRonda(rondasConEspera) {
		if op == "Esperar" {
			t.Errorf("en la ronda final se le sigue ofreciendo esperar: %v", opcionesDeLaRonda(rondasConEspera))
		}
	}
}

// El texto de la última ronda PIDE DISCULPAS. El cliente esperó media hora por algo que no
// llegó; el mismo mensaje neutro de la primera ronda se leería como indiferencia.
func TestLaUltimaRondaSeDisculpa(t *testing.T) {
	// Las variantes van completas: afirmaSecuencia compara PALABRAS enteras, así que "disculpa"
	// no casa con "disculpas" (se descubrió al escribir esto: el test fallaba con el texto
	// correcto delante).
	ultimo := cuerpoDeLaRonda(rondasConEspera, 15)
	if !afirmaSecuencia(ultimo, [][]string{{"disculpa"}, {"disculpas"}, {"lamentamos"}, {"sentimos"}}, 2) {
		t.Errorf("la ronda final no se disculpa con quien esperó media hora:\n%q", ultimo)
	}
	// Y las primeras NO se disculpan: todavía no hay nada que lamentar y sonaría a que ya
	// fracasamos antes de empezar.
	primera := cuerpoDeLaRonda(0, 15)
	if afirmaSecuencia(primera, [][]string{{"disculpa"}, {"disculpas"}, {"lamentamos"}}, 2) {
		t.Errorf("la primera ronda se disculpa antes de tiempo:\n%q", primera)
	}
}

// Los minutos que se le DICEN al cliente son los que de verdad va a esperar. Prometer "5
// minutos" y tardar 15 es peor que decir 15: el cliente cuenta el tiempo.
// EL CLIENTE NO VE LOS MINUTOS. BOT_ESPERA_RONDA_MIN es un parámetro interno nuestro, no una
// promesa al cliente (decisión 25-sep: "al cliente solo debemos decir que está buscando"). El
// mensaje NO debe contener el número de minutos, sea cual sea el valor configurado.
func TestElMensajeNoDiceLosMinutosAlCliente(t *testing.T) {
	for _, min := range []int{5, 15, 20, 30} {
		cuerpo := cuerpoDeLaRonda(0, min)
		if afirmaSecuencia(cuerpo, [][]string{{itoaTest(min), "minutos"}}, 2) ||
			strings.Contains(cuerpo, itoaTest(min)) {
			t.Errorf("el mensaje de espera NO debe mostrarle los %d minutos al cliente:\n%q", min, cuerpo)
		}
	}
	// Pero SÍ debe decirle que se está buscando (no dejarlo sin contexto).
	cuerpo := cuerpoDeLaRonda(0, 15)
	if !strings.Contains(strings.ToLower(cuerpo), "buscando") {
		t.Errorf("el mensaje debe decir que se está buscando repartidor:\n%q", cuerpo)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// LA RONDA AVANZA. Sin esto, el contador se quedaría en cero y el cliente vería la primera
// pregunta para siempre — que es el bug de `PreguntoEspera` al revés.
func TestCadaOfrecimientoAvanzaLaRonda(t *testing.T) {
	const from = "593959545411"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}
	// Los menús no salen a WhatsApp en los tests.
	ag.enviarMenu = func(string, string, []string) error { return nil }

	for esperada := 0; esperada <= rondasConEspera; esperada++ {
		w, _ := store.GetPendingWait(from)
		if w.RondaEspera != esperada {
			t.Fatalf("antes del ofrecimiento %d la ronda era %d", esperada, w.RondaEspera)
		}
		ag.ofrecerEsperaAlCliente(from, nil)
		// El cliente contesta: sin esto la marca de "esperando respuesta" bloquea el siguiente
		// ofrecimiento —que es justo lo que debe hacer— y el test mediría otra cosa.
		ag.liberarRespuestaDeEspera(from)
		// Y PASA EL TIEMPO de la ronda. Desde el 25/09 el freno entre rondas es el reloj, no solo
		// la respuesta del cliente: contestar rápido ya no gasta las rondas (ver tocaOtraRonda).
		// Se retrocede el sello en vez de dormir 15 minutos reales.
		vieja, _ := store.GetPendingWait(from)
		vieja.UltimaRondaAt = time.Now().Add(-16 * time.Minute).Unix()
		store.SetPendingWait(from, vieja)
	}
	w, _ := store.GetPendingWait(from)
	if w.RondaEspera != rondasConEspera+1 {
		t.Errorf("tras %d ofrecimientos la ronda quedó en %d", rondasConEspera+1, w.RondaEspera)
	}
}

// Y que NO se le mande el mismo menú dos veces seguidas mientras no conteste: la búsqueda se
// consulta cada pocos segundos y sin la marca de "esperando respuesta" se le repetiría el menú
// sin parar. Ese era el propósito original de PreguntoEspera y no se puede perder al pasar a
// contador.
func TestNoSeRepiteElMenuMientrasNoConteste(t *testing.T) {
	const from = "593959545412"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}
	enviados := 0
	ag.enviarMenu = func(string, string, []string) error { enviados++; return nil }

	// Tres consultas seguidas al backend sin que el cliente conteste.
	ag.ofrecerEsperaAlCliente(from, nil)
	ag.ofrecerEsperaAlCliente(from, nil)
	ag.ofrecerEsperaAlCliente(from, nil)

	if enviados != 1 {
		t.Errorf("se le mandó el menú %d veces sin que contestara; debía ser 1", enviados)
	}
}

// Al aceptar "Esperar" se limpia la marca de "esperando respuesta", para que la SIGUIENTE ronda
// pueda preguntarle otra vez. Sin esto el cliente acepta una vez y nunca más se le consulta:
// exactamente lo que le pasó a Carlos.
func TestAceptarEsperarHabilitaLaSiguienteRonda(t *testing.T) {
	const from = "593959545413"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}
	ag.enviarMenu = func(string, string, []string) error { return nil }

	ag.ofrecerEsperaAlCliente(from, nil) // ronda 0
	if w, _ := store.GetPendingWait(from); !w.EsperandoRespuesta {
		t.Fatal("tras ofrecer el menú no quedó marcado que se espera respuesta")
	}

	ag.ResponderMenuEspera(from, "Esperar")

	w, ok := store.GetPendingWait(from)
	if !ok {
		t.Fatal("al aceptar esperar se borró la espera")
	}
	if w.EsperandoRespuesta {
		t.Error("tras aceptar esperar sigue marcado 'esperando respuesta': la siguiente ronda " +
			"nunca le preguntaría y lo perderíamos como a Carlos")
	}
}

// EL HORARIO NO CORTA UNA ESPERA YA EMPEZADA (regla acordada el 23/09).
//
// Si el pedido entró ANTES de la hora de cierre, se le sigue dando gestión aunque las rondas
// crucen el cierre: el cliente hizo todo bien y a tiempo. La guarda de horario vive donde
// corresponde —al REGISTRAR un pedido nuevo (pedido.go), donde impide tomar uno que nadie va a
// poder entregar—, y no en el flujo de espera.
//
// Este test fija esa frontera: si alguien la mueve y mete el horario en la espera, un cliente
// que pidió a las 18:55 se quedaría sin respuesta a las 19:01, después de haber esperado.
func TestElHorarioNoCortaUnaEsperaYaEmpezada(t *testing.T) {
	const from = "593959545414"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	// Horario CERRADO: son las 00:00-00:01, así que "ahora" queda fuera con seguridad.
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "00:01", EsperaRonda: 15 * time.Minute}
	enviados := 0
	ag.enviarMenu = func(string, string, []string) error { enviados++; return nil }

	ag.ofrecerEsperaAlCliente(from, nil)

	if enviados != 1 {
		t.Error("fuera de horario NO se le ofreció seguir con la espera: el cliente que pidió " +
			"antes del cierre se quedaría esperando sin respuesta")
	}
	if w, _ := store.GetPendingWait(from); w.RondaEspera != 1 {
		t.Error("la ronda no avanzó fuera de horario")
	}
}

// LA VARIABLE DE ENTORNO LLEGA AL MENSAJE. Es lo único que hace configurable la espera: si el
// mensaje se arma con un número fijo, cambiar BOT_ESPERA_RONDA_MIN no sirve de nada y encima el
// cliente oye un plazo distinto del real.
// El mensaje que sale por WhatsApp (a través de ofrecerEsperaAlCliente) NO expone los minutos
// configurados: al cliente solo se le dice que se está buscando y se le pregunta si quiere esperar.
func TestElMensajeQueSaleNoExponeLosMinutos(t *testing.T) {
	const from = "593959545415"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 20 * time.Minute}
	var cuerpo string
	ag.enviarMenu = func(_ string, c string, _ []string) error { cuerpo = c; return nil }

	ag.ofrecerEsperaAlCliente(from, nil)

	if strings.Contains(cuerpo, "20") || strings.Contains(strings.ToLower(cuerpo), "minutos") {
		t.Errorf("el mensaje al cliente NO debe exponer los minutos de espera:\n%q", cuerpo)
	}
	if !strings.Contains(strings.ToLower(cuerpo), "buscando") {
		t.Errorf("el mensaje debe decir que se está buscando:\n%q", cuerpo)
	}
}

// Aunque el backend mande un plazo (espera_segundos), ese número tampoco se le muestra al cliente:
// es un parámetro interno. El backend sigue mandando su plazo para la lógica de la espera; solo
// que ya no se refleja en el texto.
func TestElPlazoDelBackendTampocoSeMuestra(t *testing.T) {
	const from = "593959545416"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}
	var cuerpo string
	ag.enviarMenu = func(_ string, c string, _ []string) error { cuerpo = c; return nil }

	ag.ofrecerEsperaAlCliente(from, &georoutes.BusquedaResult{EsperaSegundos: 600}) // 10 min

	if strings.Contains(cuerpo, "10") || strings.Contains(strings.ToLower(cuerpo), "minutos") {
		t.Errorf("el plazo del backend NO debe mostrarse al cliente:\n%q", cuerpo)
	}
}

// "Reprogramar" es el botón de la ÚLTIMA ronda: tiene que entrar al flujo de agendado, no caer
// al modelo. Si no lo reconoce el interceptor, el cliente toca el botón y el bot le contesta
// cualquier cosa — justo en el momento más delicado, tras media hora de espera.
func TestElBotonReprogramarEntraAlFlujoDeAgendado(t *testing.T) {
	const from = "593959545417"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	store.SetLocation(from, -2.9, -79.0)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}
	ag.enviarMenu = func(string, string, []string) error { return nil }

	// El botón exacto de la última ronda (ver opcionesDeLaRonda).
	_, manejado := ag.ResponderMenuEspera(from, "Reprogramar")
	if !manejado {
		t.Error("el botón 'Reprogramar' no lo reconoce el interceptor: caería al modelo y el " +
			"cliente recibiría cualquier respuesta después de media hora esperando")
	}
}

// A QUIEN YA ESPERÓ SE LE DESPIDE DISTINTO. Si cancela en la última ronda no se arrepintió: se
// quedó sin gas por algo nuestro. Volver a ofrecerle agendar ahí suena a insistencia después de
// media hora perdida; lo que corresponde es agradecerle la paciencia y dejar la puerta abierta.
func TestAlCancelarTrasEsperarSeLeDespideConAmabilidad(t *testing.T) {
	const from = "593959545418"
	store := conversation.NewMemStore()
	w := esperaDePrueba()
	w.RondaEspera = rondasConEspera + 1 // ya pasó por todas las rondas
	store.SetPendingWait(from, w)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 15 * time.Minute}

	reply, manejado := ag.ResponderMenuEspera(from, "Cancelar")
	if !manejado {
		t.Fatal("el botón 'Cancelar' de la última ronda no lo reconoce el interceptor")
	}
	if !afirmaSecuencia(reply, [][]string{{"paciencia"}, {"lamentamos"}}, 3) {
		t.Errorf("a quien esperó media hora se le despidió sin reconocerlo:\n%q", reply)
	}
	// Y NO se le insiste con agendar: ya dijo que no.
	if afirmaSecuencia(reply, [][]string{{"a", "que", "hora"}, {"agendarte"}}, 3) {
		t.Errorf("se le vuelve a ofrecer agendar a quien acaba de desistir:\n%q", reply)
	}
}

// En cambio, quien cancela en la PRIMERA ronda sí recibe la oferta de agendar: no esperó nada,
// y ofrecerle una hora es ayudarlo, no insistirle.
func TestAlCancelarEnLaPrimeraRondaSiSeLeOfreceAgendar(t *testing.T) {
	const from = "593959545419"
	store := conversation.NewMemStore()
	w := esperaDePrueba()
	w.RondaEspera = 1 // se le preguntó una vez y dijo que no
	store.SetPendingWait(from, w)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00", EsperaRonda: 15 * time.Minute}

	reply, manejado := ag.ResponderMenuEspera(from, "Cancelar")
	if !manejado {
		t.Fatal("no se manejó el cancelar de la primera ronda")
	}
	if !afirmaSecuencia(reply, [][]string{{"agendarte"}, {"a", "que", "hora"}}, 4) {
		t.Errorf("a quien canceló sin esperar no se le ofreció agendar:\n%q", reply)
	}
}

// LA DURACIÓN DE LA RONDA sale de la configuración, no de un número escrito en el código. Era
// `5 * time.Minute` dentro de una goroutine —imposible de comprobar desde fuera— y es el número
// exacto que le costó el pedido a Carlos.
func TestLaDuracionDeLaRondaSaleDeLaConfiguracion(t *testing.T) {
	if got := duracionDeLaRonda(config.Config{EsperaRonda: 20 * time.Minute}); got != 20*time.Minute {
		t.Errorf("con BOT_ESPERA_RONDA_MIN=20 la ronda duró %v", got)
	}
	// Sin configurar (un .env incompleto) cae al default, NO a cero: una espera de cero
	// segundos despediría al cliente en el acto.
	if got := duracionDeLaRonda(config.Config{}); got != 10*time.Minute {
		t.Errorf("sin configurar la ronda duró %v; debía caer al default de 10 min", got)
	}
	// Y el default NO puede ser el viejo valor de 5 minutos.
	if duracionDeLaRonda(config.Config{}) <= 5*time.Minute {
		t.Error("el default volvió a ser 5 minutos o menos: es lo que perdió a Carlos")
	}
}

// Y que el camino SIN búsqueda del backend (BOT_USAR_BUSQUEDA=false) la use de verdad. El plazo
// se calcula dentro de una goroutine, así que no hay forma de observarlo desde un test; se
// verifica sobre el código, quitando los comentarios para que el nombre no se encuentre dentro
// de su propia explicación (pasó antes en este repo, ver guardarcontacto_test.go).
func TestElCaminoSinBackendUsaLaRondaConfigurada(t *testing.T) {
	src := codigoSinComentariosDelAgente(t, "pedido.go")
	if !strings.Contains(src, "duracionDeLaRonda(cfg)") {
		t.Error("el camino sin búsqueda del backend no usa duracionDeLaRonda: tendría el plazo " +
			"escrito en el código y BOT_ESPERA_RONDA_MIN no lo cambiaría")
	}
	// Y que no haya quedado el valor viejo suelto en ese archivo.
	if strings.Contains(src, "time.Now().Add(5 * time.Minute)") {
		t.Error("volvió a aparecer la espera de 5 minutos escrita en el código")
	}
}

// codigoSinComentariosDelAgente lee un archivo del paquete quitando las líneas de comentario.
func codigoSinComentariosDelAgente(t *testing.T, archivo string) string {
	t.Helper()
	crudo, err := os.ReadFile(archivo)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", archivo, err)
	}
	var b strings.Builder
	for _, linea := range strings.Split(string(crudo), "\n") {
		if strings.HasPrefix(strings.TrimSpace(linea), "//") {
			continue
		}
		b.WriteString(linea)
		b.WriteString("\n")
	}
	return b.String()
}

// EL TOPE DEL BOT NO PUEDE COMPETIR CON EL PLAZO DEL BACKEND.
//
// topeBusqueda es una red de seguridad: existe por si el backend quedara devolviendo "buscando"
// para siempre. No es un plazo del negocio y no debe cortarle la conversación a nadie.
//
// Estaba en 30 min, que es EXACTAMENTE lo que ahora busca el backend
// (BUSQUEDA_ESPERA_SEGUNDOS=1800). Los dos relojes vencían a la vez: si ganaba el del bot, el
// cliente recibía "no hay repartidor, intenta más tarde" en vez de la última ronda con
// disculpas y [Reprogramar / Cancelar] — perdiendo la venta que todo este trabajo buscaba
// rescatar.
//
// La red de seguridad tiene que estar CLARAMENTE por encima del total del negocio.
func TestElTopeDelBotEstaPorEncimaDelPlazoDelNegocio(t *testing.T) {
	// El total que puede durar la conversación: una ronda por cada pregunta que se le hace.
	totalNegocio := duracionDeLaRonda(config.Config{}) * time.Duration(rondasConEspera+1)

	if topeBusqueda <= totalNegocio {
		t.Errorf("el tope del bot (%v) no supera el total del flujo (%v): los dos relojes "+
			"compiten y el cliente puede recibir el corte seco del bot en vez de la última "+
			"ronda con disculpas", topeBusqueda, totalNegocio)
	}
	// Y con margen de verdad, no por un minuto: el backend tiene su propio barrido cada minuto
	// y las consultas del bot van cada 7 s, así que la última ronda puede salir algo después
	// del plazo exacto.
	if topeBusqueda < totalNegocio+10*time.Minute {
		t.Errorf("el tope del bot (%v) queda muy justo sobre el total (%v): un retraso normal "+
			"del backend bastaría para que el bot corte antes", topeBusqueda, totalNegocio)
	}
}
