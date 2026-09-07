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

	// Igual que inferirPedido: se ejercita el flujo real (mensaje -> ficha -> candado). La hora
	// se guarda cuando el cliente la dice, no se rescata después del historial.
	t.Run("María Elena: dijo la hora de varias formas, vale la última", func(t *testing.T) {
		a := &Agent{store: conversation.NewMemStore(), catalog: cat, cfg: cfg}
		for _, m := range []string{"necesito un tanque", "Blanco", "1 cilindro", "a las 7 pm", "6h30", "si, hoy 18:30 pm"} {
			a.anotarDelMensaje("593979444899", m)
		}
		if h := a.horaPedidaPorCliente("593979444899"); h != "18:30" {
			t.Errorf("horaPedidaPorCliente = %q, want 18:30", h)
		}
		if !a.clienteQuiereProgramar("593979444899") {
			t.Error("debía reconocer que el cliente quiere programar")
		}
	})

	t.Run("pedido inmediato: no hay hora futura", func(t *testing.T) {
		a := &Agent{store: conversation.NewMemStore(), catalog: cat, cfg: cfg}
		for _, m := range []string{"quiero un gas", "Blanco", "1 cilindro"} {
			a.anotarDelMensaje("593888", m)
		}
		// "1 cilindro" -> extraerHora daría 01:00, pero está FUERA del horario 07-19 -> se descarta.
		if h := a.horaPedidaPorCliente("593888"); h != "" {
			t.Errorf("un pedido inmediato no debe tener hora; got %q", h)
		}
		if a.clienteQuiereProgramar("593888") {
			t.Error("un pedido inmediato NO es una programación")
		}
	})

	t.Run("la hora del cliente sobrevive a mensajes posteriores", func(t *testing.T) {
		// El caso del 05/09: la clienta dijo la hora y el bot se la volvió a preguntar tres
		// veces. Con la ficha, un "sí" o un "gracias" posterior no borra lo que ya dijo.
		a := &Agent{store: conversation.NewMemStore(), catalog: cat, cfg: cfg}
		for _, m := range []string{"Blanco", "2", "18:30", "sí", "gracias"} {
			a.anotarDelMensaje("593979444899", m)
		}
		if h := a.horaPedidaPorCliente("593979444899"); h != "18:30" {
			t.Errorf("la hora se perdió tras mensajes de charla: got %q, want 18:30", h)
		}
		// Y la cantidad tampoco se contamina con los números de la hora.
		if _, cant, _ := a.inferirPedido("593979444899"); cant != 2 {
			t.Errorf("la hora se leyó como cantidad: got %d, want 2", cant)
		}
	})

	t.Run("una hora fuera del horario no se guarda NI se vuelve cantidad", func(t *testing.T) {
		// "6h30" con horario 07:00-19:00 son las 06:30: no se puede atender. Pero tampoco son
		// 6 cilindros: el número venía de una hora. De explicárselo se encarga programarEntrega.
		a := &Agent{store: conversation.NewMemStore(), catalog: cat, cfg: cfg}
		for _, m := range []string{"Blanco", "2", "6h30"} {
			a.anotarDelMensaje("593777", m)
		}
		if h := a.horaPedidaPorCliente("593777"); h != "" {
			t.Errorf("se guardó una hora fuera del horario: %q", h)
		}
		if _, cant, _ := a.inferirPedido("593777"); cant != 2 {
			t.Errorf("el 6 de \"6h30\" se tomó como cantidad: got %d, want 2", cant)
		}
	})
}
