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
	"fmt"
	"math"
	"strings"

	"google.golang.org/genai"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/escalation"
	"wp-llm-gas/internal/georoutes"
)

const maxToolRounds = 5

// behaviorPrompt son las instrucciones de comportamiento del agente. Viven en un archivo
// de plantilla (contenido, no lógica) embebido en el binario, para editarlas sin tocar código.
//
//go:embed prompts/behavior.md
var behaviorPrompt string

// Agent orquesta las llamadas a Gemini y la ejecución de herramientas.
type Agent struct {
	cfg     config.Config
	client  *genai.Client
	store   conversation.Store
	catalog *catalog.Client
	gr      *georoutes.Client
	tools   []*genai.Tool
}

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
				"identificacion":     {Type: genai.TypeString, Description: "Cédula o identificación del cliente (obligatoria)."},
				"nombres_completos":  {Type: genai.TypeString, Description: "Nombres y apellidos del cliente (obligatorio)."},
				"correo_electronico": {Type: genai.TypeString, Description: "Correo electrónico del cliente (obligatorio)."},
				"direccion":          {Type: genai.TypeString, Description: "Dirección de entrega (obligatoria)."},
				"referencia":         {Type: genai.TypeString, Description: "Referencia del domicilio (opcional)."},
				"color":              {Type: genai.TypeString, Description: "Color/marca del cilindro que desea el cliente. Debe coincidir con uno de los colores disponibles en la INFORMACIÓN DEL SERVICIO."},
				"cantidad":           {Type: genai.TypeInteger, Description: "Cantidad de cilindros solicitados."},
				"telefono":           {Type: genai.TypeString, Description: "Teléfono del cliente. Si no lo indica, se usa su número de WhatsApp."},
			},
			Required: []string{"identificacion", "nombres_completos", "correo_electronico", "direccion", "color", "cantidad"},
		},
	}

	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{escalar, registrar}}}

	return &Agent{cfg: cfg, client: client, store: store, catalog: catalogClient, gr: grClient, tools: tools}, nil
}

// HandleMessage procesa un mensaje del cliente y devuelve la respuesta para WhatsApp.
func (a *Agent) HandleMessage(ctx context.Context, from, text string) (string, error) {
	contents := append(a.store.History(from), &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: text}},
	})

	// El system prompt se arma en cada mensaje: comportamiento (estático) + información
	// del servicio (dinámica, traída del backend con caché). Así reflejamos cambios de
	// productos, colores o precios sin reiniciar el agente.
	contexto, disponible := a.catalog.Get()
	systemPrompt := strings.TrimSpace(behaviorPrompt) + "\n\nINFORMACIÓN DEL SERVICIO:\n" + renderServiceInfo(contexto, disponible)

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
	a.store.AppendUser(from, text)
	a.store.AppendModel(from, reply)
	return reply, nil
}

func (a *Agent) runTool(from, name string, args map[string]any) string {
	switch name {
	case "escalar_al_dueno":
		motivo, _ := args["motivo"].(string)
		resumen, _ := args["resumen"].(string)
		return escalation.NotifyOwner(a.cfg, from, motivo, resumen)

	case "registrar_pedido":
		return a.registrarPedido(from, args)
	}
	return "Función desconocida: " + name
}

// registrarPedido ejecuta la secuencia real de georoutes: mapea el color a IDs de
// producto, asegura la cuenta del cliente, hace login, registra la dirección desde la
// ubicación de WhatsApp y crea el pedido. Devuelve un texto para que el modelo responda.
func (a *Agent) registrarPedido(from string, args map[string]any) string {
	loc, ok := a.store.GetLocation(from)
	if !ok {
		return "Aún no tengo la ubicación del cliente y es obligatoria para asignar un repartidor. " +
			"Pídele que comparta su ubicación de WhatsApp (📎 → Ubicación) y vuelve a intentarlo."
	}

	cantidad := toInt(args["cantidad"])
	if cantidad <= 0 {
		return "Falta una cantidad válida de cilindros. Pregúntale al cliente cuántos desea."
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
	if identificacion == "" || nombres == "" || correo == "" || direccion == "" {
		return "Faltan datos del cliente (cédula, nombre, correo o dirección). Pídeselos antes de registrar el pedido."
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
		nueva, err := a.ensureAccount(identificacion, nombres, telefono, correo, direccion, referencia, loc)
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

	// Dirección del pedido desde la ubicación compartida.
	direccionCreada, err := a.gr.CreateDirection(tokens.Access, georoutes.DirectionInput{
		Direccion:  direccion,
		Alias:      "WhatsApp",
		Referencia: referencia,
		Latitude:   loc.Latitude,
		Longitude:  loc.Longitude,
	})
	if err != nil {
		return "No se pudo registrar la dirección del pedido (motivo: " + err.Error() + "). " +
			"Puede estar fuera de la zona de cobertura; informa al cliente y deriva al dueño."
	}

	// Pedido en el flujo real.
	resultado, err := a.gr.StartOrder(tokens.Access, direccionCreada.ID, idtipopago, []georoutes.OrderProduct{{
		IDCategoria: producto.IDCategoria,
		IDProducto:  producto.IDProducto,
		IDColor:     color.ID,
		Cantidad:    cantidad,
	}})
	if err != nil {
		return "No se pudo registrar el pedido (motivo: " + err.Error() + "). " +
			"Informa al cliente del inconveniente y deriva al dueño para atención manual."
	}

	mensaje := fmt.Sprintf("Pedido registrado correctamente: %d x %s (%s).", cantidad, producto.Nombre, color.Nombre)
	if resultado.ConductorAsignado != "" {
		mensaje += " Repartidor asignado: " + resultado.ConductorAsignado + "."
	}
	return mensaje
}

// ensureAccount recupera del backend la cuenta del cliente por su identificación, o la
// crea si no existe. Devuelve las credenciales generadas por el backend.
func (a *Agent) ensureAccount(identificacion, nombres, telefono, correo, direccion, referencia string, loc conversation.Location) (conversation.Account, error) {
	if existente, err := a.gr.UserExists(identificacion); err == nil && existente.Username != "" {
		return conversation.Account{Username: existente.Username, Password: existente.Password}, nil
	}

	creada, err := a.gr.CreateUser(georoutes.NewClientInput{
		Identificacion: identificacion,
		Nombres:        nombres,
		Telefono:       telefono,
		Correo:         correo,
		Direccion:      direccion,
		Alias:          "WhatsApp",
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
