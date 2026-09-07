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

	// Se ejercita el flujo REAL: los mensajes del cliente se anotan en la ficha (anotarDelMensaje,
	// lo que hace HandleMessage en cada turno) y el candado la lee. Antes el candado releía el
	// historial y adivinaba; ahora lee lo que se guardó cuando el cliente lo dijo.
	casos := []struct {
		nombre    string
		mensajes  []string // lo que el cliente fue escribiendo, en orden
		wantColor string
		wantCant  int
		wantOk    bool
	}{
		{"Angel: Azul + 3", []string{"Azul", "3"}, "AZUL", 3, true},
		{"David: Amarillo + 1", []string{"Cambiar pedido", "Amarillo", "1"}, "AMARILLO", 1, true},
		{"cambió de opinión: gana el último color", []string{"Blanco", "2", "mejor Amarillo"}, "AMARILLO", 2, true},
		{"color pero sin cantidad: no inventa", []string{"Azul"}, "", 0, false},
		{"sin color válido: no infiere", []string{"quiero gas", "2"}, "", 0, false},
		{"un número antes del color no es la cantidad", []string{"somos 4 en la casa", "Blanco"}, "", 0, false},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			store := conversation.NewMemStore()
			a := &Agent{store: store, catalog: cat}
			for _, m := range c.mensajes {
				a.anotarDelMensaje("593999", m)
			}
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

// Un pedido ya registrado limpia la ficha: el "gracias" siguiente no puede arrastrar el color
// y la cantidad de un pedido que ya va en camino (o el candado lo registraría otra vez).
func TestFichaSeLimpiaAlRegistrar(t *testing.T) {
	store := conversation.NewMemStore()
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
	})
	a := &Agent{store: store, catalog: cat}
	a.anotarDelMensaje("593999", "Blanco")
	a.anotarDelMensaje("593999", "2")
	if _, _, ok := a.inferirPedido("593999"); !ok {
		t.Fatal("la ficha debía tener color y cantidad")
	}

	store.ClearPedidoEnCurso("593999") // lo que hace registrarPedido tras el éxito

	if _, _, ok := a.inferirPedido("593999"); ok {
		t.Error("la ficha sobrevivió al registro: un mensaje posterior podría re-registrar el pedido")
	}
}

// afirmaAvisoAlEquipo detecta la promesa de que una persona va a contactar al cliente. Sin
// candado, esa promesa no la cumple nadie: el cliente espera una llamada que no existe.
func TestAfirmaAvisoAlEquipo(t *testing.T) {
	promete := []string{
		"Ya avisé al equipo para que te contacte 🙏",
		"Listo, notifiqué al equipo.",
		"El equipo se pondrá en contacto contigo en breve.",
		"Te van a contactar enseguida.",
		"Alguien te escribirá para ayudarte.",
	}
	for _, txt := range promete {
		if !afirmaAvisoAlEquipo(txt) {
			t.Errorf("no detectó una promesa de contacto: %q", txt)
		}
	}

	// Lo que NO es una promesa cumplida por un humano: ofrecer, preguntar, o hablar del repartidor.
	noPromete := []string{
		"¿Quieres que avise al equipo?",
		"Tu repartidor te llamará cuando esté llegando.",
		"¿Te contacto con una persona del equipo?",
		"Cualquier cosa, aquí estoy 😊",
		"",
	}
	for _, txt := range noPromete {
		if afirmaAvisoAlEquipo(txt) {
			t.Errorf("falso positivo, esto no promete contacto de una persona: %q", txt)
		}
	}
}

// afirmaProgramado detecta la programación fantasma: el modelo diciendo "te dejé agendada tu
// entrega" sin haber llamado a programar_entrega. Sin este detector el rescate de programación
// era inalcanzable, porque afirmaPedidoConfirmado no reconoce ninguna forma de agendado.
func TestAfirmaProgramado(t *testing.T) {
	agenda := []string{
		"¡Listo! Te dejé agendada tu entrega para las 18:30 📅",
		"Tu entrega quedó programada para las 18:30",
		"Ya agendé tu pedido para mañana a las 9",
		"Tu entrega quedó agendada, te escribo a esa hora",
	}
	for _, txt := range agenda {
		if !afirmaProgramado(txt) {
			t.Errorf("no detectó una programación afirmada: %q", txt)
		}
		// Y el candado del fantasma tiene que poder verlas (antes ninguna llegaba).
		if afirmaPedidoConfirmado(txt) {
			t.Logf("nota: %q además cae en afirmaPedidoConfirmado", txt)
		}
	}

	noAgenda := []string{
		"¿A qué hora te gustaría recibirlo?",
		"Atendemos de 07:00 a 19:00, dime qué hora prefieres",
		"¿Quieres que te la agende para mañana?",
		"",
	}
	for _, txt := range noAgenda {
		if afirmaProgramado(txt) {
			t.Errorf("falso positivo, esto no afirma una programación: %q", txt)
		}
	}
}
