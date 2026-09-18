package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// LOS COMANDOS DE HERRAMIENTAS NO LLEGAN AL MODELO.
//
// El caso (Ángel, 18/09): escribió comandos de Claude Code en un chat real —`/clear`, `/compact`,
// `/model`— y el bot los contestó como si fueran mensajes de un cliente. Se equivocó al teclear
// ("comdel", "comtacto") y el bot siguió respondiendo cada intento con naturalidad.
//
// La detección va por la FORMA (empieza por "/" y es una sola palabra), no por una lista de
// comandos conocidos: justamente lo que falló fue un comando mal escrito que nadie había previsto.

func TestLosComandosNoLleganAlModelo(t *testing.T) {
	// Los que se vieron en producción, incluidos los que Ángel escribió mal.
	comandos := []string{
		"/clear", "/compact", "/model", "/comdel", "/comtacto", "/help", "/exit",
		"/CLEAR", "  /compact  ", "/algo-que-no-existe", "/plugin:skill",
	}
	for _, texto := range comandos {
		if _, ok := EsComando(texto); !ok {
			t.Errorf("%q no se reconoció como comando: el bot lo contestaría como si fuera un "+
				"cliente pidiendo gas", texto)
		}
	}
}

// Y NO se confunde con lo que un cliente sí escribe. Una barra dentro de una frase es normal en
// una dirección ecuatoriana ("10 de agosto s/n"); interceptarla dejaría al cliente sin respuesta.
func TestNoSeConfundenConMensajesDeClientes(t *testing.T) {
	mensajes := []string{
		"hola", "quiero 2 cilindros", "vivo en la 10 de agosto s/n y Bolívar",
		"a las 7/8 de la noche", "/ hola que tal", "/", "s/n", "24/7 atienden?",
		"mi cédula es 0105566777", "",
	}
	for _, texto := range mensajes {
		if cmd, ok := EsComando(texto); ok {
			t.Errorf("%q se tomó por el comando %q: el cliente se quedaría sin respuesta", texto, cmd)
		}
	}
}

// `/clear` SE CUMPLE, no se explica. Quien lo escribe quiere empezar de nuevo, y el bot sabe
// hacerlo: negárselo por venir en forma de comando sería quedarse en la forma y perder el fondo.
func TestClearLimpiaDeVerdadLaConversacion(t *testing.T) {
	const from = "593999200001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.AppendUser(from, "quiero 2 blancos")
	store.AppendModel(from, "¡Listo! va en camino")
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})

	respuesta, manejado := ag.ResponderComando(from, "/clear")
	if !manejado {
		t.Fatal("/clear no se resolvió en código")
	}
	if _, hay := store.GetPedidoEnCurso(from); hay {
		t.Error("la ficha del pedido sobrevivió a /clear: no se empezó de cero de verdad")
	}
	// Queda solo el turno del comando (se limpia antes de escribirlo), no el pedido viejo.
	for _, c := range store.History(from) {
		for _, p := range c.Parts {
			if strings.Contains(strings.ToLower(p.Text), "2 blancos") {
				t.Error("el historial conserva el pedido anterior tras /clear")
			}
		}
	}
	if strings.TrimSpace(respuesta) == "" {
		t.Error("no se le respondió nada al cliente")
	}
}

// Los demás comandos se contestan con amabilidad y SIN explicar qué es Claude Code: a quien tecleó
// algo raro por error le sirve más un empujón hacia el pedido que una lección.
func TestLosDemasComandosSeContestanSinTecnicismos(t *testing.T) {
	const from = "593999200002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	respuesta, manejado := ag.ResponderComando(from, "/compact")
	if !manejado {
		t.Fatal("/compact no se resolvió en código")
	}
	bajo := strings.ToLower(respuesta)
	for _, tecnicismo := range []string{"claude", "comando no reconocido", "error", "inválido"} {
		if strings.Contains(bajo, tecnicismo) {
			t.Errorf("la respuesta le habla de cosas técnicas (%q): %q", tecnicismo, respuesta)
		}
	}
	// Y le recuerda qué SÍ puede hacer aquí.
	if !strings.Contains(bajo, "gas") {
		t.Errorf("no se le ofrece lo que el bot sí sabe hacer: %q", respuesta)
	}
}

// El turno queda en el historial: sin esto el modelo ve un hueco y, si el cliente escribe después,
// arranca como si no le hubieran contestado nada.
func TestElTurnoDelComandoQuedaEnElHistorial(t *testing.T) {
	const from = "593999200003"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	if _, manejado := ag.ResponderComando(from, "/model"); !manejado {
		t.Fatal("/model no se resolvió")
	}
	if len(store.History(from)) < 2 {
		t.Errorf("el turno no quedó en el historial (%d turnos)", len(store.History(from)))
	}
}

// Un mensaje normal NO lo toca: devuelve false y sigue su camino hacia el modelo.
func TestUnMensajeNormalNoLoTocaElInterceptor(t *testing.T) {
	const from = "593999200004"
	ag := agentDePrueba(nil, conversation.NewMemStore())

	if _, manejado := ag.ResponderComando(from, "quiero un cilindro blanco"); manejado {
		t.Error("se interceptó un pedido normal")
	}
}

// Y está CABLEADO en el webhook, lo primero de todo: un comando no tiene por qué pasar por ningún
// otro interceptor ni por el modelo.
func TestElInterceptorDeComandosEstaCableado(t *testing.T) {
	if !archivoContiene(t, "../../cmd/bot/main.go", "ag.ResponderComando(inc.From, inc.Text)") {
		t.Error("el interceptor de comandos no está cableado: el bot volvería a contestar /clear " +
			"y /compact como si fueran mensajes de un cliente")
	}
}
