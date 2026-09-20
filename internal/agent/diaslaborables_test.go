package agent

import (
	"testing"
	"time"

	"wp-llm-gas/internal/config"
)

// agenteConDias arma un agente con el horario y los días dados, sin tocar nada más.
func agenteConDias(dias string) *Agent {
	return &Agent{cfg: config.Config{
		BotHorarioInicio:  "07:00",
		BotHorarioFin:     "19:00",
		BotDiasLaborables: dias,
	}}
}

// El domingo 20/09/2026 seis clientes pidieron gas y no había repartidores: de ahí sale esto.
// Con "1-6" (lunes a sábado), el domingo NO es día laborable aunque la hora esté dentro.
func TestDiaLaborableYHorario(t *testing.T) {
	ag := agenteConDias("1-6")
	domingo := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	lunes := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	sabado := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)

	if ag.esDiaLaborable(domingo) {
		t.Error("el domingo NO debería ser laborable con 1-6")
	}
	if !ag.esDiaLaborable(lunes) || !ag.esDiaLaborable(sabado) {
		t.Error("lunes y sábado SÍ deberían ser laborables con 1-6")
	}
	// La hora está dentro del horario, pero el día manda.
	if ag.dentroDeHorario(domingo) {
		t.Error("el domingo a las 10:00 no se atiende: el día pesa más que la hora")
	}
	if !ag.dentroDeHorario(lunes) {
		t.Error("el lunes a las 10:00 sí se atiende")
	}
	// Y el día laborable fuera de hora sigue estando fuera.
	if ag.dentroDeHorario(time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)) {
		t.Error("el lunes a las 22:00 está fuera de horario")
	}
}

// El texto que se le dice al cliente sale de la configuración: si mañana se trabaja domingo, el
// mensaje tiene que cambiar solo.
func TestTextoDiasLaborables(t *testing.T) {
	casos := []struct {
		dias        string
		texto       string
		descripcion string
	}{
		{"1-6", "lunes a sábado", "lo normal"},
		{"1-5", "lunes a viernes", "sin sábados"},
		{"1-7", "lunes a domingo", "todos los días"},
		{"1,3,5", "lunes, miércoles, viernes", "días sueltos: la lista completa"},
		{"", "lunes a domingo", "configuración vacía: no se cierra el negocio"},
		{"basura", "lunes a domingo", "configuración inválida: tampoco"},
	}
	for _, c := range casos {
		ag := agenteConDias(c.dias)
		if got := ag.textoDiasLaborables(); got != c.texto {
			t.Errorf("%s: textoDiasLaborables(%q) = %q, want %q", c.descripcion, c.dias, got, c.texto)
		}
	}
}
