package agent

import (
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// extraerHora saca la hora de las formas en que la gente la escribe en un chat.
func TestExtraerHora(t *testing.T) {
	casos := []struct{ in, want string }{
		{"18:30", "18:30"},
		{"a las 7 pm", "19:00"},
		{"6:30 pm", "18:30"},
		{"quiero a las 15", "15:00"},
		{"6h30", "06:30"},
		{"18h", "18:00"},
		{"si, hoy 18:30 pm", "18:30"},
		{"1 cilindro", "01:00"}, // ojo: "1" parece hora — por eso horaPedidaPorCliente filtra por horario
		{"Blanco", ""},
		{"María Elena Castillo", ""},
	}
	for _, c := range casos {
		if got := extraerHora(c.in); got != c.want {
			t.Errorf("extraerHora(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// El caso de María Elena (05/09): pidió agendar "hoy 18:30", el modelo lo afirmó sin llamar
// programar_entrega. horaPedidaPorCliente debe recuperar esa hora (dentro del horario laboral) y
// clienteQuiereProgramar debe reconocer la intención — para que el candado fuerce programación,
// no un registro inmediato.
func TestHoraPedidaPorCliente(t *testing.T) {
	cfg := config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
	})

	t.Run("María Elena: pidió 18:30", func(t *testing.T) {
		s := conversation.NewMemStore()
		for _, m := range []string{"necesito un tanque", "Blanco", "1 cilindro", "a las 7 pm", "6h30", "si, hoy 18:30 pm"} {
			s.LogMessage("593979444899", "user", m)
		}
		a := &Agent{store: s, catalog: cat, cfg: cfg}
		if h := a.horaPedidaPorCliente("593979444899"); h != "18:30" {
			t.Errorf("horaPedidaPorCliente = %q, want 18:30", h)
		}
		if !a.clienteQuiereProgramar("593979444899") {
			t.Error("debía reconocer que el cliente quiere programar")
		}
	})

	t.Run("pedido inmediato: no hay hora futura", func(t *testing.T) {
		s := conversation.NewMemStore()
		for _, m := range []string{"quiero un gas", "Blanco", "1 cilindro"} {
			s.LogMessage("593888", "user", m)
		}
		a := &Agent{store: s, catalog: cat, cfg: cfg}
		// "1 cilindro" -> extraerHora daría 01:00, pero está FUERA del horario 07-19 -> se descarta.
		if h := a.horaPedidaPorCliente("593888"); h != "" {
			t.Errorf("un pedido inmediato no debe tener hora; got %q", h)
		}
		if a.clienteQuiereProgramar("593888") {
			t.Error("un pedido inmediato NO es una programación")
		}
	})
}
