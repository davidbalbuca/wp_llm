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

// EL HORARIO LO MANDA EL BACKEND, NO EL .env (specs/horarios-franjas-y-rubros.md §3).
//
// Lo que estos tests defienden es UNA SOLA FUENTE DE VERDAD. Hasta el 14/10 el bot decidía el
// horario con su .env y la app móvil con la tabla del backend, con valores distintos: un miércoles
// a las 22:00 la app dejaba agendar y el bot decía que estábamos cerrados. Y al ampliar la
// atención hasta las 22:00, el bot habría seguido diciendo 19:00 hasta que alguien recordara
// redesplegarlo — que es exactamente el riesgo que el dueño señaló.
//
// El test que de verdad importa es TestElBotDiceElHorarioDelPanelAunqueElEnvDigaOtroONo: pone el
// .env en 19:00 y el backend en 22:00, y exige que gane el backend. Si alguien revierte la fuente,
// ahí falla.

// agenteConHorarioDelBackend arma un agente cuyo catálogo trae el horario del backend, y cuyo
// .env dice OTRA COSA a propósito: así se ve quién gana.
func agenteConHorarioDelBackend(t *testing.T, dias []georoutes.DiaHorario,
	envInicio, envFin string) *Agent {
	t.Helper()
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg = config.Config{BotHorarioInicio: envInicio, BotHorarioFin: envFin,
		BotDiasLaborables: "1-6"}
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Horario: &georoutes.HorarioSemanal{Dias: dias},
	})
	return ag
}

// semanaCompleta arma los 7 días con el mismo horario. `cerrados` son los días (numeración de
// Python: lunes=0 ... domingo=6) que van cerrados.
func semanaCompleta(desde, hasta string, cerrados ...int) []georoutes.DiaHorario {
	cerrado := map[int]bool{}
	for _, d := range cerrados {
		cerrado[d] = true
	}
	nombres := []string{"Lunes", "Martes", "Miercoles", "Jueves", "Viernes", "Sabado", "Domingo"}
	var dias []georoutes.DiaHorario
	for i := 0; i < 7; i++ {
		d := georoutes.DiaHorario{Dia: i, Nombre: nombres[i], Abierto: !cerrado[i]}
		if d.Abierto {
			d.Desde, d.Hasta = desde, hasta
		}
		dias = append(dias, d)
	}
	return dias
}

// unMiercoles devuelve un miércoles a la hora pedida, para no depender del día que sea hoy.
func unMiercoles(hora, minuto int) time.Time {
	return time.Date(2026, 10, 14, hora, minuto, 0, 0, zonaEcuador) // el 14/10/2026 fue miércoles
}

// EL TEST QUE IMPORTA: el .env dice 19:00, el panel dice 22:00 y manda el panel.
//
// Es el caso que pidió el dueño: "si queda en otro lado va a quedar como que atendemos hasta las
// 7pm pero no es así". Con el horario viviendo en el .env del bot, ampliar la atención exigía un
// despliegue y, hasta entonces, el bot mentía.
func TestElBotDiceElHorarioDelPanelAunqueElEnvDigaOtro(t *testing.T) {
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "22:00"), "07:00", "19:00")

	desde, hasta := ag.horarioDeHoy(unMiercoles(12, 0))
	if desde != "07:00" || hasta != "22:00" {
		t.Errorf("el bot dice %s a %s; tiene que decir el horario del panel (07:00 a 22:00), no el "+
			"del .env (07:00 a 19:00)", desde, hasta)
	}

	// Y lo que de verdad decide: a las 21:00 se ATIENDE, porque el panel abre hasta las 22:00.
	if !ag.dentroDeHorario(unMiercoles(21, 0)) {
		t.Error("a las 21:00 el panel dice abierto (hasta 22:00) y el bot lo da por cerrado: " +
			"sigue mandando el .env")
	}
}

// La contradicción real que había en producción: el miércoles abierto hasta las 23:30 en el panel
// (la app dejaba agendar) y el bot cerrando a las 20:30 por su .env.
func TestNoSeRepiteLaContradiccionDelMiercolesALas22(t *testing.T) {
	// Tal como estaba dev el 14/10: miércoles y jueves 00:00-23:30, el resto 07:00-19:00.
	dias := semanaCompleta("07:00", "19:00")
	for _, i := range []int{2, 3} { // miércoles y jueves
		dias[i].Desde, dias[i].Hasta = "00:00", "23:30"
	}
	ag := agenteConHorarioDelBackend(t, dias, "07:00", "20:30")

	if !ag.dentroDeHorario(unMiercoles(22, 0)) {
		t.Error("el miércoles a las 22:00 el panel dice abierto y el bot dice cerrado: es la misma " +
			"contradicción que la app ya no tiene")
	}
}

// Un día cerrado en el panel cierra el bot, aunque su .env diga que ese día se trabaja.
func TestUnDiaCerradoEnElPanelCierraElBot(t *testing.T) {
	// Miércoles (Python 2) cerrado; el .env dice "1-6", o sea que el miércoles sí se trabaja.
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "19:00", 2), "07:00", "19:00")

	if ag.esDiaLaborable(unMiercoles(12, 0)) {
		t.Error("el panel tiene el miércoles cerrado y el bot lo da por laborable")
	}
	if ag.dentroDeHorario(unMiercoles(12, 0)) {
		t.Error("un día cerrado no puede estar dentro de horario")
	}
}

// Y el domingo abierto en el panel ABRE el bot, aunque su .env diga "1-6" (lunes a sábado). Es el
// otro sentido del mismo cambio: el panel manda tanto para cerrar como para abrir.
func TestUnDomingoAbiertoEnElPanelAbreElBot(t *testing.T) {
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "19:00"), "07:00", "19:00")
	domingo := time.Date(2026, 10, 18, 12, 0, 0, 0, zonaEcuador) // 18/10/2026 fue domingo

	if !ag.esDiaLaborable(domingo) {
		t.Error("el panel tiene el domingo abierto y el bot lo da por cerrado porque su .env dice 1-6")
	}
}

// RESPALDO: si el backend no dijo el horario, se usa el del .env. Un bot que no sabe el horario no
// puede quedarse mudo; lo que no puede es inventarse uno.
func TestSinHorarioDelBackendSeUsaElDelEnv(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg = config.Config{BotHorarioInicio: "08:00", BotHorarioFin: "18:00",
		BotDiasLaborables: "1-6"}
	// Catálogo SIN horario: es lo que pasa cuando GetHorarioAtencion falla.
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{})

	desde, hasta := ag.horarioDeHoy(unMiercoles(12, 0))
	if desde != "08:00" || hasta != "18:00" {
		t.Errorf("sin horario del backend se esperaba el del .env (08:00 a 18:00), se obtuvo %s a %s",
			desde, hasta)
	}
	if !ag.dentroDeHorario(unMiercoles(12, 0)) {
		t.Error("con el respaldo, el mediodía de un miércoles tiene que estar dentro de horario")
	}
	if ag.dentroDeHorario(unMiercoles(19, 0)) {
		t.Error("con el respaldo, las 19:00 están fuera del horario 08:00-18:00")
	}
}

// Horas ilegibles del backend ("abc"): se cae al respaldo en vez de tratarlas como 00:00, que
// dejaría el día cerrado entero sin que nadie lo hubiera pedido.
func TestHorasIlegiblesDelBackendCaenAlRespaldo(t *testing.T) {
	dias := semanaCompleta("07:00", "19:00")
	dias[2].Desde, dias[2].Hasta = "abc", "def" // miércoles corrupto
	ag := agenteConHorarioDelBackend(t, dias, "06:00", "20:00")

	desde, hasta := ag.horarioDeHoy(unMiercoles(12, 0))
	if desde != "06:00" || hasta != "20:00" {
		t.Errorf("con horas ilegibles se esperaba el respaldo (06:00 a 20:00), se obtuvo %s a %s",
			desde, hasta)
	}
}

// El texto de los días sale de los días ABIERTOS del panel, no del .env.
func TestElTextoDeLosDiasSaleDelPanel(t *testing.T) {
	casos := []struct {
		nombre   string
		cerrados []int
		esperado string
	}{
		{"toda la semana", nil, "lunes a domingo"},
		{"sin domingo", []int{6}, "lunes a sábado"},
		{"sin domingo ni sábado", []int{5, 6}, "lunes a viernes"},
		// Con un hueco en medio NO se puede decir "lunes a domingo": prometería el miércoles.
		{"con el miércoles cerrado", []int{2}, "lunes, martes, jueves, viernes, sábado, domingo"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "19:00", c.cerrados...),
				"07:00", "19:00")
			if got := ag.textoDiasAbiertos(unMiercoles(12, 0)); got != c.esperado {
				t.Errorf("textoDiasAbiertos = %q, se esperaba %q", got, c.esperado)
			}
		})
	}
}

// Un día cerrado no tiene horas que decir, así que se dan las del próximo día que abra: un domingo
// cerrado tiene que poder contestar "atendemos de 07:00 a 22:00", no un horario vacío.
func TestUnDiaCerradoDiceElHorarioDelProximoDiaAbierto(t *testing.T) {
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "22:00", 6), "07:00", "19:00")
	domingo := time.Date(2026, 10, 18, 12, 0, 0, 0, zonaEcuador)

	desde, hasta := ag.horarioDeHoy(domingo)
	if desde != "07:00" || hasta != "22:00" {
		t.Errorf("un domingo cerrado tiene que decir el horario de un día abierto (07:00 a 22:00), "+
			"se obtuvo %s a %s", desde, hasta)
	}
}

// El prompt que lee el modelo lleva el horario del PANEL. Es donde el modelo decide qué contestar,
// así que si aquí dijera 19:00 el bot le diría 19:00 al cliente aunque todo lo demás esté bien.
func TestElPromptLlevaElHorarioDelPanel(t *testing.T) {
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "22:00"), "07:00", "19:00")

	// El horario va en la parte VOLÁTIL del prompt (cambia cada mensaje), no en la fija.
	_, prompt := ag.construirSistema("593900000001")
	if !strings.Contains(prompt, "a 22:00") {
		t.Error("el prompt no lleva el horario del panel (22:00); el modelo anunciará el del .env")
	}
	if strings.Contains(prompt, "a 19:00") {
		t.Error("el prompt todavía lleva el 19:00 del .env")
	}
}
