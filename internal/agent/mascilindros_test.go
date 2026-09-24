package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CANTIDAD: el menú tiene que dejar pedir más de tres cilindros.
//
// Pedido del dueño (24-sep): lista desplegable, y para más de 3 que el cliente escriba el número.
//
// El menú era [1 / 2 / 3] —tres botones tappables, el máximo de WhatsApp—, así que quien
// necesitaba 6 tenía que adivinar que podía escribirlo: nada en el chat se lo decía. En
// producción hay dos pedidos de 6 cilindros, de gente que lo escribió por su cuenta.

// El menú de cantidad ofrece la salida y, al tener 4 opciones, WhatsApp lo manda como LISTA
// desplegable en vez de botones (ver buildInteractiveMenu: >3 => list).
func TestElMenuDeCantidadOfreceLaSalidaYEsLista(t *testing.T) {
	opciones := cantidadesSugeridas()
	if !contieneLaOpcionDeMas(opciones) {
		t.Fatalf("el menú de cantidad no ofrece salida para más de 3: %v", opciones)
	}
	if len(opciones) <= 3 {
		t.Errorf("con %d opciones WhatsApp manda BOTONES, no lista desplegable: %v",
			len(opciones), opciones)
	}
	// Y los sugeridos de siempre siguen ahí: cubren el 95% de los pedidos (1→67, 2→23, 3→5).
	for _, esperado := range []string{"1", "2", "3"} {
		var hay bool
		for _, o := range opciones {
			if o == esperado {
				hay = true
			}
		}
		if !hay {
			t.Errorf("falta la cantidad %q en el menú: %v", esperado, opciones)
		}
	}
}

// EL BUG QUE ESTA OPCIÓN INTRODUCE SI NADIE LO VIGILA: "Más de 3" lleva un 3 dentro, y el
// parseo de cantidades lee números sueltos de mensajes cortos. Sin la salida explícita, el
// cliente que toca la opción queriendo SEIS se queda con TRES anotados — lo contrario exacto de
// lo que pidió, y sin que nada falle a la vista.
func TestElTresDeLaOpcionNoSeLeeComoCantidad(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	const from = "593999888777"

	ag.anotarDelMensaje(from, "BLANCO")
	ag.anotarDelMensaje(from, BotonMasCilindros)

	p, _ := store.GetPedidoEnCurso(from)
	if p.Cantidad == 3 {
		t.Fatalf("el 3 de %q se anotó como la cantidad: el cliente pidió MÁS de 3", BotonMasCilindros)
	}
	if p.Cantidad != 0 {
		t.Errorf("la cantidad debería seguir vacía (falta que la escriba), y es %d", p.Cantidad)
	}
	// El color NO se pierde: la opción responde a "¿cuántos?", el color ya estaba elegido.
	if p.Color != "BLANCO" {
		t.Errorf("se perdió el color al elegir la opción: %q", p.Color)
	}
}

// Al tocarla, se le pide el número.
func TestAlElegirMasCilindrosSeLePideElNumero(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	const from = "593999888778"
	ag.anotarDelMensaje(from, "BLANCO")

	reply, manejado := ag.ResponderMasCilindros(from, BotonMasCilindros)
	if !manejado {
		t.Fatal("la opción del menú no se resolvió en código")
	}
	bajo := strings.ToLower(reply)
	if !strings.Contains(bajo, "cuantos") && !strings.Contains(bajo, "cuántos") {
		t.Errorf("no se le pide el número de cilindros: %q", reply)
	}
}

// Y el número que escriba DESPUÉS se anota, sin interceptor nuevo: es el mismo camino que si lo
// hubiera escrito sin pasar por el menú. Este test lo comprueba de punta a punta, que es lo que
// de verdad importa: que el cliente que quiere 6 acabe con 6.
func TestElNumeroEscritoDespuesSeAnota(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	const from = "593999888779"

	ag.anotarDelMensaje(from, "BLANCO")
	ag.anotarDelMensaje(from, BotonMasCilindros)
	ag.anotarDelMensaje(from, "6")

	p, _ := store.GetPedidoEnCurso(from)
	if p.Cantidad != 6 {
		t.Errorf("el cliente pidió 6 y quedaron %d", p.Cantidad)
	}
}

// Y también en palabras ("seis"), que es como lo escribe bastante gente.
func TestLaCantidadEnPalabrasTambienSeAnota(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	const from = "593999888780"

	ag.anotarDelMensaje(from, "BLANCO")
	ag.anotarDelMensaje(from, BotonMasCilindros)
	ag.anotarDelMensaje(from, "seis cilindros")

	p, _ := store.GetPedidoEnCurso(from)
	if p.Cantidad != 6 {
		t.Errorf("\"seis cilindros\" no se leyó como 6: quedaron %d", p.Cantidad)
	}
}

// La opción NO hace nada si no se está armando un pedido: sin color elegido, la pregunta
// "¿cuántos?" no se ha hecho y contestarla sería una respuesta suelta sin contexto.
func TestSinPedidoEnCursoLaOpcionNoSeAtiende(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()

	if _, manejado := ag.ResponderMasCilindros("593999888781", BotonMasCilindros); manejado {
		t.Error("se atendió la opción sin pedido en curso")
	}
}

// Y no se mete con otros textos: solo con esa opción exacta.
func TestLaOpcionNoSeConfundeConOtrosMensajes(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	const from = "593999888782"
	ag.anotarDelMensaje(from, "BLANCO")

	for _, texto := range []string{"3", "quiero 3", "más gas", "3 blancos", "hola"} {
		if _, manejado := ag.ResponderMasCilindros(from, texto); manejado {
			t.Errorf("%q se tomó por la opción del menú", texto)
		}
	}
	// Y con la opción escrita a mano o con otra capitalización sí responde (el id del botón
	// vuelve tal cual, pero el cliente también puede teclearla).
	for _, texto := range []string{BotonMasCilindros, "mas de 3", "MÁS DE 3"} {
		if _, manejado := ag.ResponderMasCilindros(from, texto); !manejado {
			t.Errorf("no se reconoció %q como la opción del menú", texto)
		}
	}
}
