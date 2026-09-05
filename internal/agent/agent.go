// Package agent es el núcleo conversacional: Gemini + function calling
// (escalar_al_dueno, registrar_pedido). El system prompt tiene dos capas: instrucciones
// de comportamiento (estáticas, aquí) e "INFORMACIÓN DEL SERVICIO" (dinámica, traída del
// backend vía el paquete catalog: productos con colores/marcas, precios y formas de pago).
//
// El pedido entra por el MISMO flujo que la app móvil (georoutes/startOrder): el bot crea
// o recupera la cuenta del cliente, hace login (JWT), registra la dirección desde la
// ubicación de WhatsApp y crea el pedido. No hay un canal de pedidos paralelo.
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/llm"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/notify"
	"wp-llm-gas/internal/whatsapp"
)

const maxToolRounds = 5

// mensajeSinCobertura es la instrucción para el modelo cuando el pedido no se puede
// asignar por falta de repartidores/cobertura en la zona. Es una condición de negocio
// (no un error técnico): se comunica con claridad y la conversación se cierra cordialmente,
// SIN derivar al dueño y SIN reintentar en bucle.
const mensajeSinCobertura = "IMPORTANTE: En este momento no hay repartidores disponibles en la zona del " +
	"cliente, así que no se puede concretar el pedido automáticamente. Explícale con amabilidad que por " +
	"ahora no tenemos cobertura/repartidores en su ubicación, que agradecemos su interés y que puede " +
	"intentar más tarde. NO derives al dueño y NO le pidas de nuevo los datos ni la ubicación: cierra la " +
	"conversación de forma cordial."

// mensajeOfrecerEspera se devuelve cuando no hay conductor AHORA pero el pedido quedó guardado a
// la espera. El bot le ofrece al cliente esperar hasta ~5 min (se reintenta la asignación) o
// cancelar. NO se deriva al dueño ni se le vuelven a pedir datos.
const mensajeOfrecerEspera = "IMPORTANTE: En este momento no hay un repartidor disponible cerca, pero el " +
	"pedido quedó listo. Ofrécele al cliente ESPERAR usando la herramienta mostrar_menu con el cuerpo: " +
	"'Los repartidores están un poco lejos 🚚. Podría tardar hasta 5 minutos en asignarse. ¿Deseas esperar?' " +
	"y las opciones exactas [\"Esperar\", \"Programar\", \"Cancelar\"]. Si el cliente elige esperar, llama a " +
	"la herramienta esperar_conductor. Si elige PROGRAMAR, dile el horario de atención y pídele que ESCRIBA " +
	"la hora que prefiera (más tarde HOY o MAÑANA, dentro de ese horario y de las próximas 24 horas); NO le " +
	"ofrezcas horas como opciones ni uses mostrar_menu para eso. Luego llama a programar_entrega. " +
	"Si elige cancelar, llama a cancelar_espera. NO derives al dueño ni le pidas de nuevo los datos ni la " +
	"ubicación."

// mensajePedirNombreDireccion se devuelve cuando el cliente va a usar una ubicación NUEVA pero
// aún no le puso nombre. Nombrar es obligatorio para que la dirección no quede genérica en la BD.
const mensajePedirNombreDireccion = "Es una ubicación NUEVA del cliente y hay que guardarla con un " +
	"nombre (no puede quedar genérica). Pregúntale cómo quiere llamar este lugar (por ejemplo: Casa, " +
	"Trabajo, Depa, Local) y recién entonces registra el pedido con ese nombre en guardar_direccion_como. " +
	"NO registres el pedido sin el nombre."

// behaviorPrompt son las instrucciones de comportamiento del agente. Viven en un archivo
// de plantilla (contenido, no lógica) embebido en el binario, para editarlas sin tocar código.
//
//go:embed prompts/behavior.md
var behaviorPrompt string

// Agent orquesta las llamadas a Gemini y la ejecución de herramientas.
// Zona horaria de Ecuador continental (sin horario de verano).
var zonaEcuador = time.FixedZone("ECT", -5*3600)

type Agent struct {
	cfg config.Config
	// modelo es quien contesta: Gemini o Claude, segun LLM_PROVIDER. El bucle de abajo es
	// el mismo para los dos.
	modelo    llm.Provider
	store     conversation.Store
	catalog   *catalog.Client
	gr        *georoutes.Client
	tools     []*genai.Tool
	escalated bool
	// menuSent indica que en este turno la IA ya envió un MENÚ interactivo por WhatsApp
	// (vía la tool mostrar_menu); el llamador NO debe enviar además el texto de respuesta.
	menuSent bool
	// canceloEnEsteTurno se pone en true si en este turno se ejecutó cancelar_pedido. Sirve para
	// el candado que fuerza la cancelación cuando el modelo la AFIRMA sin llamar a la herramienta.
	canceloEnEsteTurno bool
	// programoEnEsteTurno se pone en true si en este turno se ejecutó programar_entrega. Sirve
	// para el candado que fuerza la programación cuando el modelo la afirma sin llamar la tool.
	programoEnEsteTurno bool
	// ultimoPedido guarda QUE paso en el ultimo registrar_pedido de este turno. El texto que
	// esa funcion devuelve esta escrito para el modelo ("Ofrecele al cliente ESPERAR usando la
	// herramienta..."), no para el cliente, asi que el flujo determinista de confirmacion
	// (confirmacion.go) necesita el resultado en limpio y no adivinarlo del texto.
	ultimoPedido resultadoPedido
	// lastMenuText es la PREGUNTA del último menú enviado en este turno (cuerpo + opciones).
	// Se guarda en el historial como turno del modelo, porque el historial solo persiste TEXTO
	// (no function calls): sin esto los menús quedaban invisibles y el modelo, al no "recordar"
	// que ya preguntó (color/cantidad), volvía a mandar el mismo menú una y otra vez.
	lastMenuText string
}

func (a *Agent) DidEscalate() bool { return a.escalated }
func (a *Agent) ClearEscalated()   { a.escalated = false }

// MenuSent indica si en el último mensaje se envió un menú interactivo (para que el
// llamador no mande un texto adicional). ClearMenuSent lo resetea.
func (a *Agent) MenuSent() bool { return a.menuSent }
func (a *Agent) ClearMenuSent() { a.menuSent = false }

// LastMenuText devuelve la pregunta + opciones del último menú enviado (para auditar el chat).
func (a *Agent) LastMenuText() string { return a.lastMenuText }

// New crea un Agent con el cliente de Gemini y las herramientas declaradas.
func New(ctx context.Context, cfg config.Config, store conversation.Store, catalogClient *catalog.Client, grClient *georoutes.Client) (*Agent, error) {
	var modelo llm.Provider
	var err error
	switch cfg.LLMProvider {
	case "anthropic":
		modelo, err = llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.AnthropicModel, cfg.AnthropicMaxTokens, cfg.AnthropicCacheTTL)
	default:
		modelo, err = llm.NewGemini(ctx, cfg.GoogleAPIKey, cfg.GeminiModel)
	}
	if err != nil {
		return nil, err
	}
	log.Printf("[llm] proveedor=%s modelo=%s", modelo.Nombre(), modelo.Modelo())

	escalar := &genai.FunctionDeclaration{
		Name: "escalar_al_dueno",
		Description: "Deriva la conversación al dueño del negocio enviándole un mensaje de WhatsApp. " +
			"Úsala cuando el cliente pida explícitamente hablar con una persona/dueño, o cuando " +
			"no puedas responder con la información disponible.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"motivo":  {Type: genai.TypeString, Description: "Por qué se deriva: pedido explícito del cliente o falta de información."},
				"resumen": {Type: genai.TypeString, Description: "Resumen breve de lo que necesita el cliente."},
			},
			Required: []string{"motivo", "resumen"},
		},
	}

	// registrar_pedido: las coordenadas NO son parámetros del modelo; se toman de la ubicación
	// que el cliente compartió por WhatsApp (las inyecta runTool), para no inventarlas. El bot
	// SIEMPRE usa esa ubicación (no hay direcciones guardadas ni menús de dirección). Solo se
	// necesita color + cantidad y, si es un cliente nuevo, su cédula + nombre.
	registrar := &genai.FunctionDeclaration{
		Name: "registrar_pedido",
		Description: "Registra un pedido de gas en el sistema. Úsala solo cuando ya tienes el color/marca y la " +
			"cantidad, el cliente compartió su ubicación de WhatsApp, y (si es un cliente NUEVO) su cédula y su " +
			"nombre. No la uses para consultas; solo para concretar el pedido.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"identificacion":    {Type: genai.TypeString, Description: "Cédula/identificación del cliente. Si ya está registrado (ver DATOS DEL CLIENTE), NO hace falta repetirla."},
				"nombres_completos": {Type: genai.TypeString, Description: "Nombres y apellidos del cliente. Si ya está registrado, NO hace falta repetirlos."},
				"color":             {Type: genai.TypeString, Description: "Color/marca del cilindro. Debe coincidir con uno de los colores de la INFORMACIÓN DEL SERVICIO."},
				"cantidad":          {Type: genai.TypeInteger, Description: "Cantidad de cilindros solicitados."},
				"telefono":          {Type: genai.TypeString, Description: "Teléfono del cliente. Si no lo indica, se usa su número de WhatsApp."},
			},
			Required: []string{"color", "cantidad"},
		},
	}

	// verificar_cliente: al inicio, apenas el cliente da su cédula, verifica si ya está
	// registrado en el backend (SIN efectos: no crea usuario ni envía código). Si existe,
	// el bot lo saluda por su nombre y NO le vuelve a pedir nombre/correo (igual que la app).
	verificarCliente := &genai.FunctionDeclaration{
		Name: "verificar_cliente",
		Description: "Verifica si un cliente ya está registrado en el sistema por su cédula/identificación. " +
			"Llámala EN CUANTO el cliente te dé su cédula, ANTES de pedirle el nombre o el correo. Si el " +
			"cliente ya existe, te devolverá su nombre para saludarlo y NO tendrás que pedirle nombre ni correo.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"identificacion": {Type: genai.TypeString, Description: "Cédula o identificación (10 dígitos) que dio el cliente."},
			},
			Required: []string{"identificacion"},
		},
	}

	// calificar_conductor: cuando un pedido se entregó, el bot le pide al cliente calificar
	// al repartidor. Si el cliente da una nota (1-5) y un comentario opcional, se registra.
	calificar := &genai.FunctionDeclaration{
		Name: "calificar_conductor",
		Description: "Registra la calificación del cliente sobre el repartidor de su pedido recién entregado. " +
			"Úsala SOLO cuando el sistema indique que hay una CALIFICACIÓN PENDIENTE y el cliente te dé una nota " +
			"del 1 al 5 (y, opcionalmente, un comentario).",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"estrellas":  {Type: genai.TypeInteger, Description: "Calificación del 1 (muy malo) al 5 (excelente)."},
				"comentario": {Type: genai.TypeString, Description: "Comentario opcional del cliente sobre el servicio del repartidor."},
			},
			Required: []string{"estrellas"},
		},
	}

	// mostrar_menu: presenta opciones como MENÚ TAPPABLE de WhatsApp (botones si 2-3, lista si
	// 4-10) para que el cliente elija sin escribir. Para colores/marcas, cantidad, repetir/cambiar,
	// o direcciones guardadas. El cliente toca y su elección vuelve como texto.
	mostrarMenu := &genai.FunctionDeclaration{
		Name: "mostrar_menu",
		Description: "Muestra al cliente un MENÚ TAPPABLE en WhatsApp (botones si son 2-3 opciones, o una " +
			"lista si son 4-10) para que elija tocando, sin escribir. Úsala SIEMPRE que ofrezcas opciones " +
			"fijas: colores/marcas de cilindro, cantidad, repetir/cambiar el pedido, o direcciones guardadas. " +
			"NO la uses para respuestas de texto libre. Máximo 10 opciones.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"cuerpo":   {Type: genai.TypeString, Description: "Texto/pregunta que acompaña al menú (ej: '¿Qué cilindro necesitas?')."},
				"opciones": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}, Description: "Opciones a mostrar, de 2 a 10. Cada una es el texto de un botón/fila (ej: 'Blanco', 'Amarillo (Duragas)')."},
			},
			Required: []string{"cuerpo", "opciones"},
		},
	}

	// cancelar_pedido: cancela el pedido ACTIVO del cliente cuando lo pide por WhatsApp.
	cancelar := &genai.FunctionDeclaration{
		Name: "cancelar_pedido",
		Description: "Cancela el pedido ACTIVO del cliente. Úsala SOLO cuando el cliente pida explícitamente " +
			"cancelar su pedido (ej. \"cancelar mi pedido\", \"ya no lo quiero\", \"anula mi pedido\").",
		Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}},
	}

	// esperar_conductor: el cliente aceptó ESPERAR a que se libere un repartidor (hasta 5 min).
	esperar := &genai.FunctionDeclaration{
		Name: "esperar_conductor",
		Description: "Úsala SOLO cuando el sistema ofreció esperar por falta de repartidor y el cliente " +
			"ACEPTA esperar (ej. elige \"Esperar\" o dice \"sí, espero\"). Inicia la espera de hasta 5 minutos.",
		Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}},
	}

	// cancelar_espera: el cliente NO quiere esperar por el repartidor.
	cancelarEsp := &genai.FunctionDeclaration{
		Name: "cancelar_espera",
		Description: "Úsala SOLO cuando el sistema ofreció esperar por falta de repartidor y el cliente NO " +
			"quiere esperar (elige \"Cancelar\" o dice que no). Cancela el pedido en espera.",
		Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}},
	}

	// programar_entrega: FUERA del horario laboral (o si el cliente pide otra hora), agenda la
	// entrega para una hora dentro del horario y de la ventana de 24h. El scheduler le escribe
	// al cliente a esa hora para confirmar el pedido.
	programar := &genai.FunctionDeclaration{
		Name: "programar_entrega",
		Description: "Agenda una ENTREGA PROGRAMADA cuando estamos fuera del horario laboral. Antes de llamarla " +
			"necesitas: color, cantidad, la ubicación de WhatsApp YA compartida, la cédula y el nombre (si es " +
			"cliente nuevo) y la hora deseada. Solo horas dentro del horario laboral y de las próximas 24 horas.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"color":          {Type: genai.TypeString, Description: "Color/marca del cilindro"},
				"cantidad":       {Type: genai.TypeInteger, Description: "Cantidad de cilindros"},
				"hora":           {Type: genai.TypeString, Description: "Hora deseada en formato HH:MM (24 horas)"},
				"dia":            {Type: genai.TypeString, Description: "'hoy' o 'manana' (si no se indica, se asume la próxima ocurrencia de esa hora)"},
				"identificacion": {Type: genai.TypeString, Description: "Cédula del cliente (si es nuevo)"},
				"nombres":        {Type: genai.TypeString, Description: "Nombre completo del cliente (si es nuevo)"},
			},
			Required: []string{"color", "cantidad", "hora"},
		},
	}

	// cancelar_programacion: el cliente ya no quiere la entrega que habia agendado. Sin esta
	// herramienta el modelo respondia "he cancelado la programacion" y la entrega seguia viva:
	// aparecia en el panel y le llegaba el mensaje de confirmacion a su hora.
	cancelarProg := &genai.FunctionDeclaration{
		Name: "cancelar_programacion",
		Description: "Cancela la ENTREGA PROGRAMADA que el cliente tenía agendada. Úsala cuando diga que " +
			"ya no la quiere, que la anules o que prefiere pedir en otro momento. NUNCA le digas que " +
			"cancelaste una programación sin haber llamado a esta herramienta.",
		Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}},
	}

	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{escalar, registrar, verificarCliente, calificar, mostrarMenu, cancelar, esperar, cancelarEsp, programar, cancelarProg}}}

	return &Agent{cfg: cfg, modelo: modelo, store: store, catalog: catalogClient, gr: grClient, tools: tools}, nil
}

// HandleMessage procesa un mensaje del cliente y devuelve la respuesta para WhatsApp.
func (a *Agent) HandleMessage(ctx context.Context, from, text string) (string, error) {
	a.menuSent = false // se pone en true si la IA envía un menú interactivo en este turno
	a.lastMenuText = ""
	a.ultimoPedido = resultadoPedido{} // se llena SOLO si registrar_pedido corre en este turno
	a.canceloEnEsteTurno = false       // se pone en true si cancelar_pedido corre en este turno
	a.programoEnEsteTurno = false      // se pone en true si programar_entrega corre en este turno

	// Vigilancia pasiva: avisa al grupo si el mensaje parece un sondeo del negocio en vez de un
	// pedido. Va en su propia goroutine y NO altera la respuesta: el modelo ya tiene prohibido
	// hablar de otra cosa, así que esto solo hace que un humano se entere.
	go a.revisarSondeo(from, text)

	contents := append(a.store.History(from), &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: text}},
	})

	// El system prompt se arma en cada mensaje: comportamiento (estático) + información
	// del servicio (dinámica, traída del backend con caché). Así reflejamos cambios de
	// productos, colores o precios sin reiniciar el agente. Se devuelve en DOS piezas para
	// poder cachear la fija (ver construirSistema y internal/llm).
	sistemaFijo, systemPrompt := a.construirSistema(from)

	reply := ""
	for round := 0; round < maxToolRounds; round++ {
		resp, err := a.modelo.Generate(ctx, llm.System{Estatico: sistemaFijo, Volatil: systemPrompt}, contents, a.tools)
		if err != nil {
			return "", err
		}
		if resp.Content == nil {
			break // el modelo no devolvio nada utilizable
		}

		if len(resp.Calls) > 0 {
			contents = append(contents, resp.Content) // turno del modelo con las llamadas
			respParts := make([]*genai.Part, 0, len(resp.Calls))
			for _, c := range resp.Calls {
				result := a.runTool(from, c.Name, c.Args)
				respParts = append(respParts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						// El ID es lo que empareja el resultado con su llamada. Gemini casa
						// por nombre y lo deja vacio sin quejarse; Anthropic lo exige.
						ID:       c.ID,
						Name:     c.Name,
						Response: map[string]any{"result": result},
					},
				})
			}
			contents = append(contents, &genai.Content{Role: "user", Parts: respParts})
			// Una pregunta por turno: si en este round ya se envió un menú, cortamos aquí (no
			// pedimos otro round que podría mandar una segunda pregunta). El texto no se envía
			// porque menuSent hace que el llamador solo muestre el menú.
			if a.menuSent {
				break
			}
			continue // vuelve a llamar al modelo con los resultados
		}

		reply = strings.TrimSpace(resp.Text)
		break
	}

	// Red de seguridad: a veces el modelo (flash-lite) "narra" la herramienta mostrar_menu
	// escribiendo su JSON {cuerpo, opciones} como TEXTO en vez de invocarla de verdad, y el
	// cliente vería ese JSON crudo. Si detectamos ese JSON en la respuesta y aún no se envió
	// un menú, lo enviamos como menú interactivo real y limpiamos el texto (nunca dejamos salir
	// el JSON al cliente).
	if !a.menuSent {
		if cuerpo, opciones, preamble, ok := extractLeakedMenu(reply); ok {
			mensaje := strings.TrimSpace(cuerpo)
			if preamble != "" {
				if mensaje != "" {
					mensaje = preamble + "\n\n" + mensaje
				} else {
					mensaje = preamble
				}
			}
			if mensaje == "" {
				mensaje = "Elige una opción 👇"
			}
			if len(opciones) >= 2 && len(opciones) <= 10 {
				if err := whatsapp.SendMenu(a.cfg, from, mensaje, opciones); err == nil {
					a.menuSent = true
					a.lastMenuText = mensaje + "\n• " + strings.Join(opciones, "\n• ")
				}
			}
			// Enviara o no el menú, no dejamos el JSON crudo en el texto que se guarda/manda.
			reply = mensaje
		}
	}

	// Red de seguridad CRÍTICA contra el pedido fantasma: el modelo NO puede decirle al cliente
	// que su pedido está "confirmado / en camino / con repartidor asignado" si en este turno NO
	// llamó a registrar_pedido con éxito. El 03/09 pasó en producción: el cliente compartió su
	// ubicación y el bot respondió "tu pedido está confirmado y el repartidor en camino" sin
	// haber llamado a la herramienta — un pedido que nunca existió, un cliente esperando gas que
	// nadie iba a llevar. El prompt ya lo prohíbe, pero esto es el candado que no depende del
	// modelo: si afirma una confirmación sin respaldo, se reemplaza por un mensaje honesto.
	if !a.menuSent && !a.ultimoPedido.ok && !a.ultimoPedido.enEspera && afirmaPedidoConfirmado(reply) {
		// Si el cliente pidió PROGRAMAR (dijo una hora) y el modelo afirmó sin llamar la tool, lo
		// que hay que forzar es la PROGRAMACIÓN, no un registro inmediato. Pasó con María Elena el
		// 05/09: pidió agendar para las 18:30, el modelo lo confirmó sin llamar programar_entrega,
		// y el candado del fantasma le forzó una ESPERA de conductor. La programación nunca se
		// creó y a las 18:30 no iba a pasar nada, aunque un humano ya le había confirmado la hora.
		if !a.programoEnEsteTurno && a.clienteQuiereProgramar(from) {
			log.Printf("[fantasma] %s: el modelo afirmó una programación sin llamar programar_entrega; se fuerza", from)
			if forzado, ok := a.forzarProgramacionSiHaceFalta(from); ok {
				reply = forzado
			}
		} else {
			log.Printf("[fantasma] %s: el modelo afirmó un pedido sin registrar_pedido; se fuerza el registro", from)
			if forzado, ok := a.forzarRegistroSiHaceFalta(from); ok {
				reply = forzado
			}
		}
	}

	// Mismo candado para la CANCELACIÓN: si el modelo dijo "he cancelado tu pedido" sin haber
	// llamado a cancelar_pedido, el pedido sigue vivo. El 03/09 pasó: el bot dijo cancelado y el
	// pedido quedó activo hasta que el conductor lo canceló a mano. Si NO se canceló en este
	// turno pero el modelo lo afirma, se cancela en código. a.canceloEnEsteTurno lo marca el
	// dispatch de la tool.
	if !a.canceloEnEsteTurno && afirmaCancelado(reply) {
		log.Printf("[fantasma] %s: el modelo dijo cancelado sin llamar cancelar_pedido; se fuerza", from)
		if forzado, ok := a.forzarCancelacionSiHaceFalta(from); ok {
			reply = forzado
		}
	}

	// Turno del modelo para el HISTORIAL. Si en este turno se envió un menú interactivo,
	// guardamos la PREGUNTA del menú (cuerpo + opciones), NO un texto vacío ni el fallback:
	// así el modelo recuerda qué preguntó y no repite el menú. El historial solo guarda texto,
	// por eso sin esto los menús (function calls) quedaban invisibles para el modelo.
	modelTurn := reply
	if a.menuSent && a.lastMenuText != "" {
		modelTurn = a.lastMenuText
	}
	if strings.TrimSpace(modelTurn) == "" {
		// Sin menú y sin texto real: recién aquí cae el fallback visible al cliente.
		reply = "Disculpa, no pude procesar tu mensaje. ¿Podrías reformularlo?"
		modelTurn = reply
	}

	a.store.AppendUser(from, text)
	a.store.AppendModel(from, modelTurn)
	return reply, nil
}

// extractLeakedMenu detecta cuando el modelo escribió una llamada a mostrar_menu como TEXTO
// (un objeto JSON con "cuerpo" y "opciones") en vez de invocar la herramienta. Devuelve el
// cuerpo, las opciones y el texto que venía ANTES del JSON (preámbulo), y ok=true si lo halló.
func extractLeakedMenu(s string) (cuerpo string, opciones []string, preamble string, ok bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", nil, "", false
	}
	// Recorremos emparejando llaves (respetando strings/escapes) para aislar el objeto JSON.
	depth, end := 0, -1
	inStr, esc := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", nil, "", false
	}
	var m struct {
		Cuerpo   string   `json:"cuerpo"`
		Opciones []string `json:"opciones"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &m); err != nil || len(m.Opciones) < 2 {
		return "", nil, "", false
	}
	// Limpiamos el preámbulo de cercos de código markdown (``` / ```json) que suele dejar el modelo.
	pre := strings.TrimSpace(s[:start])
	pre = strings.TrimRight(pre, "`\n ")
	pre = strings.TrimSpace(strings.TrimSuffix(pre, "json"))
	pre = strings.TrimRight(pre, "`\n ")
	return m.Cuerpo, m.Opciones, strings.TrimSpace(pre), true
}

func (a *Agent) runTool(from, name string, args map[string]any) string {
	// Se registra CADA herramienta con sus argumentos y lo que devolvio. Sin esto, cuando el
	// bot hace algo raro en produccion solo queda su mensaje final y hay que adivinar que
	// llamo: asi paso el 27/08 con la reprogramacion de una clienta real.
	log.Printf("[tool] %s %s args=%v", from, name, args)
	res := a.runToolInterno(from, name, args)
	corte := res
	if len(corte) > 160 {
		corte = corte[:160] + "..."
	}
	log.Printf("[tool] %s %s -> %s", from, name, strings.ReplaceAll(corte, "\n", " "))
	return res
}

func (a *Agent) runToolInterno(from, name string, args map[string]any) string {
	switch name {
	case "escalar_al_dueno":
		a.escalated = true
		motivo, _ := args["motivo"].(string)
		resumen, _ := args["resumen"].(string)
		id := a.crearTicketSoporte(from, motivo, resumen)
		if id > 0 {
			return fmt.Sprintf("Se creó el caso de soporte #%d y el equipo fue notificado. Dile al cliente que "+
				"su caso quedó registrado y que pronto lo contactará una persona del equipo.", id)
		}
		return "No se pudo registrar el caso, pero el equipo fue notificado. Dile al cliente que pronto lo contactarán."

	case "verificar_cliente":
		return a.verificarCliente(from, args)

	case "mostrar_menu":
		return a.mostrarMenu(from, args)

	case "calificar_conductor":
		return a.calificarConductor(from, args)

	case "registrar_pedido":
		antesEscalado := a.escalated
		result := a.registrarPedido(from, args)
		// La escalación se marca EXPLÍCITAMENTE dentro de registrarPedido (a.escalated) solo en
		// las derivaciones reales. Antes se adivinaba buscando "dueño" en el texto, y el mensaje
		// del flujo de ESPERA ("NO derives al dueño...") disparaba un falso positivo que creaba
		// tickets y borraba la conversación en un flujo normal.
		if a.escalated && !antesEscalado {
			a.crearTicketSoporte(from, "Fallo al registrar un pedido", result)
		}
		return result

	case "cancelar_pedido":
		a.canceloEnEsteTurno = true
		return a.cancelarPedido(from)

	case "esperar_conductor":
		return a.esperarConductor(from)

	case "programar_entrega":
		a.programoEnEsteTurno = true
		return a.programarEntrega(from, args)

	case "cancelar_programacion":
		return a.cancelarProgramacion(from)

	case "cancelar_espera":
		return a.cancelarEspera(from)
	}
	return "Función desconocida: " + name
}

// verificarCliente consulta al backend si el cliente ya existe (por cédula) SIN efectos
// secundarios. Si existe, guarda su perfil local para no volver a pedirle nombre/correo y
// le indica al modelo que lo salude por su nombre. Si no existe (o falla la consulta),
// deja que el flujo de registro continúe normalmente.
func (a *Agent) verificarCliente(from string, args map[string]any) string {
	identificacion := strings.TrimSpace(str(args["identificacion"]))
	if identificacion == "" {
		return "Falta la cédula del cliente. Pídesela para verificar si ya está registrado."
	}
	info, err := a.gr.ClientExists(identificacion)
	if err != nil {
		// Fallo técnico: no bloqueamos el pedido, seguimos el registro normal.
		return "No se pudo verificar al cliente en este momento; continúa pidiéndole su nombre completo para registrarlo (NO pidas correo)."
	}
	if !info.Existe {
		return "El cliente NO está registrado todavía. Continúa el registro: pídele SOLO su nombre completo (NO pidas correo electrónico; no hace falta)."
	}
	// Cliente existente: persistimos su perfil para reutilizar sus datos y no volver a pedirlos.
	a.store.SetProfile(from, conversation.Profile{
		Identificacion: identificacion,
		Nombres:        info.Nombres,
		Correo:         info.Correo,
	})
	return fmt.Sprintf("El cliente YA está registrado. Nombre: %s. Correo: %s. Salúdalo por su nombre y NO le "+
		"pidas nombre ni correo. Para el pedido solo necesitas: color/marca, cantidad y su ubicación de WhatsApp.",
		info.Nombres, info.Correo)
}

// mostrarMenu envía al cliente un menú interactivo (botones o lista) con las opciones dadas.
// Marca menuSent para que el llamador NO envíe además un texto. Si falla, pide usar texto.
func (a *Agent) mostrarMenu(from string, args map[string]any) string {
	// Tope de UNA pregunta por turno: si ya se envió un menú en este turno, NO enviamos otro.
	// Evita que el modelo encadene color + cantidad de un tiro (y se adelante asumiendo la
	// elección). El segundo menú se rechaza y se le pide al modelo esperar la respuesta.
	if a.menuSent {
		return "Ya enviaste un menú en este turno. NO envíes otro menú ni otra pregunta: espera a que el cliente responda el que ya mandaste."
	}
	cuerpo := strings.TrimSpace(str(args["cuerpo"]))
	var opciones []string
	if raw, ok := args["opciones"].([]any); ok {
		for _, v := range raw {
			if s := strings.TrimSpace(str(v)); s != "" {
				opciones = append(opciones, s)
			}
		}
	}
	if cuerpo == "" || len(opciones) < 2 {
		return "Para un menú necesito un texto y al menos 2 opciones. Si hay menos, responde por texto normal."
	}
	if len(opciones) > 10 {
		return "El menú admite máximo 10 opciones. Muéstrale las principales o pídeselo por texto."
	}
	if err := whatsapp.SendMenu(a.cfg, from, cuerpo, opciones); err != nil {
		return "No pude enviar el menú (motivo: " + err.Error() + "). Preséntale las opciones por texto normal."
	}
	a.menuSent = true
	// Guardamos la pregunta del menú para el historial (ver Agent.lastMenuText): así, en el
	// próximo mensaje, el modelo recuerda qué ofreció y no vuelve a mandar el mismo menú.
	a.lastMenuText = cuerpo + "\n• " + strings.Join(opciones, "\n• ")
	return "MENÚ ENVIADO al cliente con esas opciones. NO repitas las opciones por texto; espera a que elija."
}

// calificarConductor registra la calificación del cliente sobre el conductor de un pedido
// entregado. Re-autentica al cliente (la calificación puede llegar horas después) y envía la
// reseña por el flujo real (ratingOrder). Limpia el estado pendiente al terminar.
func (a *Agent) calificarConductor(from string, args map[string]any) string {
	rating, ok := a.store.GetPendingRating(from)
	if !ok || rating.PedidoID <= 0 {
		return "No hay un pedido pendiente de calificación en este momento. Agradécele e invítalo a un nuevo pedido."
	}
	estrellas := toInt(args["estrellas"])
	if estrellas < 1 || estrellas > 5 {
		return "La calificación debe ser un número del 1 al 5. Pídele al cliente que indique cuántas estrellas (1 a 5)."
	}
	comentario := strings.TrimSpace(str(args["comentario"]))

	account, ok := a.store.GetAccount(from)
	if !ok || account.Username == "" {
		a.store.ClearPendingRating(from)
		return "No pude registrar la calificación (no encuentro la cuenta del cliente). Agradécele de todos modos por su tiempo."
	}
	// JWT fresco: la calificación puede llegar mucho después del pedido, re-autenticamos.
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		// Se limpia el pendiente TAMBIÉN aquí (antes este camino lo dejaba vivo y el saludo
		// seguía pidiendo la calificación en cada conversación nueva).
		a.store.ClearPendingRating(from)
		return "No se pudo registrar la calificación en este momento (motivo: " + err.Error() + "). Agradécele igualmente."
	}
	if err := a.gr.RatingOrder(tokens.Access, rating.PedidoID, estrellas, comentario); err != nil {
		a.store.ClearPendingRating(from)
		return "No se pudo registrar la calificación (motivo: " + err.Error() + "). Agradécele igualmente por su tiempo."
	}
	a.store.ClearPendingRating(from)
	return fmt.Sprintf("¡Calificación de %d/5 registrada con éxito para el repartidor %s! Agradécele calurosamente "+
		"al cliente por su tiempo y su preferencia, y despídete de forma cordial.", estrellas, rating.Conductor)
}

// nombreDe devuelve el nombre del cliente si lo conocemos, "" si no. Para los avisos.
func (a *Agent) nombreDe(from string) string {
	return conversation.NombreDe(a.store, from)
}

// crearTicketSoporte reporta una escalación del agente por el ÚNICO camino de fallos
// (notify.ReportarFallo): ticket + marca en el chat + correo + Telegram. Devuelve el id del
// ticket (0 si no se pudo crear).
func (a *Agent) crearTicketSoporte(from, motivo, resumen string) int64 {
	return notify.ReportarFallo(a.cfg, a.store, from, motivo, resumen)
}

// HandleVerification procesa el código OTP que el cliente envió por WhatsApp.
// Valida el código contra el backend. Devuelve (respuesta, retomarPedido):
//   - Si el código es inválido, devuelve el aviso y retomarPedido=false.
//   - Si es válido y NO hay pedido en pausa, devuelve un saludo y retomarPedido=false.
//   - Si es válido y HAY pedido en pausa, devuelve ("", true): el llamador debe retomar el
//     pedido con ResumeOrder para que la IA redacte la confirmación final.
func (a *Agent) HandleVerification(from, codigo string) (string, bool) {
	account, ok := a.store.GetPendingVerification(from)
	if !ok {
		return "", false
	}
	if err := a.gr.ValidateVerificationCode(account.JWT, codigo); err != nil {
		return "El código que ingresaste no es válido o ya expiró. Revisa tu correo (incluye spam). 📩", false
	}
	a.store.ClearPendingVerification(from)

	if draft, hay := a.store.GetOrderDraft(from); hay && draft.Cantidad > 0 {
		return "", true // hay pedido pendiente: el llamador lo retoma
	}
	return "¡Código verificado correctamente ✅! Tu cuenta ya está activa. Cuéntame, ¿qué cilindro " +
		"necesitas y cuántos? 😊", false
}

// ResumeOrder retoma el pedido que quedó en pausa esperando el OTP, ahora que el cliente
// ya está verificado. Concreta el pedido por el flujo normal (registrar_pedido) y deja que
// la IA redacte la confirmación al cliente. Es determinista: no depende de que la IA
// "recuerde" el color/cantidad, porque los toma del draft guardado.
func (a *Agent) ResumeOrder(ctx context.Context, from string) (string, error) {
	draft, ok := a.store.GetOrderDraft(from)
	if !ok || draft.Cantidad <= 0 {
		return "¡Tu cuenta ya está verificada ✅! Cuéntame, ¿qué cilindro necesitas y cuántos?", nil
	}
	a.store.ClearOrderDraft(from)

	// Mensaje sintético (en nombre del cliente) para que la IA llame a registrar_pedido con
	// el pedido ya conocido y redacte la respuesta con su estilo. Así el pedido se concreta
	// de forma determinista y el texto final sale natural.
	synthetic := fmt.Sprintf("Ya validé mi código de verificación. Procede a registrar mi pedido: "+
		"%d cilindro(s) color/marca %s. Ya compartí mi ubicación antes.", draft.Cantidad, draft.Color)
	return a.HandleMessage(ctx, from, synthetic)
}
