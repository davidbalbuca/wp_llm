package agent

import (
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// primerNumero lee la cantidad de un mensaje de chat, sin confundirla con una cédula o teléfono.
func TestPrimerNumero(t *testing.T) {
	casos := []struct {
		in   string
		want int
	}{
		{"2", 2},
		{"quiero 3 porfa", 3},
		{"1 cilindro", 1},
		{"dame 10", 10},
		{"0105888887", -1}, // cédula: no es una cantidad
		{"sin números", -1},
		{"Amarillo", -1},
	}
	for _, c := range casos {
		if got := primerNumero(c.in); got != c.want {
			t.Errorf("primerNumero(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// afirmaCancelado detecta que el bot le dijo al cliente que su pedido YA se canceló (el caso de
// David: "he cancelado tu pedido" sin llamar a la herramienta), sin saltar con "¿quieres cancelar?".
func TestAfirmaCancelado(t *testing.T) {
	cancelado := []string{
		"Entendido, David. He cancelado tu pedido 🙏",
		"Listo, tu pedido fue cancelado.",
		"Ya cancelé tu pedido, cuando quieras hacemos otro.",
		"Tu pedido quedó cancelado con éxito.",
	}
	for _, m := range cancelado {
		if !afirmaCancelado(m) {
			t.Errorf("NO detectó cancelación afirmada: %q", m)
		}
	}
	legitimos := []string{
		"¿Quieres cancelar tu pedido?",
		"Si deseas cancelar, dímelo.",
		"Tu pedido está confirmado y en camino 🚚.",
		"¿En qué te puedo ayudar?",
	}
	for _, m := range legitimos {
		if afirmaCancelado(m) {
			t.Errorf("FALSO POSITIVO en cancelación: %q", m)
		}
	}
}

// inferirPedido reconstruye color y cantidad de la conversación cuando el modelo se saltó la
// herramienta. Es el corazón del rescate: el caso de Angel (Azul + 3) y el de David (Amarillo + 1).
func TestInferirPedido(t *testing.T) {
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{
			{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
				{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"},
				{ID: 12, Nombre: "AZUL"}, {ID: 13, Nombre: "NARANJA"},
			}},
		},
	})

	casos := []struct {
		nombre    string
		conv      [][2]string // {role, content}
		wantColor string
		wantCant  int
		wantOk    bool
	}{
		{"Angel: Azul + 3", [][2]string{
			{"user", "Azul"}, {"model", "¿cuántos?"}, {"user", "3"},
			{"user", "📍 ubicación: -2.9, -79.0"},
		}, "AZUL", 3, true},

		{"David: Amarillo + 1", [][2]string{
			{"user", "Cambiar pedido"}, {"user", "Amarillo"}, {"user", "1"},
		}, "AMARILLO", 1, true},

		{"cambió de opinión: gana el último color", [][2]string{
			{"user", "Blanco"}, {"user", "mejor Amarillo"}, {"user", "2"},
		}, "AMARILLO", 2, true},

		{"color pero sin cantidad: no inventa", [][2]string{
			{"user", "Azul"}, {"model", "¿cuántos?"},
		}, "", 0, false},

		{"sin color válido: no infiere", [][2]string{
			{"user", "quiero gas"}, {"user", "2"},
		}, "", 0, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			store := conversation.NewMemStore()
			for _, m := range c.conv {
				store.LogMessage("593999", m[0], m[1])
			}
			a := &Agent{store: store, catalog: cat}
			color, cant, ok := a.inferirPedido("593999")
			if ok != c.wantOk {
				t.Fatalf("ok=%v, want %v (color=%q cant=%d)", ok, c.wantOk, color, cant)
			}
			if ok && (color != c.wantColor || cant != c.wantCant) {
				t.Errorf("got (%q, %d), want (%q, %d)", color, cant, c.wantColor, c.wantCant)
			}
		})
	}
}
