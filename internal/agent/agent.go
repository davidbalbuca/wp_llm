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
	"math"
	"strings"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/escalation"
	"wp-llm-gas/internal/georoutes"
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

// coberturaMarkers son fragmentos (en minúsculas) de los mensajes que el backend devuelve
// cuando no hay conductor asignable por zona/cercanía. Se comparan contra el error para
// distinguir "sin cobertura" (negocio) de un fallo técnico real.
var coberturaMarkers = []string{
	"no existen conductores",
	"no hay conductores",
	"fuera de la zona",
	"fuera de zona",
	"fuera de cobertura",
	"sin cobertura",
	"no se encontraron conductores",
	"no existen conductores en el sector",
}

// esFalloDeCobertura indica si el mensaje de error del backend corresponde a "no hay
// repartidores en la zona / fuera de cobertura" en lugar de un error técnico.
func esFalloDeCobertura(mensaje string) bool {
	m := strings.ToLower(mensaje)
	for _, marker := range coberturaMarkers {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}

// behaviorPrompt son las instrucciones de comportamiento del agente. Viven en un archivo
// de plantilla (contenido, no lógica) embebido en el binario, para editarlas sin tocar código.
//
//go:embed prompts/behavior.md
var behaviorPrompt string

// Agent orquesta las llamadas a Gemini y la ejecución de herramientas.
// Zona horaria de Ecuador continental (sin horario de verano).
var zonaEcuador = time.FixedZone("ECT", -5*3600)

// parseHoraHHMM convierte "HH:MM" a minutos del día (-1 si es inválida).
func parseHoraHHMM(s string) int {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

type Agent struct {
	cfg       config.Config
	client    *genai.Client
	store     conversation.Store
	catalog   *catalog.Client
	gr        *georoutes.Client
	tools     []*genai.Tool
	escalated bool
	// menuSent indica que en este turno la IA ya envió un MENÚ interactivo por WhatsApp
	// (vía la tool mostrar_menu); el llamador NO debe enviar además el texto de respuesta.
	menuSent bool
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
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.GoogleAPIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

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

	return &Agent{cfg: cfg, client: client, store: store, catalog: catalogClient, gr: grClient, tools: tools}, nil
}

// HandleMessage procesa un mensaje del cliente y devuelve la respuesta para WhatsApp.
func (a *Agent) HandleMessage(ctx context.Context, from, text string) (string, error) {
	a.menuSent = false // se pone en true si la IA envía un menú interactivo en este turno
	a.lastMenuText = ""
	contents := append(a.store.History(from), &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: text}},
	})

	// El system prompt se arma en cada mensaje: comportamiento (estático) + información
	// del servicio (dinámica, traída del backend con caché). Así reflejamos cambios de
	// productos, colores o precios sin reiniciar el agente.
	contexto, disponible := a.catalog.Get()
	systemPrompt := strings.TrimSpace(behaviorPrompt) + "\n\nINFORMACIÓN DEL SERVICIO:\n" + renderServiceInfo(contexto, disponible)

	// Si ya conocemos al cliente (pidió antes), inyectamos sus datos para que el bot NO se
	// los vuelva a pedir. La IA los reutiliza directamente al registrar el pedido.
	if perfil, ok := a.store.GetProfile(from); ok && perfil.Identificacion != "" {
		systemPrompt += fmt.Sprintf("\n\nDATOS DEL CLIENTE (ya registrado, NO se los vuelvas a pedir; úsalos "+
			"directamente al registrar el pedido):\n- Cédula/identificación: %s\n- Nombres: %s\n- Correo: %s\n"+
			"Salúdalo por su nombre. Para un nuevo pedido solo necesitas: color/marca, cantidad y su ubicación de WhatsApp.",
			perfil.Identificacion, perfil.Nombres, perfil.Correo)

		// Si tiene un pedido anterior, ofrécele repetir lo mismo: es más amigable que
		// preguntarle todo desde cero.
		if last, ok := a.store.GetLastOrder(from); ok && last.Cantidad > 0 {
			systemPrompt += fmt.Sprintf("\n\nÚLTIMO PEDIDO DEL CLIENTE (del %s): %d x %s color/marca %s. "+
				"Cuando quiera pedir, en vez de preguntarle todo desde cero, ofrécele de forma amable repetir "+
				"este mismo pedido (ej: \"¿Deseas lo mismo de la última vez: %d %s %s? ¿O prefieres cambiar algo?\"). "+
				"Si acepta repetir, solo necesitas confirmar y pedirle la ubicación.",
				last.Fecha, last.Cantidad, last.Producto, last.Color, last.Cantidad, last.Producto, last.Color)
		}
	}

	// Si el cliente tiene un pedido recién entregado sin calificar, se lo indicamos al modelo
	// para que le pida (o registre) la calificación del repartidor.
	if rating, ok := a.store.GetPendingRating(from); ok && rating.PedidoID > 0 {
		systemPrompt += fmt.Sprintf("\n\nCALIFICACIÓN PENDIENTE: el cliente tiene un pedido recién ENTREGADO por el "+
			"repartidor %s. Si el cliente responde con un número del 1 al 5 (y opcionalmente un comentario), llama "+
			"PRIMERO a calificar_conductor con ese número, ANTES de ofrecer menús, repetir pedidos o cualquier otro "+
			"tema. Si aún no la ha dado, pídele con amabilidad que califique del 1 al 5 a su repartidor. Si prefiere "+
			"no calificar o lo ignora, no insistas ni lo vuelvas a mencionar.", rating.Conductor)
	}

	// Hora actual + horario laboral: fuera de horario NO se registran pedidos (regla dura,
	// también validada en código); se ofrece PROGRAMAR la entrega.
	ahora := time.Now().In(zonaEcuador)
	systemPrompt += fmt.Sprintf("\n\nHORA ACTUAL: %s (Ecuador). HORARIO DE ENTREGAS: %s a %s.",
		ahora.Format("15:04"), a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	if !a.dentroDeHorario(ahora) {
		systemPrompt += " ESTAMOS FUERA DE HORARIO: a esta hora NO hay conductores disponibles, así que NO llames " +
			"a registrar_pedido. Explícaselo con amabilidad y ofrécele PROGRAMAR la entrega con la herramienta " +
			"programar_entrega: pide color, cantidad, su ubicación de WhatsApp, cédula y nombre (si es cliente " +
			"nuevo) y la hora deseada. Para la hora, DILE EL HORARIO DE ATENCIÓN y deja que el cliente escriba " +
			"la que prefiera (dentro de ese horario y de las próximas 24 horas): NO le ofrezcas horas como " +
			"opciones ni uses mostrar_menu para eso."
	} else {
		// Decirlo EN POSITIVO es necesario: si solo se avisa cuando estamos fuera, el modelo ve
		// la hora cerca del cierre y deduce solo que "la jornada terminó". Paso en produccion el
		// 26/08 a las 17:50 (con el horario hasta las 19:00): ofrecio programar para el dia
		// siguiente y luego se contradijo en la misma frase.
		systemPrompt += " ESTAMOS DENTRO DEL HORARIO: el servicio está ACTIVO y SÍ hay entregas ahora mismo, " +
			"aunque falte poco para cerrar. Si el cliente quiere su gas, usa registrar_pedido. NO ofrezcas " +
			"programar_entrega salvo que el cliente PIDA EXPRESAMENTE otra hora, y NUNCA le digas que la " +
			"jornada terminó ni que no hay disponibilidad."
	}

	// Pedido PROGRAMADO esperando confirmación: el scheduler ya le escribió al cliente.
	if sch, ok := a.store.GetConfirmingSchedule(from); ok {
		a.store.SetLocation(from, sch.Latitude, sch.Longitude)
		if sch.Identificacion != "" {
			a.store.SetProfile(from, conversation.Profile{Identificacion: sch.Identificacion, Nombres: sch.Nombres})
		}
		systemPrompt += fmt.Sprintf("\n\nPEDIDO PROGRAMADO EN CONFIRMACIÓN: %d x %s color %s (los datos y la "+
			"ubicación del cliente ya están guardados). Si el cliente CONFIRMA (\"sí\", \"dale\", \"confirmo\"), "+
			"llama registrar_pedido con color=%s y cantidad=%d SIN pedirle nada más. Si dice que ya no lo desea, "+
			"agradécele y no registres nada.",
			sch.Cantidad, sch.ProductoNombre, sch.ColorNombre, sch.ColorNombre, sch.Cantidad)
	}

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: systemPrompt}}},
		Tools:             a.tools,
	}

	reply := ""
	for round := 0; round < maxToolRounds; round++ {
		resp, err := a.client.Models.GenerateContent(ctx, a.cfg.GeminiModel, contents, cfg)
		if err != nil {
			return "", err
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			break
		}
		cand := resp.Candidates[0]

		// ¿Hay llamadas a función?
		var calls []*genai.FunctionCall
		for _, p := range cand.Content.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, p.FunctionCall)
			}
		}

		if len(calls) > 0 {
			contents = append(contents, cand.Content) // turno del modelo con las llamadas
			respParts := make([]*genai.Part, 0, len(calls))
			for _, c := range calls {
				result := a.runTool(from, c.Name, c.Args)
				respParts = append(respParts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
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

		reply = strings.TrimSpace(resp.Text())
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
		return a.cancelarPedido(from)

	case "esperar_conductor":
		return a.esperarConductor(from)

	case "programar_entrega":
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

// verDireccionesGuardadas trae las direcciones guardadas del cliente para ofrecérselas. Si no
// tiene cuenta o direcciones, indica que pida la ubicación de WhatsApp.
func (a *Agent) verDireccionesGuardadas(from string) string {
	account, ok := a.store.GetAccount(from)
	if !ok || account.Username == "" {
		return "El cliente aún no tiene cuenta ni direcciones guardadas. Pídele que comparta su ubicación de WhatsApp para este pedido."
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		return "No pude consultar las direcciones guardadas ahora. Pídele que comparta su ubicación de WhatsApp."
	}
	dirs, err := a.gr.GetDirections(tokens.Access)
	if err != nil || len(dirs) == 0 {
		return "El cliente no tiene direcciones guardadas todavía. Pídele que comparta su ubicación de WhatsApp."
	}
	// WhatsApp muestra como máximo 10 filas en una lista; dejamos una para "Otra dirección".
	// Si el cliente tiene muchas, mostramos solo las más recientes (las últimas de la lista).
	if len(dirs) > 8 {
		dirs = dirs[len(dirs)-8:]
	}
	var b strings.Builder
	b.WriteString("Direcciones guardadas del cliente (etiqueta → id). Ofréceselas SIEMPRE con la herramienta " +
		"mostrar_menu (lista tappable), NUNCA como texto numerado 1️⃣ 2️⃣. Las opciones del menú son las " +
		"ETIQUETAS de abajo TAL CUAL, MÁS una última opción \"Otra dirección\". NO agregues el id ni " +
		"coordenadas al texto de las opciones:\n")
	for i, d := range dirs {
		etiqueta := friendlyDirName(d)
		if etiqueta == "" {
			// Dirección vieja sin nombre real: le damos una etiqueta secuencial legible.
			etiqueta = fmt.Sprintf("Dirección %d", i+1)
		}
		fmt.Fprintf(&b, "- id %d: %s\n", d.ID, etiqueta)
	}
	b.WriteString("Cuando el cliente toque una etiqueta, mapéala a su id y registra el pedido con " +
		"id_direccion_guardada = ese id (NO necesitas la ubicación). Si toca \"Otra dirección\", pídele que " +
		"comparta su ubicación de WhatsApp.")
	return b.String()
}

// friendlyDirName arma una etiqueta legible para una dirección guardada. Quita el prefijo
// genérico "WhatsApp - " (para mostrar solo "Casa", "Trabajo", etc.) y, si la dirección no tiene
// un nombre real (direcciones viejas guardadas simplemente como "WhatsApp"), cae a la referencia
// o a un extracto de la dirección de texto. Devuelve "" si no hay nada legible.
func friendlyDirName(d georoutes.SavedDirection) string {
	alias := strings.TrimSpace(d.Alias)
	for _, p := range []string{"WhatsApp -", "WhatsApp-", "Whatsapp -", "Whatsapp-"} {
		if strings.HasPrefix(alias, p) {
			alias = strings.TrimSpace(alias[len(p):])
			break
		}
	}
	if alias != "" && !strings.EqualFold(alias, "WhatsApp") {
		return alias
	}
	if ref := strings.TrimSpace(d.Referencia); ref != "" {
		return ref
	}
	if dir := strings.TrimSpace(d.Direccion); dir != "" && !strings.HasPrefix(dir, "Ubicación compartida por WhatsApp") {
		return dir
	}
	return ""
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

// cancelarPedido cancela el pedido ACTIVO del cliente cuando lo pide por WhatsApp. Re-autentica
// al cliente y llama al MISMO flujo que el botón "Cancelar" de la app (cancelOrder): el backend
// marca el pedido CANCELADO_CLIENTE, devuelve el stock al conductor y le avisa. Limpia el estado
// del pedido activo y el historial para arrancar fresco.
func (a *Agent) cancelarPedido(from string) string {
	pedidoID, ok := a.store.GetActivePedido(from)
	if !ok || pedidoID <= 0 {
		return "El cliente no tiene un pedido activo para cancelar. Aclárale con amabilidad que no encuentras un " +
			"pedido en curso a su nombre, y ofrécele hacer uno nuevo cuando quiera."
	}
	account, ok := a.store.GetAccount(from)
	if !ok || account.Username == "" {
		return "No encuentro la cuenta del cliente para cancelar el pedido. Discúlpate y dile que en un momento lo revisa el equipo."
	}
	// JWT fresco: el pedido pudo hacerse hace rato, re-autenticamos antes de cancelar.
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		return "No se pudo cancelar el pedido en este momento (motivo: " + err.Error() + "). Discúlpate y pídele que intente de nuevo en un momento."
	}
	if err := a.gr.CancelOrder(tokens.Access, pedidoID); err != nil {
		return "No se pudo cancelar el pedido (motivo: " + err.Error() + "). Discúlpate y dile que en un momento lo revisa el equipo."
	}
	a.store.ClearActivePedido(from)
	// El historial NO se borra: la memoria del chat dura la ventana de 24h.
	return "Pedido cancelado con éxito. Confírmale al cliente con amabilidad que su pedido fue cancelado y que " +
		"puede hacer uno nuevo cuando lo desee."
}

// esperarConductor arranca la espera de hasta 5 minutos: reintenta la asignación cada 30s y le
// avisa al cliente por WhatsApp cuando se asigne, o si se agota el tiempo sin repartidor.
func (a *Agent) esperarConductor(from string) string {
	w, ok := a.store.GetPendingWait(from)
	if !ok || w.IDProducto == 0 {
		return "No hay un pedido en espera en este momento. Ofrécele con amabilidad hacer un pedido nuevo."
	}
	a.startWaitForDriver(from, w)
	return "El cliente aceptó esperar. Confírmale con calidez que estás buscando un repartidor y que le " +
		"avisas apenas se asigne (o si en unos minutos no hay disponible). Pídele que esté atento por aquí."
}

// crearTicketSoporte crea el TICKET de la escalación (durable, se gestiona desde el panel), deja
// constancia en la auditoría del chat y notifica al equipo por CORREO (async, best-effort).
// Devuelve el id del ticket (0 si no se pudo crear).
func (a *Agent) crearTicketSoporte(from, motivo, resumen string) int64 {
	id := a.store.CreateTicket(from, motivo, resumen)
	if id > 0 {
		a.store.LogMessage(from, "system", fmt.Sprintf("🎫 Ticket de soporte #%d creado — %s", id, motivo))
	}
	go escalation.SendSupportEmail(a.cfg, id, from, motivo, resumen)
	return id
}

// cancelarEspera descarta el pedido en espera cuando el cliente NO quiere esperar. Antes de
// descartarlo lo registra como NO ASIGNADO en el backend, para gestión manual.
func (a *Agent) cancelarEspera(from string) string {
	a.registrarNoAsignado(from)
	a.store.ClearPendingWait(from)
	// El historial NO se borra: la memoria del chat dura la ventana de 24h.
	return "El cliente no quiso esperar; el pedido quedó registrado para gestión manual. ANTES de despedirte, " +
		"ofrécele PROGRAMAR la entrega para más tarde hoy o para mañana: si acepta, dile el horario de atención " +
		"y deja que ESCRIBA la hora que prefiera (dentro de ese horario y de las próximas 24 horas, sin " +
		"ofrecerle opciones), y llama a programar_entrega. Si tampoco quiere, " +
		"despídete con: \"Muchas gracias, espero poder ayudarte la próxima vez. 🙌\""
}

// registrarNoAsignado guarda el pedido pendiente como NO ASIGNADO en el backend (gestión manual).
// Best-effort: si algo falla, solo se loguea (nunca rompe el flujo del cliente).
func (a *Agent) registrarNoAsignado(from string) {
	w, ok := a.store.GetPendingWait(from)
	if !ok || w.IDProducto == 0 {
		return
	}
	account, okA := a.store.GetAccount(from)
	loc, okL := a.store.GetLocation(from)
	if !okA || !okL {
		return
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		log.Printf("[no-asignado] login falló para %s: %v", from, err)
		return
	}
	if err := a.gr.WppRegistrarPedidoNoAsignado(tokens.Access, loc.Latitude, loc.Longitude, w.IDTipoPago,
		[]georoutes.OrderProduct{{IDCategoria: w.IDCategoria, IDProducto: w.IDProducto, IDColor: w.IDColor, Cantidad: w.Cantidad}}); err != nil {
		log.Printf("[no-asignado] registro falló para %s: %v", from, err)
	}
}

// startWaitForDriver corre en segundo plano: reintenta la asignación cada 30s durante 5 min. En
// WhatsApp NO sirve el push (token placeholder), por eso el bot reintenta activamente. Al asignarse
// o al expirar, envía el mensaje directo por WhatsApp. Best-effort: nunca tumba el proceso.
func (a *Agent) startWaitForDriver(from string, w conversation.PendingWait) {
	cfg := a.cfg
	gr := a.gr
	store := a.store
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[espera] panic recuperado para %s: %v", from, r)
			}
		}()
		deadline := time.Now().Add(5 * time.Minute)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			// Si el cliente canceló (o ya se resolvió) mientras esperaba, salir sin avisar.
			if _, ok := store.GetPendingWait(from); !ok {
				return
			}
			account, okA := store.GetAccount(from)
			loc, okL := store.GetLocation(from)
			if okA && okL {
				if tokens, err := gr.Login(account.Username, account.Password); err == nil {
					res, err := gr.WppOrder(tokens.Access, loc.Latitude, loc.Longitude, w.IDTipoPago,
						[]georoutes.OrderProduct{{IDCategoria: w.IDCategoria, IDProducto: w.IDProducto, IDColor: w.IDColor, Cantidad: w.Cantidad}})
					if err == nil {
						// ¡Asignado! Guardar estado igual que un pedido normal y avisar al cliente.
						store.SetProfile(from, conversation.Profile{Identificacion: w.Identificacion, Nombres: w.Nombres})
						if res.IDPedido > 0 {
							store.SetOrderPhone(res.IDPedido, from)
							store.SetActivePedido(from, res.IDPedido)
						}
						store.SetLastOrder(from, conversation.LastOrder{Producto: w.ProductoNombre, Color: w.ColorNombre, Cantidad: w.Cantidad, Fecha: time.Now().Format("02/01/2006")})
						store.ClearPendingWait(from)
						// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
						msg := "🎉 ¡Listo! Ya tienes un repartidor asignado"
						if res.ConductorAsignado != "" {
							msg += ": " + res.ConductorAsignado
						}
						msg += ". Sale con tu pedido en breve. ¡Gracias por tu espera!"
						store.LogMessage(from, "system", msg)
						_ = whatsapp.SendText(cfg, from, msg)
						return
					}
				}
			}
			if time.Now().After(deadline) {
				break
			}
		}
		// Se agotaron los 5 min sin repartidor -> registrar el pedido como NO ASIGNADO (gestión
		// manual). Best-effort: si falla, solo se loguea.
		if account, okA := store.GetAccount(from); okA {
			if loc, okL := store.GetLocation(from); okL {
				if tokens, err := gr.Login(account.Username, account.Password); err == nil {
					if err := gr.WppRegistrarPedidoNoAsignado(tokens.Access, loc.Latitude, loc.Longitude, w.IDTipoPago,
						[]georoutes.OrderProduct{{IDCategoria: w.IDCategoria, IDProducto: w.IDProducto, IDColor: w.IDColor, Cantidad: w.Cantidad}}); err != nil {
						log.Printf("[no-asignado] registro (timeout) falló para %s: %v", from, err)
					}
				}
			}
		}
		store.ClearPendingWait(from)
		// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
		msgTimeout := "Te pedimos disculpas 🙏. Por ahora no hay ningún repartidor disponible " +
			"para asignar tu pedido. Intenta más tarde, con gusto te ayudamos."
		store.LogMessage(from, "system", msgTimeout)
		_ = whatsapp.SendText(cfg, from, msgTimeout)
	}()
}

// registrarPedido ejecuta la secuencia real de georoutes: mapea el color a IDs de
// producto, asegura la cuenta del cliente, hace login, registra la dirección desde la
// ubicación de WhatsApp y crea el pedido. Devuelve un texto para que el modelo responda.
// programarEntrega agenda una entrega para una hora dentro del horario laboral y de la ventana
// de 24h de WhatsApp. El scheduler (main.go) le escribirá al cliente a esa hora para confirmar
// y ahí se registra el pedido real (registrar_pedido).
func (a *Agent) programarEntrega(from string, args map[string]any) string {
	// REGLA DURA, espejo de la de registrarPedido: DENTRO del horario no se programa salvo que
	// el cliente pida otra hora explicitamente (viene 'hora' en los argumentos). Sin esto, el
	// modelo programaba entregas para el dia siguiente estando el servicio activo.
	// Excepcion: si el pedido quedo en espera por FALTA DE CONDUCTORES, programar SI es valido
	// aunque estemos dentro del horario (lo pidio el jefe el 26/08): se puede reagendar para mas
	// tarde el mismo dia o para el dia siguiente, dentro del horario y de las 24 h.
	_, esperandoConductor := a.store.GetPendingWait(from)
	if a.dentroDeHorario(time.Now().In(zonaEcuador)) && strings.TrimSpace(str(args["hora"])) == "" &&
		!esperandoConductor {
		return "ESTAMOS DENTRO DEL HORARIO (" + a.cfg.BotHorarioInicio + " a " + a.cfg.BotHorarioFin + "): " +
			"no se programa nada porque el servicio está ACTIVO ahora. Usa registrar_pedido para tomar el " +
			"pedido de una vez. Solo programa si el cliente pidió EXPRESAMENTE otra hora."
	}
	cantidad := toInt(args["cantidad"])
	if cantidad <= 0 {
		return "Falta una cantidad válida de cilindros. Pregúntale al cliente cuántos desea."
	}
	loc, hasLoc := a.store.GetLocation(from)
	if !hasLoc {
		return "Aún no tengo la ubicación del cliente y es obligatoria para programar. Pídele que comparta su " +
			"ubicación de WhatsApp 📎."
	}

	identificacion := strings.TrimSpace(str(args["identificacion"]))
	nombres := strings.TrimSpace(str(args["nombres"]))
	if perfil, ok := a.store.GetProfile(from); ok {
		if identificacion == "" {
			identificacion = perfil.Identificacion
		}
		if nombres == "" {
			nombres = perfil.Nombres
		}
	}
	if identificacion == "" || nombres == "" {
		return "Para programar necesito la cédula y el nombre completo del cliente. Pídeselos."
	}

	mins := parseHoraHHMM(str(args["hora"]))
	if mins < 0 {
		return fmt.Sprintf("Aún no tengo una hora válida. Dile que atendemos de %s a %s y pídele que escriba "+
			"a qué hora quiere recibirlo (formato HH:MM). NO le ofrezcas horas como opciones.",
			a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	}
	ini := parseHoraHHMM(a.cfg.BotHorarioInicio)
	fin := parseHoraHHMM(a.cfg.BotHorarioFin)
	if mins < ini || mins >= fin {
		return fmt.Sprintf("Esa hora está fuera del horario de entregas (%s a %s). Pídele al cliente una hora "+
			"dentro del horario.", a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	}

	ahora := time.Now().In(zonaEcuador)
	target := time.Date(ahora.Year(), ahora.Month(), ahora.Day(), mins/60, mins%60, 0, 0, zonaEcuador)
	dia := strings.ToLower(strings.TrimSpace(str(args["dia"])))
	if dia == "manana" || dia == "mañana" {
		target = target.Add(24 * time.Hour)
	} else if !target.After(ahora) {
		target = target.Add(24 * time.Hour) // esa hora ya pasó hoy -> la próxima es mañana
	}
	if target.Sub(ahora) > 24*time.Hour {
		return "Solo puedo programar entregas dentro de las PRÓXIMAS 24 HORAS. Dile eso al cliente con amabilidad " +
			"y pídele una hora más cercana."
	}

	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		return "No puedo consultar el catálogo en este momento. Pídele al cliente que intente más tarde."
	}
	colorNombre := strings.TrimSpace(str(args["color"]))
	producto, color, ok := findProductByColor(contexto.Products, colorNombre)
	if !ok {
		return fmt.Sprintf("El color/marca \"%s\" no está disponible. Colores disponibles: %s. "+
			"Pregúntale al cliente cuál desea.", colorNombre, availableColors(contexto.Products))
	}
	idtipopago, ok := defaultPaymentID(contexto.Payments)
	if !ok {
		return "No hay una forma de pago configurada en el sistema. Pídele al cliente que intente más tarde."
	}

	a.store.SetProfile(from, conversation.Profile{Identificacion: identificacion, Nombres: nombres})
	id := a.store.CreateScheduled(conversation.ScheduledOrder{
		Phone:          from,
		Identificacion: identificacion,
		Nombres:        nombres,
		IDCategoria:    producto.IDCategoria,
		IDProducto:     producto.IDProducto,
		IDColor:        color.ID,
		Cantidad:       cantidad,
		IDTipoPago:     idtipopago,
		ProductoNombre: producto.Nombre,
		ColorNombre:    color.Nombre,
		Latitude:       loc.Latitude,
		Longitude:      loc.Longitude,
		HoraPropuesta:  target.Unix(),
	})
	if id <= 0 {
		return "No se pudo guardar la programación. Discúlpate y pídele al cliente intentar de nuevo."
	}
	a.store.LogMessage(from, "system", fmt.Sprintf("📅 Entrega programada #%d: %d x %s %s para el %s",
		id, cantidad, producto.Nombre, color.Nombre, target.Format("02/01 a las 15:04")))
	// Si venia de una espera por falta de conductor, se cierra: el pedido ya quedo agendado y no
	// debe registrarse ademas como NO ASIGNADO cuando venza la espera.
	if esperandoConductor {
		a.store.ClearPendingWait(from)
	}

	etiquetaDia := "hoy"
	if target.Day() != ahora.Day() {
		etiquetaDia = "mañana"
	}
	return fmt.Sprintf("Entrega PROGRAMADA con éxito para %s a las %s. Confírmale al cliente que le escribiremos "+
		"a esa hora para confirmar y enviar su pedido de %d x %s color %s. Recuérdale estar atento al chat.",
		etiquetaDia, target.Format("15:04"), cantidad, producto.Nombre, color.Nombre)
}

// cancelarProgramacion borra la entrega agendada del cliente. Es la contraparte de
// programarEntrega: sin ella el modelo decia que cancelaba y la entrega seguia en pie.
func (a *Agent) cancelarProgramacion(from string) string {
	n := a.store.CancelScheduled(from)
	if n == 0 {
		return "El cliente no tiene ninguna entrega programada pendiente. Aclaraselo con amabilidad " +
			"y preguntale si quiere hacer un pedido ahora."
	}
	a.store.LogMessage(from, "system", "🗑️ Entrega programada cancelada por el cliente")
	return "Entrega programada CANCELADA. Confirmaselo al cliente y ofrecele hacer un pedido cuando lo necesite."
}

// dentroDeHorario indica si `t` cae dentro del horario laboral de entregas configurado.
func (a *Agent) dentroDeHorario(t time.Time) bool {
	ini := parseHoraHHMM(a.cfg.BotHorarioInicio)
	fin := parseHoraHHMM(a.cfg.BotHorarioFin)
	if ini < 0 || fin < 0 {
		return true // configuración inválida: no bloquear el servicio
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= ini && mins < fin
}

func (a *Agent) registrarPedido(from string, args map[string]any) string {
	// REGLA DURA de horario: fuera del horario laboral NO se registran pedidos (no hay
	// conductores). El prompt también lo dice; esto es la garantía en código.
	if !a.dentroDeHorario(time.Now().In(zonaEcuador)) {
		return "FUERA DE HORARIO (" + a.cfg.BotHorarioInicio + " a " + a.cfg.BotHorarioFin + "): NO se registró " +
			"el pedido porque a esta hora no hay conductores. Explícaselo al cliente con amabilidad y ofrécele " +
			"PROGRAMAR la entrega con la herramienta programar_entrega."
	}
	cantidad := toInt(args["cantidad"])
	if cantidad <= 0 {
		return "Falta una cantidad válida de cilindros. Pregúntale al cliente cuántos desea."
	}

	// El bot SIEMPRE trabaja con la ubicación compartida por WhatsApp (ya no hay direcciones
	// guardadas ni menús de dirección). La ubicación es obligatoria.
	loc, hasLoc := a.store.GetLocation(from)
	if !hasLoc {
		return "Aún no tengo la ubicación del cliente y es obligatoria para el pedido. " +
			"Pídele que comparta su ubicación de WhatsApp 📎."
	}

	identificacion := strings.TrimSpace(str(args["identificacion"]))
	nombres := strings.TrimSpace(str(args["nombres_completos"]))
	colorNombre := strings.TrimSpace(str(args["color"]))
	telefono := strings.TrimSpace(str(args["telefono"]))
	if telefono == "" {
		telefono = from
	}

	// Cliente recurrente: si la IA no repitió cédula/nombre, los tomamos del perfil guardado.
	if perfil, ok := a.store.GetProfile(from); ok {
		if identificacion == "" {
			identificacion = perfil.Identificacion
		}
		if nombres == "" {
			nombres = perfil.Nombres
		}
	}
	if identificacion == "" || nombres == "" {
		return "Faltan datos del cliente (cédula o nombre). Pídeselos antes de registrar el pedido."
	}

	// Persistimos el perfil apenas tenemos cédula+nombre (sin correo: el bot no lo pide). Así,
	// si el pedido falla, un cliente que ya dio sus datos no los repite en el próximo intento.
	if _, ya := a.store.GetProfile(from); !ya {
		a.store.SetProfile(from, conversation.Profile{
			Identificacion: identificacion,
			Nombres:        nombres,
		})
	}

	// Catálogo: mapear el color elegido a (producto, color) y elegir la forma de pago.
	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		a.escalated = true // derivación REAL (señal explícita; ya no se adivina por texto)
		return "No puedo consultar el catálogo en este momento. Discúlpate con el cliente y deriva al dueño."
	}
	producto, color, ok := findProductByColor(contexto.Products, colorNombre)
	if !ok {
		return fmt.Sprintf("El color/marca \"%s\" no está disponible. Colores disponibles: %s. "+
			"Pregúntale al cliente cuál desea.", colorNombre, availableColors(contexto.Products))
	}
	idtipopago, ok := defaultPaymentID(contexto.Payments)
	if !ok {
		a.escalated = true
		return "No hay una forma de pago configurada en el sistema. Deriva al dueño."
	}

	// Cuenta del bot (YA verificada, sin OTP): de la caché local, o del backend (get-or-create).
	account, ok := a.store.GetAccount(from)
	if !ok {
		nueva, err := a.gr.WppGetOrCreateClient(identificacion, nombres, telefono)
		if err != nil {
			a.escalated = true
			return "No se pudo registrar la cuenta del cliente (motivo: " + err.Error() + "). " +
				"Informa al cliente y deriva al dueño."
		}
		account = conversation.Account{Username: nueva.Username, Password: nueva.Password}
		a.store.SetAccount(from, account)
	}

	// Login → JWT. Si las credenciales guardadas ya no sirven (la contraseña rotó en el backend),
	// re-obtenemos la cuenta del bot y reintentamos una vez.
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		nueva, e2 := a.gr.WppGetOrCreateClient(identificacion, nombres, telefono)
		if e2 != nil {
			a.escalated = true
			return "No se pudo autenticar al cliente (motivo: " + err.Error() + "). Deriva al dueño."
		}
		account = conversation.Account{Username: nueva.Username, Password: nueva.Password}
		a.store.SetAccount(from, account)
		tokens, err = a.gr.Login(account.Username, account.Password)
		if err != nil {
			a.escalated = true
			return "No se pudo autenticar al cliente (motivo: " + err.Error() + "). Deriva al dueño."
		}
	}

	// Guardar JWT y refresh para reutilizar en próximos pedidos.
	account.JWT = tokens.Access
	account.Refresh = tokens.Refresh
	a.store.SetAccount(from, account)

	// Pedido: el backend hace UPSERT de la dirección "WhatsApp" del cliente con esta ubicación
	// (la reemplaza; el cliente no la ve ni la nombra) y REUTILIZA el flujo real de pedido.
	resultado, err := a.gr.WppOrder(tokens.Access, loc.Latitude, loc.Longitude, idtipopago, []georoutes.OrderProduct{{
		IDCategoria: producto.IDCategoria,
		IDProducto:  producto.IDProducto,
		IDColor:     color.ID,
		Cantidad:    cantidad,
	}})
	if err != nil {
		// Sin repartidores / fuera de cobertura NO es error técnico: guardamos el pedido a la
		// espera y le ofrecemos al cliente esperar hasta 5 min (reintento de asignación).
		if esFalloDeCobertura(err.Error()) {
			a.store.SetPendingWait(from, conversation.PendingWait{
				IDCategoria:    producto.IDCategoria,
				IDProducto:     producto.IDProducto,
				IDColor:        color.ID,
				Cantidad:       cantidad,
				IDTipoPago:     idtipopago,
				ProductoNombre: producto.Nombre,
				ColorNombre:    color.Nombre,
				Identificacion: identificacion,
				Nombres:        nombres,
			})
			return mensajeOfrecerEspera
		}
		a.escalated = true
		return "No se pudo registrar el pedido (motivo: " + err.Error() + "). " +
			"Informa al cliente del inconveniente y deriva al dueño para atención manual."
	}

	// Guardar perfil (para no re-pedir datos), el teléfono del pedido (para la calificación) y
	// el resumen del último pedido (para ofrecer repetir). El historial NO se borra: la
	// memoria del chat dura la ventana de 24h.
	a.store.SetProfile(from, conversation.Profile{
		Identificacion: identificacion,
		Nombres:        nombres,
	})
	if resultado.IDPedido > 0 {
		a.store.SetOrderPhone(resultado.IDPedido, from)
		a.store.SetActivePedido(from, resultado.IDPedido)
	}
	a.store.SetLastOrder(from, conversation.LastOrder{
		Producto: producto.Nombre,
		Color:    color.Nombre,
		Cantidad: cantidad,
		Fecha:    time.Now().Format("02/01/2006"),
	})
	// Marca como CONFIRMADO cualquier pedido programado en confirmación de este cliente.
	if sch, ok := a.store.GetConfirmingSchedule(from); ok {
		a.store.SetScheduledEstado(sch.ID, conversation.ScheduleConfirmado)
	}

	mensaje := fmt.Sprintf("Pedido registrado correctamente: %d x %s (%s).", cantidad, producto.Nombre, color.Nombre)
	if resultado.ConductorAsignado != "" {
		mensaje += " Repartidor asignado: " + resultado.ConductorAsignado + "."
	}
	return mensaje
}

// ensureAccount recupera del backend la cuenta del cliente por su identificación, o la
// crea si no existe. Devuelve las credenciales generadas por el backend.
func (a *Agent) ensureAccount(identificacion, nombres, telefono, correo, direccion, referencia, alias string, loc conversation.Location) (conversation.Account, error) {
	if existente, err := a.gr.UserExists(identificacion); err == nil && existente.Username != "" {
		return conversation.Account{Username: existente.Username, Password: existente.Password}, nil
	}
	if strings.TrimSpace(alias) == "" {
		alias = "WhatsApp"
	}

	creada, err := a.gr.CreateUser(georoutes.NewClientInput{
		Identificacion: identificacion,
		Nombres:        nombres,
		Telefono:       telefono,
		Correo:         correo,
		Direccion:      direccion,
		Alias:          alias,
		Referencia:     referencia,
		Latitude:       loc.Latitude,
		Longitude:      loc.Longitude,
	})
	if err != nil {
		return conversation.Account{}, err
	}
	return conversation.Account{Username: creada.Username, Password: creada.Password}, nil
}

// findProductByColor busca en el catálogo el producto cuyo listado de colores/marcas
// contiene el color pedido (comparación sin distinguir mayúsculas/espacios).
func findProductByColor(products []georoutes.Product, colorNombre string) (georoutes.Product, georoutes.Color, bool) {
	objetivo := strings.ToLower(strings.TrimSpace(colorNombre))
	for _, producto := range products {
		for _, color := range producto.Colores {
			if strings.ToLower(strings.TrimSpace(color.Nombre)) == objetivo {
				return producto, color, true
			}
		}
	}
	return georoutes.Product{}, georoutes.Color{}, false
}

// availableColors devuelve la lista de colores/marcas disponibles, para re-preguntar.
func availableColors(products []georoutes.Product) string {
	var nombres []string
	visto := map[string]bool{}
	for _, producto := range products {
		for _, color := range producto.Colores {
			if !visto[color.Nombre] {
				visto[color.Nombre] = true
				nombres = append(nombres, color.Nombre)
			}
		}
	}
	if len(nombres) == 0 {
		return "(sin colores configurados)"
	}
	return strings.Join(nombres, ", ")
}

// defaultPaymentID elige la forma de pago por defecto: "efectivo" si existe, o la primera.
func defaultPaymentID(payments []georoutes.Payment) (int, bool) {
	for _, pago := range payments {
		if strings.Contains(strings.ToLower(pago.Nombre), "efectivo") {
			return pago.ID, true
		}
	}
	if len(payments) > 0 {
		return payments[0].ID, true
	}
	return 0, false
}

// str convierte de forma segura un valor de args (any) a string.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toInt convierte un valor de args a entero. Los números JSON llegan como float64.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return int(math.Trunc(n))
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// renderServiceInfo arma la sección "INFORMACIÓN DEL SERVICIO" con el catálogo dinámico
// del backend (productos con colores/marcas y precio, y formas de pago). Si no hay
// catálogo disponible, devuelve una nota para que el agente derive en lugar de inventar.
func renderServiceInfo(contexto *catalog.Context, disponible bool) string {
	if !disponible || contexto == nil {
		return "La información del servicio (productos, precios) no está disponible en este momento. " +
			"Si el cliente pregunta por estos datos y no los tienes con certeza, discúlpate y deriva al dueño."
	}

	var texto strings.Builder
	negocio := contexto.Business
	if negocio.Nombre != "" {
		fmt.Fprintf(&texto, "Negocio: %s\n", negocio.Nombre)
	}
	if negocio.Telefono != "" {
		fmt.Fprintf(&texto, "Teléfono/WhatsApp: %s\n", negocio.Telefono)
	}
	if negocio.Horario != "" {
		fmt.Fprintf(&texto, "Horario de atención: %s\n", negocio.Horario)
	}
	if negocio.TiemposEntrega != "" {
		fmt.Fprintf(&texto, "Tiempos de entrega: %s\n", negocio.TiemposEntrega)
	}
	if negocio.Nombre != "" || negocio.Telefono != "" || negocio.Horario != "" {
		texto.WriteString("\n")
	}

	if len(contexto.Products) > 0 {
		texto.WriteString("Productos disponibles (con colores/marcas y precio):\n")
		for _, producto := range contexto.Products {
			// PRECIO TOTAL por unidad (unitario + envío + instalación + servicio): es lo que
			// el cliente paga de verdad. NUNCA cotizar solo el unitario.
			fmt.Fprintf(&texto, "- %s: $%.2f por cilindro (incluye envío, instalación y servicio)",
				producto.Nombre, producto.PrecioTotal())
			if len(producto.Colores) > 0 {
				var nombres []string
				for _, color := range producto.Colores {
					nombres = append(nombres, color.Nombre)
				}
				fmt.Fprintf(&texto, " | colores/marcas: %s", strings.Join(nombres, ", "))
			}
			texto.WriteString("\n")
		}
	}

	if negocio.FormasPago != "" {
		fmt.Fprintf(&texto, "\nFormas de pago: %s\n", negocio.FormasPago)
	}
	if negocio.Seguridad != "" {
		fmt.Fprintf(&texto, "Seguridad (fuga de gas): %s\n", negocio.Seguridad)
	}
	if negocio.Adicional != "" {
		fmt.Fprintf(&texto, "\n%s\n", negocio.Adicional)
	}

	texto.WriteString("\nPara concretar un pedido necesitas del cliente: cédula, nombre completo, " +
		"el color/marca deseado, la cantidad y su ubicación de WhatsApp (📎 → Ubicación). " +
		"NUNCA pidas correo electrónico (no se necesita).\n")
	return texto.String()
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
