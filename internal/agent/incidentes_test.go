package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/llm"
)

// Los INCIDENTES REALES de producción (ago-sep 2026), cada uno como un test que reproduce la
// conversación que falló. Es la prueba guiada de la spec (T2.3) escrita como código: una prueba
// manual se hace una vez y se olvida; esto falla el día que alguien reintroduzca el bug.
//
// Cada test usa un modelo FALSO que se comporta como se comportó el real ese día —normalmente
// afirmando algo sin llamar a la herramienta— y verifica que el bot ya no le cree.

// modeloQueDice es un llm.Provider falso: responde el texto que se le indique, sin llamar
// herramientas. Reproduce al modelo que "narra" una acción en vez de ejecutarla.
type modeloQueDice struct {
	respuestas []string // una por ronda; la última se repite
	mu         sync.Mutex
	llamadas   int
}

func (m *modeloQueDice) Generate(context.Context, llm.System, []*genai.Content, []*genai.Tool) (llm.Response, error) {
	m.mu.Lock()
	i := m.llamadas
	m.llamadas++
	m.mu.Unlock()
	if i >= len(m.respuestas) {
		i = len(m.respuestas) - 1
	}
	texto := m.respuestas[i]
	return llm.Response{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: texto}}},
		Text:    texto,
	}, nil
}

func (m *modeloQueDice) Nombre() string { return "fake" }
func (m *modeloQueDice) Modelo() string { return "fake-narrador" }

func catalogoDePrueba() *catalog.Client {
	return catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"}, {ID: 12, Nombre: "AZUL"},
		}}},
	})
}

// agentIncidente arma un Agent con catálogo real de prueba y horario amplio. El backend apunta
// a una URL muerta: los registros FALLARÁN, que es justo lo que hace visible si el bot le
// prometió al cliente algo que no ocurrió.
func agentIncidente(modelo llm.Provider, store conversation.Store) *Agent {
	ag := agentDePrueba(modelo, store)
	ag.catalog = catalogoDePrueba()
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	return ag
}

// INCIDENTE 03/09 — PEDIDO FANTASMA. El bot le dijo a la clienta "tu pedido está confirmado y
// el repartidor va en camino" sin haber llamado a registrar_pedido. No había pedido, no había
// conductor, y ella esperó un gas que nadie iba a llevar.
func TestIncidente_PedidoFantasma(t *testing.T) {
	const from = "593999100001"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	fake := &modeloQueDice{respuestas: []string{
		"¡Listo! Tu pedido está confirmado y el repartidor ya va en camino 🚚",
	}}
	ag := agentIncidente(fake, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "2")

	res, err := ag.HandleMessage(context.Background(), from, "sí, mándalo")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	// El backend está muerto: el pedido NO pudo crearse. Lo que no puede pasar es que el
	// cliente se quede con la confirmación inventada del modelo.
	if strings.Contains(res.Texto, "va en camino") && !strings.Contains(res.Texto, "Disculpa") {
		t.Errorf("el bot confirmó un pedido que no existe: %q", res.Texto)
	}
	if _, hay := store.GetActivePedido(from); hay {
		t.Error("se marcó un pedido activo que el backend nunca creó")
	}
}

// INCIDENTE 03/09 (bis) — CANCELACIÓN FANTASMA. El bot dijo "he cancelado tu pedido" sin llamar
// a cancelar_pedido: el pedido siguió vivo hasta que el conductor lo canceló a mano.
func TestIncidente_CancelacionFantasma(t *testing.T) {
	const from = "593999100002"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 512)
	fake := &modeloQueDice{respuestas: []string{"Entendido, he cancelado tu pedido 🙏"}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "ya no lo quiero")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	// El backend muerto no puede cancelar: el bot NO puede decir que quedó cancelado.
	if _, sigue := store.GetActivePedido(from); sigue && !strings.Contains(res.Texto, "Disculpa") {
		t.Errorf("le dijo al cliente que canceló pero el pedido sigue activo: %q", res.Texto)
	}
}

// INCIDENTE 05/09 — PROGRAMACIÓN FANTASMA (María Elena). Pidió agendar para las 18:30, el bot
// se lo confirmó sin llamar a programar_entrega, y a esa hora no iba a pasar nada. Antes de
// este refactor el detector ni siquiera reconocía la frase: no había candado.
func TestIncidente_ProgramacionFantasma(t *testing.T) {
	const from = "593999100003"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	fake := &modeloQueDice{respuestas: []string{
		"¡Listo! Te dejé agendada tu entrega para las 18:30 📅",
	}}
	ag := agentIncidente(fake, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")
	ag.anotarDelMensaje(from, "18:30")

	if !afirmaProgramado("¡Listo! Te dejé agendada tu entrega para las 18:30 📅") {
		t.Fatal("el detector no reconoce la frase del incidente: el candado sería inalcanzable")
	}
	if !ag.clienteQuiereProgramar(from) {
		t.Fatal("la ficha no registró que el cliente quiere programar a las 18:30")
	}

	res, err := ag.HandleMessage(context.Background(), from, "sí, para esa hora")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if _, agendada := store.GetConfirmingSchedule(from); !agendada && !strings.Contains(res.Texto, "Disculpa") {
		t.Errorf("le confirmó una programación que no existe: %q", res.Texto)
	}
}

// INCIDENTE 10/09 — EL CANDADO MINTIÓ (Ilda). Su pedido SÍ estaba registrado (#231, repartidor
// Carlos). Escribió "a lado de la funeraria" —solo una referencia de su casa—, el modelo repitió
// con razón que su pedido estaba confirmado, y como en ESE turno no se llamó ninguna herramienta
// el candado lo tomó por fantasma: le respondió "tuve un problema al registrar tu pedido" y la
// hizo repetir color, cantidad y ubicación de un pedido que ya existía.
func TestIncidente_PedidoRealNoSeTratacomoFantasma(t *testing.T) {
	const from = "593999100010"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.898331, -78.972586)
	store.SetActivePedido(from, 231) // el pedido de Ilda: existe de verdad
	fake := &modeloQueDice{respuestas: []string{
		"Tu pedido está confirmado y el repartidor Carlos va en camino 🚚",
	}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "a lado de la funeraria")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(res.Texto, "Disculpa") {
		t.Errorf("se disculpó por un pedido que SÍ existe: %q", res.Texto)
	}
	if !strings.Contains(res.Texto, "camino") {
		t.Errorf("pisó la respuesta correcta del modelo: %q", res.Texto)
	}
	if id, hay := store.GetActivePedido(from); !hay || id != 231 {
		t.Errorf("se perdió el pedido real: id=%d hay=%v", id, hay)
	}
}

// Misma trampa del lado de la PROGRAMACIÓN: el cliente ya tiene su entrega agendada y solo
// escribe otra cosa. Repetir "quedó agendada" es verdad, no un fantasma que haya que rescatar.
func TestIncidente_ProgramacionRealNoSeTratacomoFantasma(t *testing.T) {
	const from = "593999100011"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	store.CreateScheduled(conversation.ScheduledOrder{
		Phone: from, Cantidad: 1, ColorNombre: "BLANCO",
		Estado: conversation.SchedulePendiente,
	})
	fake := &modeloQueDice{respuestas: []string{
		"Tu entrega quedó agendada para las 18:30 📅",
	}}
	ag := agentIncidente(fake, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")
	ag.anotarDelMensaje(from, "18:30")

	res, err := ag.HandleMessage(context.Background(), from, "gracias")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(res.Texto, "Disculpa") {
		t.Errorf("se disculpó por una programación que SÍ existe: %q", res.Texto)
	}
}

// Un pedido activo COLGADO (el backend nunca lo cerró) no puede volver ciego al candado: pasada
// la ventana de huérfanos deja de contar como vivo, para que un fantasma real siga detectándose.
func TestIncidente_PedidoHuerfanoNoTapaAlFantasma(t *testing.T) {
	const from = "593999100012"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	store.SetActivePedido(from, 99)
	ag := agentIncidente(nil, store)

	if !ag.tienePedidoVivo(from) {
		t.Fatal("un pedido recién creado tiene que contar como vivo")
	}

	ag.store = storeConPedidoViejo{Store: store, edad: ventanaPedidoActivo + time.Hour}
	if ag.tienePedidoVivo(from) {
		t.Error("un pedido huérfano no puede seguir contando como vivo")
	}
}

// storeConPedidoViejo envejece el pedido activo sin esperar horas: solo cambia hace cuánto se
// marcó. Todo lo demás lo resuelve el store real.
type storeConPedidoViejo struct {
	conversation.Store
	edad time.Duration
}

func (s storeConPedidoViejo) ActivePedidoDesde(string) time.Duration { return s.edad }

// INCIDENTE 05/09 (bis) — LA HORA REPETIDA. La clienta dijo "6h30" y luego "18:30 pm", y el bot
// le pidió confirmar la hora TRES veces. Con la ficha, la hora se guarda cuando la dice.
func TestIncidente_NoRepreguntarLaHora(t *testing.T) {
	const from = "593999100004"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	ag := agentIncidente(nil, store)

	for _, m := range []string{"necesito un tanque", "Blanco", "1 cilindro", "a las 7 pm", "18:30"} {
		ag.anotarDelMensaje(from, m)
	}

	// El prompt que ve el modelo NO puede pedir la hora: el cliente ya la dio.
	_, vol := ag.construirSistema(from)
	if !strings.Contains(vol, "hora=18:30") {
		t.Errorf("la hora del cliente no llegó al prompt:\n%s", vol)
	}
	if strings.Contains(vol, "hora de la entrega") { // aparecería dentro de "FALTA:"
		t.Errorf("el prompt pide una hora que el cliente YA dio:\n%s", vol)
	}
}

// INCIDENTE 26/08 — EL BOT DECIDIÓ QUE YA ERA TARDE. A las 17:50, con horario hasta las 19:00,
// ofreció programar para el día siguiente y se contradijo en la misma frase. El horario lo
// decide el sistema, y el prompt tiene que decirlo EN POSITIVO.
func TestIncidente_DentroDeHorarioElServicioEstaActivo(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"} // siempre dentro

	_, vol := ag.construirSistema("593999100005")
	if !strings.Contains(vol, "DENTRO DEL HORARIO") {
		t.Errorf("el prompt no le dice al modelo que el servicio está ACTIVO:\n%s", vol)
	}
	if strings.Contains(vol, "FUERA DE HORARIO") {
		t.Errorf("dice fuera de horario estando dentro:\n%s", vol)
	}
}

// SUGERIDO POR LA REVISIÓN — "HOLA + CANTIDAD" EN EL PRIMER MENSAJE. El saludo más común de
// WhatsApp no puede convertir el pedido en una programación (la "h" de "hola" se leía como hora).
func TestIncidente_SaludoConCantidadNoEsProgramacion(t *testing.T) {
	const from = "593999100006"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "Hola, quiero 8 blanco")

	p, _ := store.GetPedidoEnCurso(from)
	if p.Hora != "" || p.Flujo == conversation.FlujoProgramacion {
		t.Errorf("un saludo con cantidad se volvió programación: %+v", p)
	}
	if ag.clienteQuiereProgramar(from) {
		t.Error("el candado agendaría un pedido que el cliente quiere AHORA")
	}
}

// SUGERIDO POR LA REVISIÓN — RECLAMO DE UN CLIENTE CON PEDIDO ENTREGADO. Un reclamo no puede
// armar un pedido nuevo: "van 4 días con este problema" dejaba la ficha en 4 cilindros.
func TestIncidente_ReclamoNoArmaUnPedido(t *testing.T) {
	const from = "593999100007"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1})
	ag := agentIncidente(nil, store)

	for _, m := range []string{
		"el cilindro blanco que me trajeron viene fallando",
		"van 4 dias con este problema y nadie me responde",
	} {
		ag.anotarDelMensaje(from, m)
	}

	if _, _, ok := ag.inferirPedido(from); ok {
		p, _ := store.GetPedidoEnCurso(from)
		t.Errorf("un reclamo dejó un pedido listo para registrar: %+v", p)
	}
	// Y el prompt no puede decirle al modelo que no falta nada.
	if _, vol := ag.construirSistema(from); strings.Contains(vol, "FALTA: nada") {
		t.Errorf("el prompt ordena registrar un pedido que el cliente nunca hizo:\n%s", vol)
	}
}

// INCIDENTE 10/09 — "COMO EL ÚLTIMO PEDIDO" (David). Canceló su pedido (la cancelación limpia
// la ficha), escribió "Como el último pedido" y el interceptor de repetir solo aceptaba el
// texto exacto del botón: el mensaje fue al modelo, que afirmó el pedido sin registrarlo; el
// rescate no encontró ficha y respondió "tuve un problema al registrar tu pedido" — cuatro
// veces seguidas, sin que hubiera ningún problema. Ahora la intención escrita entra por el
// mismo camino en código que el botón, y la ficha se carga desde el LastOrder.
func TestIncidente_ComoElUltimoPedido(t *testing.T) {
	const from = "593999100013"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1, Fecha: "10/09/2026",
	})
	ag := agentIncidente(nil, store)

	// Como quedó tras cancelar: sin pedido activo y sin ficha.
	store.ClearActivePedido(from)
	store.ClearPedidoEnCurso(from)

	_, manejado := ag.ResponderRepetirPedido(from, "Como el último pedido")
	if !manejado {
		t.Fatal("\"Como el último pedido\" escrito no se resolvió en código: el turno iría al " +
			"modelo y el rescate del fantasma no tendría ficha con qué registrar (el bug de David)")
	}
	if p, hay := store.GetPedidoEnCurso(from); !hay || p.Color != "BLANCO" || p.Cantidad != 1 {
		t.Errorf("la ficha no se cargó desde el último pedido: %+v (hay=%v)", p, hay)
	}
}

// INCIDENTE 27/08 — MENÚ REPETIDO. Un cliente escribió "¿por qué no me saludas?" y recibió el
// mismo menú tres veces. El tope de un menú por turno lo impide en código.
func TestIncidente_UnSoloMenuPorTurno(t *testing.T) {
	tn := &turno{}
	ag := agentIncidente(nil, conversation.NewMemStore())
	args := map[string]any{"cuerpo": "¿Qué color?", "opciones": []any{"Blanco", "Azul"}}

	// El primero intenta enviarse de verdad (sin credenciales de WhatsApp, falla y lo dice).
	ag.mostrarMenu(tn, "593999100008", args)
	tn.menuSent = true // simula que sí salió

	segundo := ag.mostrarMenu(tn, "593999100008", args)
	if !strings.Contains(segundo, "NO envíes otro menú") {
		t.Errorf("se permitió un segundo menú en el mismo turno: %q", segundo)
	}
}
