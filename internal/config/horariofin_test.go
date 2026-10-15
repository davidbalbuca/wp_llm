package config

import (
	"testing"
	"time"
)

func TestHorarioFinPorDia(t *testing.T) {
	c := Config{BotHorarioFin: "20:30", BotHorarioFinPorDia: "7=19:00, 6=20:00"}
	casos := map[time.Weekday]string{
		time.Sunday:   "19:00",
		time.Saturday: "20:00",
		time.Monday:   "20:30",
	}
	for dia, want := range casos {
		if got := c.HorarioFin(dia); got != want {
			t.Errorf("%v: HorarioFin = %q, se esperaba %q", dia, got, want)
		}
	}
}

// Una entrada mal escrita se ignora: no puede cerrar el negocio ni mover otro día.
func TestHorarioFinPorDiaMalEscrito(t *testing.T) {
	for _, mal := range []string{"", "basura", "7=25:00", "7=", "=19:00", "domingo=19:00"} {
		c := Config{BotHorarioFin: "20:30", BotHorarioFinPorDia: mal}
		if got := c.HorarioFin(time.Sunday); got != "20:30" {
			t.Errorf("%q: HorarioFin(domingo) = %q, se esperaba el general 20:30", mal, got)
		}
	}
}
