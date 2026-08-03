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
	"encoding/json"
	_ "embed"
	"fmt"
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
}

func (a *Agent) DidEscalate() bool { return a.escalated }
func (a *Agent) ClearEscalated()   { a.escalated = false }

// MenuSent indica si en el último mensaje se envió un menú interactivo (para que el
// llamador no mande un texto adicional). ClearMenuSent lo resetea.
func (a *Agent) MenuSent() bool  { return a.menuSent }
func (a *Agent) ClearMenuSent()  { a.menuSent = false }

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

	// registrar_pedido: las coordenadas NO son parámetros del modelo; se toman de la
	// ubicación que el cliente compartió por WhatsApp (las inyecta runTool), para no inventarlas.
	// El "color" debe ser uno de los colores/marcas listados en la INFORMACIÓN DEL SERVICIO.
	registrar := &genai.FunctionDeclaration{
		Name: "registrar_pedido",
		Description: "Registra un pedido de gas en el sistema de la distribuidora. Úsala solo cuando ya " +
			"recopilaste TODOS los datos del cliente (cédula, nombre, correo, color/marca y cantidad) Y el " +
			"cliente compartió su ubicación de WhatsApp. No la uses para consultas; solo para concretar un pedido.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"identificacion":     {Type: genai.TypeString, Description: "Cédula o identificación del cliente. Si el cliente ya está registrado (ver DATOS DEL CLIENTE), no hace falta volver a pedirla."},
				"nombres_completos":  {Type: genai.TypeString, Description: "Nombres y apellidos del cliente. Si el cliente ya está registrado, no hace falta volver a pedirlos."},
				"correo_electronico": {Type: genai.TypeString, Description: "Correo electrónico del cliente. Si el cliente ya está registrado, no hace falta volver a pedirlo."},
				"direccion":          {Type: genai.TypeString, Description: "Dirección de entrega en texto (opcional). NO es necesaria: la entrega se guía por la ubicación GPS de WhatsApp. Si el cliente no la da, se usa la ubicación compartida."},
				"referencia":         {Type: genai.TypeString, Description: "Referencia del domicilio para que el repartidor ubique mejor (opcional; ej: color de casa, local cercano)."},
				"color":              {Type: genai.TypeString, Description: "Color/marca del cilindro que desea el cliente. Debe coincidir con uno de los colores disponibles en la INFORMACIÓN DEL SERVICIO."},
				"cantidad":           {Type: genai.TypeInteger, Description: "Cantidad de cilindros solicitados."},
				"telefono":           {Type: genai.TypeString, Description: "Teléfono del cliente. Si no lo indica, se usa su número de WhatsApp."},
				"guardar_direccion_como": {Type: genai.TypeString, Description: "Nombre con el que se guardará la dirección (ej: Casa, Trabajo, Depa, Local). OBLIGATORIO cuando el cliente comparte una ubicación NUEVA: no registres un pedido a una ubicación nueva sin este nombre. Se ignora si el pedido usa una dirección guardada (id_direccion_guardada)."},
				"id_direccion_guardada":  {Type: genai.TypeInteger, Description: "ID de una dirección YA GUARDADA del cliente (de ver_direcciones_guardadas) a la que enviar el pedido. Si lo usas, NO hace falta la ubicación de WhatsApp. Déjalo vacío/0 si el cliente comparte una ubicación nueva."},
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

	// ver_direcciones_guardadas: para un cliente YA registrado, trae sus direcciones guardadas
	// (con su nombre) para ofrecérselas y que elija a cuál enviar sin re-compartir ubicación.
	verDirecciones := &genai.FunctionDeclaration{
		Name: "ver_direcciones_guardadas",
		Description: "Devuelve las direcciones que el cliente YA tiene guardadas (con su nombre/alias). Úsala cuando " +
			"un cliente ya registrado va a pedir, ANTES de pedirle la ubicación: ofrécele elegir una de sus " +
			"direcciones guardadas (para no compartir ubicación de nuevo) o mandar una ubicación nueva.",
		Parameters: &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}},
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

	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{escalar, registrar, verificarCliente, calificar, verDirecciones, mostrarMenu}}}

	return &Agent{cfg: cfg, client: client, store: store, catalog: catalogClient, gr: grClient, tools: tools}, nil
}

// HandleMessage procesa un mensaje del cliente y devuelve la respuesta para WhatsApp.
func (a *Agent) HandleMessage(ctx context.Context, from, text string) (string, error) {
	a.menuSent = false // se pone en true si la IA envía un menú interactivo en este turno
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
			"repartidor %s. Si el cliente te da una calificación del 1 al 5 (y opcionalmente un comentario), llama a "+
			"calificar_conductor con esos datos. Si aún no la ha dado, pídele con amabilidad que califique del 1 al 5 "+
			"a su repartidor. No insistas si prefiere no calificar.", rating.Conductor)
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
			continue // vuelve a llamar al modelo con los resultados
		}

		reply = strings.TrimSpace(resp.Text())
		break
	}

	if reply == "" {
		reply = "Disculpa, no pude procesar tu mensaje. ¿Podrías reformularlo?"
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
				}
			}
			// Enviara o no el menú, no dejamos el JSON crudo en el texto que se guarda/manda.
			reply = mensaje
		}
	}

	a.store.AppendUser(from, text)
	a.store.AppendModel(from, reply)
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
		return escalation.NotifyOwner(a.cfg, from, motivo, resumen)

	case "verificar_cliente":
		return a.verificarCliente(from, args)

	case "ver_direcciones_guardadas":
		return a.verDireccionesGuardadas(from)

	case "mostrar_menu":
		return a.mostrarMenu(from, args)

	case "calificar_conductor":
		return a.calificarConductor(from, args)

	case "registrar_pedido":
		result := a.registrarPedido(from, args)
		if strings.Contains(result, "dueño") || strings.Contains(result, "Deriva") {
			a.escalated = true
		}
		return result
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
		return "No se pudo verificar al cliente en este momento; continúa pidiéndole su nombre y correo para registrarlo."
	}
	if !info.Existe {
		return "El cliente NO está registrado todavía. Continúa el registro: pídele su nombre completo y su correo electrónico."
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

// registrarPedido ejecuta la secuencia real de georoutes: mapea el color a IDs de
// producto, asegura la cuenta del cliente, hace login, registra la dirección desde la
// ubicación de WhatsApp y crea el pedido. Devuelve un texto para que el modelo responda.
func (a *Agent) registrarPedido(from string, args map[string]any) string {
	cantidad := toInt(args["cantidad"])
	if cantidad <= 0 {
		return "Falta una cantidad válida de cilindros. Pregúntale al cliente cuántos desea."
	}

	// La ubicación del pedido puede venir de una dirección YA GUARDADA que el cliente eligió,
	// o de la ubicación compartida por WhatsApp. Solo exigimos ubicación si NO eligió una guardada.
	idDireccionGuardada := toInt(args["id_direccion_guardada"])
	usarGuardada := idDireccionGuardada > 0
	loc, hasLoc := a.store.GetLocation(from)
	if !usarGuardada && !hasLoc {
		return "Aún no tengo la ubicación del cliente y es obligatoria para asignar un repartidor. " +
			"Pídele que comparta su ubicación de WhatsApp, o que elija una de sus direcciones guardadas."
	}

	identificacion := strings.TrimSpace(str(args["identificacion"]))
	nombres := strings.TrimSpace(str(args["nombres_completos"]))
	correo := strings.TrimSpace(str(args["correo_electronico"]))
	direccion := strings.TrimSpace(str(args["direccion"]))
	referencia := strings.TrimSpace(str(args["referencia"]))
	colorNombre := strings.TrimSpace(str(args["color"]))
	telefono := strings.TrimSpace(str(args["telefono"]))
	if telefono == "" {
		telefono = from
	}
	// Nombre con el que el cliente quiere guardar la ubicación NUEVA (ej: Casa, Trabajo). Se usa
	// tanto para la primera dirección de un cliente nuevo (creada junto con la cuenta) como para
	// las direcciones creadas después, para que NINGUNA quede con el alias genérico "WhatsApp".
	nombreDireccion := strings.TrimSpace(str(args["guardar_direccion_como"]))
	aliasDireccion := "WhatsApp"
	if nombreDireccion != "" {
		aliasDireccion = "WhatsApp - " + nombreDireccion
	}
	// Cliente recurrente: si la IA no repitió los datos personales, los tomamos del perfil
	// guardado. Así un cliente que ya pidió no tiene que dar cédula/nombre/correo otra vez.
	if perfil, ok := a.store.GetProfile(from); ok {
		if identificacion == "" {
			identificacion = perfil.Identificacion
		}
		if nombres == "" {
			nombres = perfil.Nombres
		}
		if correo == "" {
			correo = perfil.Correo
		}
	}
	// La dirección de texto es opcional: la entrega se guía por el GPS de WhatsApp. Si el
	// cliente no la dio, la rellenamos con la ubicación compartida para tener un rótulo legible.
	if direccion == "" && hasLoc {
		direccion = fmt.Sprintf("Ubicación compartida por WhatsApp (%.6f, %.6f)", loc.Latitude, loc.Longitude)
	}
	if identificacion == "" || nombres == "" || correo == "" {
		return "Faltan datos del cliente (cédula, nombre o correo). Pídeselos antes de registrar el pedido."
	}

	// Persistimos el perfil en cuanto tenemos sus datos completos, ANTES de intentar el
	// pedido. Así, si el pedido falla por cobertura o cualquier motivo, un cliente que ya
	// dio cédula/nombre/correo no tiene que repetirlos en el próximo intento.
	if _, ya := a.store.GetProfile(from); !ya {
		a.store.SetProfile(from, conversation.Profile{
			Identificacion: identificacion,
			Nombres:        nombres,
			Correo:         correo,
		})
	}

	// Catálogo: mapear el color elegido a (producto, color) y elegir la forma de pago.
	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		return "No puedo consultar el catálogo en este momento. Discúlpate con el cliente y deriva al dueño."
	}
	producto, color, ok := findProductByColor(contexto.Products, colorNombre)
	if !ok {
		return fmt.Sprintf("El color/marca \"%s\" no está disponible. Colores disponibles: %s. "+
			"Pregúntale al cliente cuál desea.", colorNombre, availableColors(contexto.Products))
	}
	idtipopago, ok := defaultPaymentID(contexto.Payments)
	if !ok {
		return "No hay una forma de pago configurada en el sistema. Deriva al dueño."
	}

	// Cuenta georoutes del cliente (recuperar de la caché local, o crear/recuperar del backend).
	account, ok := a.store.GetAccount(from)
	if !ok {
		// Cliente nuevo: su PRIMERA dirección se crea junto con la cuenta y será la que use el
		// pedido (la reutilización de cercana la encontrará). Por eso también debe llevar nombre:
		// si es una ubicación nueva sin nombre, lo pedimos ANTES de crear la cuenta.
		if !usarGuardada && nombreDireccion == "" {
			return mensajePedirNombreDireccion
		}
		nueva, err := a.ensureAccount(identificacion, nombres, telefono, correo, direccion, referencia, aliasDireccion, loc)
		if err != nil {
			return "No se pudo registrar la cuenta del cliente (motivo: " + err.Error() + "). " +
				"Informa al cliente y deriva al dueño."
		}
		account = nueva
		a.store.SetAccount(from, account)
	}

	// Login → JWT.
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		return "No se pudo autenticar al cliente (motivo: " + err.Error() + "). Deriva al dueño."
	}

	// Guardar JWT y refresh en Account para reutilizar en próximos pedidos.
	account.JWT = tokens.Access
	account.Refresh = tokens.Refresh
	a.store.SetAccount(from, account)

	// Si el cliente no está verificado, solicitamos el código OTP y pausamos el pedido.
	if !tokens.EstaValidado {
		if err := a.gr.GetVerificationCode(tokens.Access); err != nil {
			return "No se pudo enviar el código de verificación al correo del cliente " +
				"(motivo: " + err.Error() + "). Deriva al dueño."
		}
		a.store.SetPendingVerification(from, account)
		// Guardamos el pedido recopilado para RETOMARLO automáticamente cuando el cliente
		// valide el código, sin depender de que la IA recuerde el color/cantidad.
		a.store.SetOrderDraft(from, conversation.OrderDraft{Color: colorNombre, Cantidad: cantidad})
		return "Por seguridad, hemos enviado un código de verificación al correo electrónico del cliente. " +
			"Pídele que revise su bandeja de entrada (y spam) y que te comparta el código por WhatsApp para " +
			"poder procesar el pedido. Cuando lo tenga, dímelo y lo valido."
	}

	// Dirección del pedido. Igual que la app: primero buscamos si el cliente ya tiene una
	// dirección guardada CERCANA a esta ubicación (nearby-direction, tolerancia del backend);
	// si existe, la reutilizamos en vez de crear una nueva cada vez. Solo si no hay ninguna
	// cercana creamos una. Si la consulta de cercanía falla técnicamente, caemos a crear
	// (no bloqueamos el pedido por eso).
	var direccionID int
	switch {
	case usarGuardada:
		// El cliente eligió una de sus direcciones guardadas: la usamos tal cual.
		direccionID = idDireccionGuardada
	default:
		// Ubicación nueva: reutilizamos una dirección cercana si existe; si no, creamos una.
		if cercana, errNear := a.gr.NearbyDirection(tokens.Access, loc.Latitude, loc.Longitude); errNear == nil && cercana.Existe && cercana.IDDireccion != nil {
			direccionID = *cercana.IDDireccion
		} else {
			// Ubicación nueva: nombrar la dirección es OBLIGATORIO (no debe quedar genérica en la BD).
			// Si el modelo aún no recopiló el nombre, no registramos: pedimos que lo pregunte primero.
			if nombreDireccion == "" {
				return mensajePedirNombreDireccion
			}
			direccionCreada, err := a.gr.CreateDirection(tokens.Access, georoutes.DirectionInput{
				Direccion:  direccion,
				Alias:      aliasDireccion,
				Referencia: referencia,
				Latitude:   loc.Latitude,
				Longitude:  loc.Longitude,
			})
			if err != nil {
				if esFalloDeCobertura(err.Error()) {
					return mensajeSinCobertura
				}
				return "No se pudo registrar la dirección del pedido (motivo: " + err.Error() + "). " +
					"Informa al cliente del inconveniente y deriva al dueño para atención manual."
			}
			direccionID = direccionCreada.ID
		}
	}

	// Pedido en el flujo real.
	resultado, err := a.gr.StartOrder(tokens.Access, direccionID, idtipopago, []georoutes.OrderProduct{{
		IDCategoria: producto.IDCategoria,
		IDProducto:  producto.IDProducto,
		IDColor:     color.ID,
		Cantidad:    cantidad,
	}})
	if err != nil {
		// Sin repartidores en la zona / fuera de cobertura NO es un error técnico: es una
		// condición normal de negocio. Se informa con un mensaje claro y NO se deriva al
		// dueño ni se entra en bucle; la conversación se cierra cordialmente.
		if esFalloDeCobertura(err.Error()) {
			return mensajeSinCobertura
		}
		return "No se pudo registrar el pedido (motivo: " + err.Error() + "). " +
			"Informa al cliente del inconveniente y deriva al dueño para atención manual."
	}

	// Guardar el perfil del cliente para no volver a pedir estos datos en pedidos futuros.
	a.store.SetProfile(from, conversation.Profile{
		Identificacion: identificacion,
		Nombres:        nombres,
		Correo:         correo,
	})

	// Recordar con qué teléfono de WhatsApp se hizo este pedido, para contactar al cliente
	// por el número correcto cuando el backend avise que se entregó (calificación).
	if resultado.IDPedido > 0 {
		a.store.SetOrderPhone(resultado.IDPedido, from)
	}

	// Guardar el resumen del pedido para poder ofrecerle repetir lo mismo la próxima vez.
	a.store.SetLastOrder(from, conversation.LastOrder{
		Producto: producto.Nombre,
		Color:    color.Nombre,
		Cantidad: cantidad,
		Fecha:    time.Now().Format("02/01/2006"),
	})

	// Recordar con qué teléfono de WhatsApp se hizo este pedido, para poder pedirle la
	// calificación por el número correcto cuando el backend avise que se entregó.
	if resultado.IDPedido > 0 {
		a.store.SetOrderPhone(resultado.IDPedido, from)
	}

	// Pedido exitoso: limpiamos historial para que el proximo arranque fresco.
	a.store.ClearHistory(from)

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
			fmt.Fprintf(&texto, "- %s: $%.2f", producto.Nombre, producto.PrecioUnitario)
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

	texto.WriteString("\nPara concretar un pedido necesitas del cliente: cédula, nombre completo, correo, " +
		"el color/marca deseado, la cantidad y su ubicación de WhatsApp (📎 → Ubicación).\n")
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
