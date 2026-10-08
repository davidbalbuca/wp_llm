package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO 08/10 (Alicia): "AMARILLO", "Y blanco", "2", "1". El bot preguntó por los amarillos y el 2
// quedó en los blancos; "Y blanco" había PISADO el amarillo porque todavía no tenía cantidad. Luego
// "tengo un cilindro amarillo y uno blanco" no se leyó ("tengo" anulaba las cantidades) y el bot
// siguió preguntando "¿cuántos amarillos?" hasta después de recibir la ubicación.

func TestAlicia_YBlancoSumaAunqueElAmarilloNoTengaCantidad(t *testing.T) {
	const from = "593999200101"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "AMARILLO")
	ag.anotarDelMensaje(from, "Y blanco")

	p, _ := store.GetPedidoEnCurso(from)
	lineas := p.Lineas()
	if len(lineas) != 2 || lineas[0].Color != "AMARILLO" || lineas[1].Color != "BLANCO" {
		t.Fatalf("\"Y blanco\" es una suma, no un cambio: %+v", lineas)
	}
	// Se pregunta primero por el que nombró primero, y el prompt dice ESE.
	if l, _ := p.PrimeraSinCantidad(); l.Color != "AMARILLO" {
		t.Errorf("la primera pregunta debía ser por los amarillos, es por %q", l.Color)
	}
	_, vol := ag.construirSistema(from)
	if !strings.Contains(vol, "cantidad de cilindros AMARILLO") || strings.Contains(vol, "cantidad de cilindros BLANCO") {
		t.Errorf("FALTA debía nombrar solo los amarillos:\n%s", vol)
	}
}

// Con los DOS stores: en memoria el slice compartido escondía que la cantidad de una línea
// cerrada nunca se guardaba; con SQLite (producción) se perdía.
func storesFicha(t *testing.T) map[string]conversation.Store {
	sq, err := conversation.NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]conversation.Store{"memoria": conversation.NewMemStore(), "sqlite": sq}
}

func TestAlicia_CadaNumeroVaAlColorPorElQueSePregunta(t *testing.T) {
	for nombre, store := range storesFicha(t) {
		t.Run(nombre, func(t *testing.T) { aliciaCadaNumero(t, store) })
	}
}

func aliciaCadaNumero(t *testing.T, store conversation.Store) {
	const from = "593999200102"
	ag := agentIncidente(nil, store)

	nota := ""
	for _, m := range []string{"AMARILLO", "Y blanco", "2"} {
		nota = ag.anotarDelMensaje(from, m)
	}
	// Al modelo se le dice en ESTE turno a qué color fue el número.
	if !strings.Contains(nota, "cilindros AMARILLO") {
		t.Errorf("la nota del turno debía decir que el 2 son amarillos: %q", nota)
	}
	p, _ := store.GetPedidoEnCurso(from)
	if l, _ := p.PrimeraSinCantidad(); l.Color != "BLANCO" {
		t.Fatalf("con 2 amarillos, lo siguiente es cuántos blancos: %+v", p.Lineas())
	}

	ag.anotarDelMensaje(from, "1")
	p, _ = store.GetPedidoEnCurso(from)
	if !p.Completo() {
		t.Fatalf("la ficha debía quedar completa: %+v", p.Lineas())
	}
	got := map[string]int{}
	for _, l := range p.Lineas() {
		got[l.Color] = l.Cantidad
	}
	if got["AMARILLO"] != 2 || got["BLANCO"] != 1 {
		t.Errorf("quería 2 amarillos y 1 blanco, quedó %v", got)
	}
}

func TestAlicia_TengoUnAmarilloYUnoBlancoEsElPedido(t *testing.T) {
	for nombre, store := range storesFicha(t) {
		t.Run(nombre, func(t *testing.T) { aliciaTengo(t, store) })
	}
}

func aliciaTengo(t *testing.T, store conversation.Store) {
	const from = "593999200103"
	ag := agentIncidente(nil, store)

	for _, m := range []string{"AMARILLO", "Y blanco", "Aver tengo un cilindro amarillo y uno blanco"} {
		ag.anotarDelMensaje(from, m)
	}
	p, _ := store.GetPedidoEnCurso(from)
	got := map[string]int{}
	for _, l := range p.Lineas() {
		got[l.Color] = l.Cantidad
	}
	if !p.Completo() || got["AMARILLO"] != 1 || got["BLANCO"] != 1 || len(got) != 2 {
		t.Errorf("quería 1 amarillo y 1 blanco, quedó %+v", p.Lineas())
	}
}

// Un número con TODO completo sigue siendo una corrección de la línea en curso.
func TestNumeroSueltoConTodoCompletoCorrigeLaUltima(t *testing.T) {
	for nombre, store := range storesFicha(t) {
		t.Run(nombre, func(t *testing.T) { numeroSueltoCorrige(t, store) })
	}
}

func numeroSueltoCorrige(t *testing.T, store conversation.Store) {
	const from = "593999200104"
	ag := agentIncidente(nil, store)

	for _, m := range []string{"Blanco", "2", "también uno amarillo", "3"} {
		ag.anotarDelMensaje(from, m)
	}
	p, _ := store.GetPedidoEnCurso(from)
	l := p.Lineas()
	if len(l) != 2 || l[0].Cantidad != 2 || l[1].Color != "AMARILLO" || l[1].Cantidad != 3 {
		t.Errorf("el 3 corrige el amarillo (la línea en curso) y no toca el blanco: %+v", l)
	}
}

// El intercambio de color sigue sin leerse como pedido.
func TestTengoPorOtroColorSigueSiendoIntercambio(t *testing.T) {
	cases := []string{"tengo 2 amarillos, me los cambian por blanco", "tengo 2 azules por 2 blancos"}
	ag := agentIncidente(nil, conversation.NewMemStore())
	ctx, _ := ag.catalog.Get()
	for _, c := range cases {
		if got := cantidadesPegadasAlColor(ctx.Products, c); len(got) != 0 {
			t.Errorf("%q no es un pedido de esas cantidades: %v", c, got)
		}
	}
}
