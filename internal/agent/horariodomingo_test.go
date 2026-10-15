package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
)

// HORARIO POR DÍA (04/10): el dueño pidió que el domingo se cierre a las 19:00 y el resto de días
// a las 20:30. Hasta entonces el bot solo tenía UNA hora de cierre para toda la semana.
func agenteDomingoCorto() *Agent {
	return &Agent{cfg: config.Config{
		BotHorarioInicio:    "07:00",
		BotHorarioFin:       "20:30",
		BotDiasLaborables:   "1-7",
		BotHorarioFinPorDia: "7=19:00",
	}}
}

var (
	domingo1930 = time.Date(2026, 10, 4, 19, 30, 0, 0, zonaEcuador)
	domingo1830 = time.Date(2026, 10, 4, 18, 30, 0, 0, zonaEcuador)
	lunes1930   = time.Date(2026, 10, 5, 19, 30, 0, 0, zonaEcuador)
)

func TestElDomingoCierraAntesQueLosDemasDias(t *testing.T) {
	ag := agenteDomingoCorto()
	if ag.dentroDeHorario(domingo1930) {
		t.Error("el domingo a las 19:30 ya no se atiende (cierra a las 19:00)")
	}
	if !ag.dentroDeHorario(domingo1830) {
		t.Error("el domingo a las 18:30 sí se atiende")
	}
	if !ag.dentroDeHorario(lunes1930) {
		t.Error("el lunes a las 19:30 sí se atiende (cierra a las 20:30)")
	}
}

// Sin la variable, todo sigue como antes: un solo cierre para toda la semana.
func TestSinHorarioPorDiaTodoIgualQueAntes(t *testing.T) {
	ag := agenteDomingoCorto()
	ag.cfg.BotHorarioFinPorDia = ""
	if !ag.dentroDeHorario(domingo1930) {
		t.Error("sin horario por día, el domingo a las 19:30 se atiende como cualquier día")
	}
	if got := ag.textoHorario(); got != "lunes a domingo de 07:00 a 20:30" {
		t.Errorf("textoHorario = %q", got)
	}
}

// Al cliente se le dice el horario completo, con el domingo aparte.
func TestTextoHorarioConDomingoAparte(t *testing.T) {
	if got := agenteDomingoCorto().textoHorario(); got != "lunes a sábado de 07:00 a 20:30 y domingo de 07:00 a 19:00" {
		t.Errorf("textoHorario = %q", got)
	}
}

// El menú de horas de un domingo por la tarde no ofrece horas pasadas las 19:00: salta a mañana,
// y el lunes sí puede ofrecer hasta las 20:00.
func TestHorasSugeridasRespetanElCierreDelDomingo(t *testing.T) {
	ag := agenteDomingoCorto()
	horas := ag.horasSugeridas(time.Date(2026, 10, 4, 17, 0, 0, 0, zonaEcuador))
	for _, h := range horas {
		if strings.HasPrefix(h, "hoy ") && h >= "hoy 19:00" {
			t.Errorf("se ofreció %q un domingo que cierra a las 19:00", h)
		}
	}
	if len(horas) == 0 || horas[0] != "hoy 18:00" {
		t.Errorf("horas = %v, se esperaba empezar por \"hoy 18:00\"", horas)
	}
	lunes := ag.horasSugeridas(time.Date(2026, 10, 5, 18, 0, 0, 0, zonaEcuador))
	if !contiene(lunes, "hoy 20:00") {
		t.Errorf("el lunes debería ofrecer las 20:00: %v", lunes)
	}
}

func TestCierreMasTarde(t *testing.T) {
	if got := agenteDomingoCorto().cierreMasTarde(); got != "20:30" {
		t.Errorf("cierreMasTarde = %q", got)
	}
}
