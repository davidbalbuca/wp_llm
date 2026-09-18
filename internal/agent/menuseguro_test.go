package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// EL MENÚ SALE AUNQUE EL MODELO NO LO MANDE.
//
// Pedido del dueño (18/09): "por más que haya botones, el modelo de IA no siempre lo manda;
// debemos asegurar que siempre lo haga y no dejar esto opcional a la IA".
//
// Es el mismo problema que ya explotó cuatro veces aquí: el prompt PIDE llamar a una herramienta
// y el modelo a veces no lo hace. Con los menús, el síntoma es silencioso — el cliente recibe la
// pregunta en texto y le toca escribir, que es exactamente lo que se quería evitar.

// agenteConCatalogo arma un agente con colores de verdad para poder construir el menú.
func agenteConCatalogo(t *testing.T) (*Agent, conversation.Store) {
	t.Helper()
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{
				{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"}, {ID: 12, Nombre: "NARANJA"},
			}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	// El menú no sale a internet: se captura para poder leerlo.
	return ag, store
}

// capturarMenu deja el agente apuntando a una función que guarda el menú en vez de enviarlo.
func capturarMenu(ag *Agent) (*string, *[]string) {
	var cuerpo string
	var opciones []string
	ag.enviarMenu = func(from, c string, o []string) error {
		cuerpo, opciones = c, o
		return nil
	}
	return &cuerpo, &opciones
}

// EL CASO PRINCIPAL: el modelo pregunta el color EN TEXTO y el menú sale igual.
func TestSiElModeloPreguntaElColorSinMenuElCodigoLoManda(t *testing.T) {
	const from = "593999050001"
	ag, _ := agenteConCatalogo(t)
	cuerpo, opciones := capturarMenu(ag)
	tur := &turno{} // el modelo NO mandó menú en este turno

	salida := ag.revisarMenuDeProducto(tur, from, "¡Hola David! 👋 ¿Qué color de cilindro necesitas?")

	if !tur.menuSent {
		t.Fatal("no se mandó el menú: el cliente tendría que escribir el color a mano, que es " +
			"justo lo que los botones evitan")
	}
	if salida != "" {
		t.Errorf("se devolvió texto además del menú (%q): el cliente vería la pregunta dos veces", salida)
	}
	// El texto del MODELO se respeta como cuerpo: lleva el saludo y el tono de la conversación.
	if !strings.Contains(*cuerpo, "David") {
		t.Errorf("se perdió el texto del modelo, con su saludo: %q", *cuerpo)
	}
	// Y las opciones salen del catálogo, no de una lista escrita a mano.
	for _, esperado := range []string{"BLANCO", "AMARILLO", "NARANJA"} {
		if !contieneOpcion(*opciones, esperado) {
			t.Errorf("falta el color %q en el menú: %v", esperado, *opciones)
		}
	}
}

// Y lo mismo con la CANTIDAD, una vez que ya eligió color.
func TestSiElModeloPreguntaLaCantidadSinMenuElCodigoLoManda(t *testing.T) {
	const from = "593999050002"
	ag, store := agenteConCatalogo(t)
	_, opciones := capturarMenu(ag)
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO"}) // sin cantidad
	tur := &turno{}

	salida := ag.revisarMenuDeProducto(tur, from, "¡Perfecto! ¿Cuántos cilindros necesitas?")

	if !tur.menuSent {
		t.Fatal("no se mandó el menú de cantidad")
	}
	if salida != "" {
		t.Errorf("se devolvió texto además del menú: %q", salida)
	}
	if len(*opciones) < 2 {
		t.Errorf("el menú de cantidad tiene muy pocas opciones: %v", *opciones)
	}
}

// SI EL MODELO YA MANDÓ MENÚ, no se manda otro. El tope de una pregunta por turno vale también
// para el candado: dos menús seguidos es justo lo que behavior.md §5 prohíbe.
func TestSiElModeloYaMandoMenuNoSeMandaOtro(t *testing.T) {
	const from = "593999050003"
	ag, _ := agenteConCatalogo(t)
	var seMando bool
	ag.enviarMenu = func(from, c string, o []string) error { seMando = true; return nil }
	tur := &turno{menuSent: true} // el modelo SÍ lo mandó

	ag.revisarMenuDeProducto(tur, from, "¿Qué color necesitas?")

	if seMando {
		t.Error("se mandó un segundo menú en el mismo turno: el cliente recibiría la pregunta dos veces")
	}
}

// NO se manda menú si el dato NO falta. Si el cliente ya eligió color y el modelo menciona
// colores por otro motivo (le está confirmando, o respondiendo una duda), un menú ahí sobra.
func TestNoSeMandaMenuSiElDatoYaEstaElegido(t *testing.T) {
	const from = "593999050004"
	ag, store := agenteConCatalogo(t)
	var seMando bool
	ag.enviarMenu = func(from, c string, o []string) error { seMando = true; return nil }
	// Ficha COMPLETA: color y cantidad.
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})
	tur := &turno{}

	ag.revisarMenuDeProducto(tur, from, "Tu cilindro BLANCO va en camino. ¿Cuántos días te dura?")

	if seMando {
		t.Error("se mandó un menú por un dato que el cliente ya eligió: el bot le preguntaría algo " +
			"que ya respondió")
	}
}

// Y NO se manda por una frase que solo MENCIONA un color o un número sin preguntarlo. Un candado
// que salta de más rompe la conversación normal, que es el otro error caro.
func TestNoSeMandaMenuPorFrasesQueNoPreguntanNada(t *testing.T) {
	frases := []string{
		"Tu pedido de 2 cilindros BLANCO va en camino 🚚",
		"El cilindro blanco cuesta $3.50",
		"¡Hola! 👋 ¿En qué te puedo ayudar?",
		"Compárteme tu ubicación por WhatsApp 📎",
		"Ya tengo tu color y cantidad, ahora necesito tu ubicación",
	}
	for _, frase := range frases {
		const from = "593999050005"
		ag, _ := agenteConCatalogo(t)
		var seMando bool
		ag.enviarMenu = func(from, c string, o []string) error { seMando = true; return nil }

		ag.revisarMenuDeProducto(&turno{}, from, frase)

		if seMando {
			t.Errorf("se mandó un menú por %q, que no está preguntando nada", frase)
		}
	}
}

// Si el menú FALLA (WhatsApp caído), el cliente recibe la pregunta escrita: peor que un botón,
// pero infinitamente mejor que quedarse sin respuesta.
func TestSiElMenuFallaElClienteRecibeLaPreguntaEscrita(t *testing.T) {
	const from = "593999050006"
	ag, _ := agenteConCatalogo(t)
	ag.enviarMenu = func(from, c string, o []string) error { return errFalso }
	tur := &turno{}

	const pregunta = "¿Qué color de cilindro necesitas?"
	salida := ag.revisarMenuDeProducto(tur, from, pregunta)

	if salida != pregunta {
		t.Errorf("con el menú caído el cliente se queda sin la pregunta: %q", salida)
	}
	if tur.menuSent {
		t.Error("se marcó como enviado un menú que falló: el cliente no recibiría nada")
	}
}

// Sin catálogo no se inventan colores: sale el texto, como antes.
func TestSinCatalogoNoSeInventanColores(t *testing.T) {
	const from = "593999050007"
	ag := agentDePrueba(nil, conversation.NewMemStore()) // catálogo apuntando a un backend muerto
	var seMando bool
	ag.enviarMenu = func(from, c string, o []string) error { seMando = true; return nil }

	const pregunta = "¿Qué color necesitas?"
	if salida := ag.revisarMenuDeProducto(&turno{}, from, pregunta); salida != pregunta {
		t.Errorf("sin catálogo debía salir el texto tal cual: %q", salida)
	}
	if seMando {
		t.Error("se mandó un menú de colores sin catálogo: las opciones serían inventadas")
	}
}

// Y está CABLEADO en HandleMessage: un test que solo llame a la función pasa igual aunque nadie
// la invoque en el flujo real (ya pasó dos veces en este proyecto).
func TestElCandadoDelMenuEstaCableado(t *testing.T) {
	if !archivoContiene(t, "agent.go", "a.revisarMenuDeProducto(t, from, reply)") {
		t.Error("el candado del menú no está cableado: si el modelo no manda los botones, el " +
			"cliente sigue teniendo que escribir")
	}
}

func contieneOpcion(opciones []string, buscado string) bool {
	for _, o := range opciones {
		if strings.EqualFold(strings.TrimSpace(o), buscado) {
			return true
		}
	}
	return false
}

// errFalso simula un fallo de WhatsApp.
var errFalso = errorDePrueba("whatsapp no disponible")

type errorDePrueba string

func (e errorDePrueba) Error() string { return string(e) }
