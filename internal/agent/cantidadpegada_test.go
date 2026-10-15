package agent

import (
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

func catalogoColores() []georoutes.Product {
	return []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
		{ID: 1, Nombre: "BLANCO"}, {ID: 2, Nombre: "AMARILLO"}, {ID: 3, Nombre: "NARANJA"}, {ID: 4, Nombre: "AZUL"},
	}}}
}

func TestCantidadesPegadasAlColor(t *testing.T) {
	casos := []struct {
		texto string
		want  map[string]int
	}{
		{"quiero 1 blanco y 1 amarillo", map[string]int{"BLANCO": 1, "AMARILLO": 1}},
		{"Hola buenas, me manda 2 cilindros de gas blanco y uno amarillo porfa", map[string]int{"BLANCO": 2, "AMARILLO": 1}},
		{"dos azules", map[string]int{"AZUL": 2}},
		{"quiero blanco", map[string]int{}},
		{"tengo 2 amarillos, me los cambian por blanco", map[string]int{}},
		{"cuánto cuestan 2 blancos?", map[string]int{}},
		{"van 4 días con este problema, el blanco no llega", map[string]int{}},
	}
	for _, c := range casos {
		got := cantidadesPegadasAlColor(catalogoColores(), c.texto)
		if len(got) != len(c.want) {
			t.Errorf("%q: %v, se esperaba %v", c.texto, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%q: %s=%d, se esperaba %d", c.texto, k, got[k], v)
			}
		}
	}
}

// CASO 04/10 (simulador): "quiero 1 blanco y 1 amarillo" dejaba los dos colores SIN cantidad y el
// bot preguntaba "¿cuántos?" justo después de decir "ya quedó anotado".
func TestMulticolorConCantidadesEnUnMensaje(t *testing.T) {
	const from = "593900950001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()

	ag.anotarDelMensaje(from, "quiero 1 blanco y 1 amarillo")

	p, _ := store.GetPedidoEnCurso(from)
	if !p.Completo() {
		t.Fatalf("la ficha debía quedar completa (1 BLANCO + 1 AMARILLO): %+v", p.Lineas())
	}
	if len(p.Lineas()) != 2 {
		t.Errorf("se esperaban 2 líneas: %+v", p.Lineas())
	}
}

func TestColoresEnPlural(t *testing.T) {
	got := coloresEnTexto(catalogoColores(), "mándame dos blancos y un azul")
	if len(got) != 2 || got[0] != "BLANCO" || got[1] != "AZUL" {
		t.Errorf("colores = %v", got)
	}
}

// CASO 04/10 (prod, Adrián): "Ahora compárteme tu ubicación por WhatsApp 📎 para enviarte el
// pedido.\n\n¿Me compartes tu ubicación 📎 para enviártelo?" — la petición de ubicación ya le
// pasa el turno, no hay que añadirle otra pregunta.
func TestPedirLaUbicacionYaDevuelveElTurno(t *testing.T) {
	if !devuelveElTurno("¡Dos cilindros de GAS 15KG Blanco, listo! 🙌 Ahora compárteme tu ubicación por WhatsApp 📎 para enviarte el pedido.") {
		t.Error("pedir la ubicación ya devuelve el turno: no hay que añadir otra pregunta")
	}
	if devuelveElTurno("Estoy revisando tu pedido, ya te aviso") {
		t.Error("una promesa colgada sigue sin devolver el turno")
	}
}
