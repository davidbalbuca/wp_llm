package agent

import (
	"testing"
	"time"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// EL ESCENARIO QUE PIDIÓ EL DUEÑO (14/10): "si se atiende de noche, y por lo general la noche
// seria desde las 7pm a 10pm, debemos asegurarnos de tener una sola fuente de verdad para cuando
// decimos que atendemos hasta las 10pm, porque si queda en otro lado va a quedar como que
// atendemos hasta las 7pm pero no es asi".
//
// Esto es exactamente ese caso, de punta a punta: el panel abre hasta las 22:00, el .env del bot
// sigue diciendo 19:00 (como está hoy en producción) y se comprueba que el bot atiende y anuncia
// las 22:00. Si alguien devuelve la fuente al .env, este test falla.
func TestAtenderHastaLas22SoloSeConfiguraEnElPanel(t *testing.T) {
	// Lo que el panel pondría al ampliar la atención: 07:00 a 22:00 los siete días.
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "22:00"), "07:00", "19:00")

	casos := []struct {
		hora    int
		minuto  int
		atiende bool
		porque  string
	}{
		{6, 30, false, "antes de abrir"},
		{7, 0, true, "la hora de apertura"},
		{18, 59, true, "antes del viejo cierre del .env"},
		{19, 0, true, "LA HORA CLAVE: el .env cerraba aquí, el panel sigue abierto"},
		{20, 0, true, "noche, dentro del nuevo horario"},
		{21, 59, true, "el último minuto"},
		{22, 0, false, "la hora de cierre ya no despacha al instante"},
		{23, 0, false, "después de cerrar"},
	}
	for _, c := range casos {
		cuando := unMiercoles(c.hora, c.minuto)
		if got := ag.dentroDeHorario(cuando); got != c.atiende {
			t.Errorf("a las %02d:%02d dentroDeHorario = %v, se esperaba %v (%s)",
				c.hora, c.minuto, got, c.atiende, c.porque)
		}
	}

	// Y lo que se le DICE al cliente: las 22:00, no las 19:00 del .env.
	desde, hasta := ag.horarioDeHoy(unMiercoles(20, 0))
	if desde != "07:00" || hasta != "22:00" {
		t.Errorf("el bot anuncia %s a %s; el panel dice 07:00 a 22:00", desde, hasta)
	}
}

// La otra mitad: con el horario SOLO en el .env (sin backend), ampliar a las 22:00 exigiría
// redesplegar. Este test documenta por qué el respaldo no sirve como fuente — a las 20:00 el bot
// cierra, aunque el negocio atienda.
func TestConSoloElEnvElBotSeQuedaEnLas19(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00",
		BotDiasLaborables: "1-6"}
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{}) // sin horario del backend

	if ag.dentroDeHorario(unMiercoles(20, 0)) {
		t.Error("con el respaldo del .env (hasta 19:00), las 20:00 tienen que quedar fuera")
	}
	// Esto es lo que pasaba ANTES en producción y es lo que el cambio evita: el negocio atiende
	// hasta las 22:00 y el bot dice 19:00 porque su valor vive en otro sitio.
	_, hasta := ag.horarioDeHoy(unMiercoles(20, 0))
	if hasta != "19:00" {
		t.Errorf("el respaldo tiene que decir 19:00, dijo %s", hasta)
	}
}

// El horario de la noche NO lo decide el bot: si el panel cierra a las 19:00, una franja nocturna
// no existe. Es la relación entre (B) y (D) de la spec, fijada en un test para que no se olvide:
// primero hay que poder ABRIR de noche; cobrar distinto es lo de después.
func TestSiElPanelCierraALas19NoHayNoche(t *testing.T) {
	ag := agenteConHorarioDelBackend(t, semanaCompleta("07:00", "19:00"), "07:00", "22:00")

	// El .env dice 22:00, pero manda el panel: a las 20:00 está cerrado.
	if ag.dentroDeHorario(unMiercoles(20, 0)) {
		t.Error("el panel cierra a las 19:00; a las 20:00 el bot no puede estar atendiendo " +
			"porque su .env diga 22:00")
	}
}

// Y el caso de borde del cambio de día: un horario que cierra a medianoche no puede dejar al bot
// atendiendo a las 00:30 del día siguiente (ese ya es otro día, con su propia fila).
func TestElHorarioNoSeDerramaAlDiaSiguiente(t *testing.T) {
	// Miércoles abierto hasta las 23:30; jueves (Python 3) CERRADO.
	dias := semanaCompleta("07:00", "23:30", 3)
	ag := agenteConHorarioDelBackend(t, dias, "07:00", "19:00")

	if !ag.dentroDeHorario(unMiercoles(23, 0)) {
		t.Error("el miércoles a las 23:00 el panel dice abierto")
	}
	jueves := time.Date(2026, 10, 15, 0, 30, 0, 0, zonaEcuador)
	if ag.dentroDeHorario(jueves) {
		t.Error("el jueves está cerrado: las 00:30 del jueves no heredan el horario del miércoles")
	}
}

var _ = georoutes.DiaHorario{}
