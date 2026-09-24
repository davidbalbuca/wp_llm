package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/llm"
)

// INCIDENTE 21/09 — LA PROMESA COLGADA (Doris, 593958615651).
//
// La clienta dio color, cantidad, ubicación, aceptó las políticas y mandó su cédula. El bot
// contestó "Gracias, Doris. Un momento mientras verifico tu información en el sistema..." y
// NO llamó a verificar_cliente. El turno terminó ahí. Como en WhatsApp solo un mensaje del
// cliente dispara un turno nuevo, y ella se quedó esperando —hizo exactamente lo que se le
// pidió—, la conversación murió en silencio. Quince minutos después la rescató un humano a
// mano. Nunca hubo pedido.
//
// Esta es una clase de fallo DISTINTA a la del pedido fantasma. Los candados de forzar.go
// cazan el PASADO ("ya lo hice") comparando contra listas de frases. Esta promesa está en
// FUTURO ("ahora lo hago"), y es peor: cuando el bot miente en pasado el cliente reclama y la
// conversación sigue; cuando promete en futuro, el cliente se calla a esperar.
//
// La regla que se verifica aquí NO es "detectar la frase 'un momento'" —eso sería el candado
// número catorce, y el modelo tiene infinitas maneras de decirlo—. Es una invariante sobre la
// forma del turno: SI EL TURNO TERMINA Y LA PELOTA QUEDA DEL LADO DEL BOT, ESE TURNO ESTÁ MAL.
// El bot solo puede cerrar un turno de dos maneras: haciendo algo (llamar una herramienta) o
// devolviéndole el turno al cliente (preguntarle algo).
func TestIncidente_PromesaColgada_Doris(t *testing.T) {
	const from = "593958615651"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.899462, -78.972733)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true, Fecha: time.Now()})

	// El modelo real de ese día: promete verificar y no llama a ninguna herramienta.
	fake := &modeloQueDice{respuestas: []string{
		"Gracias, Doris. Un momento mientras verifico tu información en el sistema...",
	}}
	ag := agentIncidente(fake, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "0301754156")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}

	// La pelota no puede quedarse del lado del bot. O el turno termina con una pregunta al
	// cliente, o termina con un menú (que también le devuelve el turno).
	if res.MenuEnviado {
		return // un menú le devuelve el turno a la clienta: correcto
	}
	if !strings.Contains(res.Texto, "?") && !strings.Contains(res.Texto, "¿") {
		t.Errorf("el turno terminó sin devolverle el turno a la clienta: ella espera al bot "+
			"y el bot no va a volver a hablar.\nrespuesta: %q", res.Texto)
	}
}

// La otra cara de la moneda: un turno que SÍ cierra bien no debe tocarse. Aquí el modelo
// contesta una pregunta informativa y le devuelve el turno al cliente. Sin este test, el
// arreglo podría "arreglar" todas las conversaciones normales metiendo preguntas de más.
func TestRespuestaNormalNoSeToca(t *testing.T) {
	const from = "593999100077"
	store := conversation.NewMemStore()
	// SIN signo de pregunta y SIN despedida, a propósito: así el turno llega hasta la guarda
	// que este test dice cubrir. Con un "¿te lo despacho?" al final salía antes por el signo de
	// pregunta y el test pasaba aunque la guarda se borrara (el mutante sobrevivió así).
	const dicho = "El cilindro de 15 kg cuesta $3.00 💵"
	fake := &modeloQueDice{respuestas: []string{dicho}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "cuánto cuesta el gas")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	// Igualdad EXACTA: un Contains("$3.00") pasa igual con el "¿seguimos?" pegado detrás.
	if res.Texto != dicho {
		t.Errorf("a quien solo preguntó un precio se le insistió con el pedido:\n  dijo: %q\n  salió: %q",
			dicho, res.Texto)
	}
}

// modeloQueLlamaYAfirma llama una herramienta en la primera ronda y en la segunda devuelve un
// texto SIN pregunta. Reproduce el turno legítimo que la invariante no debe tocar: el bot SÍ
// hizo algo, así que la conversación no quedó esperándolo aunque el texto no pregunte nada.
type modeloQueLlamaYAfirma struct {
	texto    string
	llamadas int
}

func (m *modeloQueLlamaYAfirma) Generate(context.Context, llm.System, []*genai.Content, []*genai.Tool) (llm.Response, error) {
	m.llamadas++
	if m.llamadas == 1 {
		args := map[string]any{"motivo": "prueba", "resumen": "prueba"}
		return llm.Response{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{Name: "escalar_al_dueno", Args: args}},
			}},
			Calls: []*genai.FunctionCall{{Name: "escalar_al_dueno", Args: args}},
		}, nil
	}
	return llm.Response{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: m.texto}}},
		Text:    m.texto,
	}, nil
}

func (m *modeloQueLlamaYAfirma) Nombre() string { return "fake" }
func (m *modeloQueLlamaYAfirma) Modelo() string { return "fake-con-tool" }

// Si el bot EJECUTÓ una herramienta, el turno no está colgado aunque el texto no pregunte:
// pasó algo de verdad y hay un flujo detrás que continúa. Sin este test, la invariante podría
// ignorar por completo si hubo herramienta y nadie se enteraría — el cliente recibiría un
// "¿seguimos?" pegado a cada confirmación real.
func TestTurnoConHerramientaNoSeToca(t *testing.T) {
	const from = "593999100079"
	const dicho = "Listo, ya avisé al equipo 🙏"
	store := conversation.NewMemStore()
	fake := &modeloQueLlamaYAfirma{texto: dicho}
	ag := agentIncidente(fake, store)

	// El cliente está a medio pedir: es la situación en la que la invariante SÍ actúa si no
	// hubo herramienta. Así el test aísla exactamente la condición "hubo herramienta".
	ag.anotarDelMensaje(from, "Blanco")

	res, err := ag.HandleMessage(context.Background(), from, "mi gas nunca llegó")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if res.Texto != dicho {
		t.Errorf("se alteró un turno que SÍ ejecutó una herramienta:\n  dijo: %q\n  salió: %q", dicho, res.Texto)
	}
}

// Un MENÚ le devuelve el turno al cliente aunque su cuerpo no lleve signo de pregunta: los
// botones SON la pregunta. Se prueba la invariante DIRECTAMENTE y no a través de HandleMessage
// porque enviar un menú exige WhatsApp de verdad; montar un envío falso probaría el simulacro,
// no la regla.
func TestTurnoConMenuNoSeToca(t *testing.T) {
	const from = "593999100080"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.anotarDelMensaje(from, "Blanco") // a medio pedir: sin menuSent, esto quedaría colgado

	// Cuerpo AFIRMATIVO a propósito: si la invariante dejara de mirar menuSent, este turno
	// parecería colgado y el cliente recibiría el menú con un "¿seguimos?" pegado.
	// El texto NO puede ir vacío: con reply="" la invariante sale antes por otra guarda y el
	// test pasaría aunque menuSent se ignorara por completo (pasó: el mutante sobrevivió).
	const cuerpo = "Elige el color de tu cilindro"
	conMenu := &turno{menuSent: true, lastMenuText: cuerpo}
	if ag.turnoQuedaColgado(conMenu, from, cuerpo, cuerpo) {
		t.Error("un turno que YA mandó menú se consideró colgado: los botones son la pregunta")
	}

	// Y el control: el mismo estado SIN menú sí está colgado. Sin esta mitad, el test pasaría
	// igual aunque la invariante nunca rescatara a nadie.
	sinMenu := &turno{}
	if !ag.turnoQuedaColgado(sinMenu, from, "Un momento, ya verifico", "Un momento, ya verifico") {
		t.Error("sin menú y sin herramienta, una afirmación suelta SÍ deja colgado al cliente")
	}
}

// Y que la PREGUNTA del modelo baste por sí sola: cliente a medio pedir, sin herramienta, pero
// el texto pregunta. Es el caso que separa "no hizo nada" de "no hizo nada Y no preguntó" — sin
// él, la invariante podría dejar de mirar el signo de pregunta y seguiría en verde.
func TestPreguntaDelModeloCierraElTurno(t *testing.T) {
	const from = "593999100081"
	const dicho = "¿De qué color es tu cilindro?"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{dicho}}
	ag := agentIncidente(fake, store)
	ag.anotarDelMensaje(from, "Blanco") // a medio pedir: la invariante está armada

	res, err := ag.HandleMessage(context.Background(), from, "quiero gas")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if res.Texto != dicho {
		t.Errorf("se le agregó un cierre a un turno que YA preguntaba:\n  dijo: %q\n  salió: %q", dicho, res.Texto)
	}
}

// C1 — EL FALSO POSITIVO QUE HABRÍA VISTO TODO CLIENTE EN COLA. Cuando un candado REEMPLAZA la
// respuesta del modelo, ese texto ya no es una promesa suelta: es una decisión del código, con
// su propio temporizador detrás. El de FDS-1 está escrito justo para que el cliente espere
// tranquilo SIN hacer nada; pegarle "¿seguimos?" le pide lo contrario en el mismo mensaje.
//
// Se verifica pasando por HandleMessage completo, que es donde se decide.
func TestElAvisoDeBusquedaNoRecibeElRescate(t *testing.T) {
	const from = "593999100083"
	store := conversation.NewMemStore()
	espera := conversation.PendingWait{IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 1}
	store.SetPendingWait(from, espera)
	// El modelo promete repartidor; el candado de repartidorprometido.go sustituye su texto.
	fake := &modeloQueDice{respuestas: []string{"¡Tu repartidor ya va en camino, llega en 10 minutos!"}}
	ag := agentIncidente(fake, store)
	ag.anotarDelMensaje(from, "Blanco") // a medio pedir: la invariante está armada

	res, err := ag.HandleMessage(context.Background(), from, "ya viene?")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if !strings.Contains(res.Texto, "buscando al repartidor") {
		t.Fatalf("el candado de FDS-1 no actuó; el test no prueba lo que dice: %q", res.Texto)
	}
	// Se compara contra el texto COMPLETO del candado, no contra una palabra del rescate: el
	// rescate cambia de frase según lo que falte ("¿Cuántos cilindros…?", "¿De qué color…?"),
	// así que buscar una palabra suelta deja el test ciego en cuanto ese texto se edita. Pasó:
	// este test dejó de detectar el bug al cambiar el mensaje de rescate.
	esperado := ag.mensajeBuscandoRepartidor(espera)
	if res.Texto != esperado {
		t.Errorf("se alteró un mensaje escrito por el código:\n  esperado: %q\n  salió:    %q",
			esperado, res.Texto)
	}
}

// C2 — DORIS CON EL BACKEND CAÍDO: el camino MÁS PROBABLE de que el incidente se repita. El
// modelo SÍ llama a verificar_cliente, la llamada falla, y remata con "un momento, ya verifico".
// Si una herramienta que no hizo nada desarmara la invariante, la clienta quedaría colgada
// igual que el 21/09, pero con la apariencia de estar cubierta.
func TestHerramientaQueFallaNoDesarmaLaInvariante(t *testing.T) {
	const from = "593999100084"
	store := conversation.NewMemStore()
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true, Fecha: time.Now()})
	// agentIncidente apunta a un backend muerto: ClientExists falla de verdad, no simulado.
	fake := &modeloQueLlamaVerificar{texto: "Gracias. Un momento mientras verifico tu información en el sistema..."}
	ag := agentIncidente(fake, store)
	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "0301754156")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if fake.llamoVerificar == 0 {
		t.Fatal("el test no ejerció verificar_cliente; no prueba lo que dice")
	}
	if !strings.ContainsAny(res.Texto, "?¿") {
		t.Errorf("con el backend caído la clienta quedó colgada igual que el 21/09:\n%q", res.Texto)
	}
}

// modeloQueLlamaVerificar llama verificar_cliente (que fallará contra el backend muerto) y
// después remata con una promesa en futuro, como hizo el modelo real con Doris.
type modeloQueLlamaVerificar struct {
	texto          string
	llamadas       int
	llamoVerificar int
}

func (m *modeloQueLlamaVerificar) Generate(context.Context, llm.System, []*genai.Content, []*genai.Tool) (llm.Response, error) {
	m.llamadas++
	if m.llamadas == 1 {
		m.llamoVerificar++
		args := map[string]any{"identificacion": "0301754156"}
		return llm.Response{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{Name: "verificar_cliente", Args: args}},
			}},
			Calls: []*genai.FunctionCall{{Name: "verificar_cliente", Args: args}},
		}, nil
	}
	return llm.Response{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: m.texto}}},
		Text:    m.texto,
	}, nil
}

func (m *modeloQueLlamaVerificar) Nombre() string { return "fake" }
func (m *modeloQueLlamaVerificar) Modelo() string { return "fake-verifica" }

// El rescate PREGUNTA POR EL DATO QUE FALTA, no "¿te ayudo con algo más?". La diferencia no es
// de estilo: esa fórmula da a entender que lo anterior terminó —justo lo que no pasó— e invita
// a un "no, gracias" que mata el pedido.
func TestElRescatePreguntaPorLoQueFalta(t *testing.T) {
	casos := []struct {
		nombre  string
		montar  func(*Agent, string)
		esperar string
	}{
		{
			nombre:  "eligió color y falta cantidad",
			montar:  func(ag *Agent, from string) { ag.anotarDelMensaje(from, "Blanco") },
			esperar: "¿Cuántos cilindros te envío? 😊",
		},
		{
			nombre:  "dijo cantidad y falta color",
			montar:  func(ag *Agent, from string) { ag.anotarDelMensaje(from, "quiero 2 cilindros") },
			esperar: "¿De qué color es tu cilindro? 😊",
		},
		{
			nombre: "ficha completa y falta la ubicación",
			montar: func(ag *Agent, from string) {
				ag.anotarDelMensaje(from, "Blanco")
				ag.anotarDelMensaje(from, "1")
			},
			esperar: "¿Me compartes tu ubicación 📎 para enviártelo?",
		},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			const from = "593999100085"
			store := conversation.NewMemStore()
			ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
			c.montar(ag, from)
			if got := ag.preguntaQueFalta(from); got != c.esperar {
				t.Errorf("el rescate no pide el dato que falta:\n  esperado: %q\n  salió:    %q", c.esperar, got)
			}
		})
	}
}

// La frontera entre una DESPEDIDA y una PROMESA COLGADA, que es lo más delicado del archivo:
// las dos pueden contener la misma fórmula ("cualquier cosa", "aquí estoy", "te esperamos"), y
// lo único que las separa es si el mensaje sigue después. Confundirlas cuesta en las dos
// direcciones: tomar una promesa por despedida deja al cliente esperando (el caso de Doris),
// y tomar una despedida por promesa le contesta "¿seguimos?" a quien ya se despidió.
func TestFronteraEntreDespedidaYPromesa(t *testing.T) {
	promesas := []string{
		"Gracias, Doris. Un momento mientras verifico tu información en el sistema...",
		"Dame un momento, aquí estoy revisando tu información en el sistema.",
		"Estoy verificando tu cédula, cualquier cosa te aviso por aquí.",
		"Ya mismo proceso tu pedido, te esperamos un segundito.",
		"Permíteme validar tus datos; quedo atento para confirmarte.",
	}
	despedidas := []string{
		"De nada 😊 Cualquier cosa, aquí estoy 👋",
		"¡Que estés bien! 🙌",
		"Listo, te esperamos pronto.",
		"Gracias por preferirnos.",
		"Perfecto. Cualquier cosa, aquí estoy.",
		"¡Un gusto ayudarte! Hasta pronto 👋",
		"Listo. Escríbeme cuando gustes.",
		"Que tengas un buen día.",
	}
	for _, c := range promesas {
		if esUnCierre(c) {
			t.Errorf("una promesa colgada se tomó por despedida (el cliente queda esperando): %q", c)
		}
	}
	for _, c := range despedidas {
		if !esUnCierre(c) {
			t.Errorf("una despedida no se reconoció (le contestaríamos \"¿seguimos?\"): %q", c)
		}
	}
}

// esUnCierre es la ÚNICA parte de la invariante que sí mira palabras, así que se prueba
// aparte: una despedida no tiene marcador gramatical propio y sin ella el bot le contestaría
// "¿seguimos?" a quien acaba de decir "gracias, hasta luego". El test de arriba
// (TestDespedidaNoEsPromesaColgada) NO la cubre: aquel cliente no está a medio pedir, así que
// sale antes por otra guarda y pasa aunque esta función devuelva siempre false.
func TestCierreReconocidoAunqueElClienteEstePidiendo(t *testing.T) {
	const from = "593999100082"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.anotarDelMensaje(from, "Blanco") // a medio pedir: la invariante está armada

	despedidas := []string{
		"De nada 😊 Cualquier cosa, aquí estoy 👋",
		"¡Que estés bien! 🙌",
		"Listo, te esperamos pronto.",
	}
	for _, d := range despedidas {
		if ag.turnoQuedaColgado(&turno{}, from, d, d) {
			t.Errorf("una despedida se tomó por turno colgado: %q", d)
		}
	}

	// Control: una afirmación que NO es despedida sí deja colgado al cliente. Sin esta mitad,
	// el test pasaría aunque esUnCierre devolviera true para todo.
	if !ag.turnoQuedaColgado(&turno{}, from, "Ya mismo reviso tu información en el sistema", "Ya mismo reviso tu información en el sistema") {
		t.Error("una promesa en futuro NO es una despedida: debe rescatarse")
	}
}

// Una despedida tampoco deja la pelota del lado del bot: el cliente cerró, nadie espera nada.
// Este caso NO debe disparar el rescate aunque no tenga signo de pregunta.
func TestDespedidaNoEsPromesaColgada(t *testing.T) {
	const from = "593999100078"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"¡De nada! 😊 Cualquier cosa, aquí estoy 👋",
	}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "gracias")
	if err != nil {
		t.Fatalf("HandleMessage devolvió error: %v", err)
	}
	if !strings.Contains(res.Texto, "aquí estoy") {
		t.Errorf("se alteró una despedida normal: %q", res.Texto)
	}
}
