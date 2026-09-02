package agent

import (
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El caso que motivó el candado: el 02/09 un cliente compartió su ubicación a las 22:49 y el bot
// le agendó una entrega para las 06:00 que él nunca pidió. Como era cliente conocido, todas las
// demás validaciones pasaban (cédula y nombre del perfil, cantidad y color del menú, ubicación
// recién compartida) y la hora se la inventó el modelo.
func TestClienteMencionoHora(t *testing.T) {
	casos := []struct {
		nombre   string
		escribio []string // lo que dijo el CLIENTE
		hora     string   // lo que el modelo quiere agendar
		want     bool
	}{
		{"el caso de David: nunca dijo una hora", []string{
			"Blanco", "1 cilindro", "He compartido mi ubicación actual.",
		}, "06:00", false},

		{"la escribió en formato HH:MM", []string{"quiero a las 14:30"}, "14:30", true},
		{"la dijo en formato 12h", []string{"me lo traes a las 3 pm?"}, "15:00", true},
		{"la dijo sin minutos", []string{"a las 9 por favor"}, "09:00", true},
		{"con la h pegada", []string{"mañana 8h esta bien"}, "08:00", true},

		{"dijo OTRA hora distinta", []string{"a las 10 am"}, "16:00", false},
		{"sin mensajes del cliente", nil, "10:00", false},
		{"hora inválida no se verifica", []string{"a las 10"}, "", false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			store := conversation.NewMemStore()
			for _, m := range c.escribio {
				store.LogMessage("593999", "user", m)
			}
			// El modelo hablando NO cuenta como que el cliente pidió esa hora.
			store.LogMessage("593999", "model", "¿Te la programo para las 06:00?")

			a := &Agent{store: store}
			if got := a.clienteMencionoHora("593999", c.hora); got != c.want {
				t.Errorf("clienteMencionoHora(%q) = %v, want %v", c.hora, got, c.want)
			}
		})
	}
}
