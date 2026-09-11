// Flujo de PEDIDO del agente: registrar el pedido por georoutes (cuenta, login, geocerca,
// startOrder), cancelarlo, y la espera de conductor cuando no hay uno disponible (reintento
// de 5 min en segundo plano). Es el corazón del negocio; agent.go solo lo despacha.
package agent

import (
	"fmt"
	"log"
	"strings"
	"time"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/notify"
	"wp-llm-gas/internal/whatsapp"
)

// resultadoPedido es el desenlace de un registrar_pedido, para quien lo llame desde codigo.
type resultadoPedido struct {
	ok          bool // el pedido se creo en el backend
	enEspera    bool // se creo pero no habia repartidor: quedo en cola (PendingWait)
	IDPedido    int
	Conductor   string
	Placa       string
	Producto    string
	Color       string
	Cantidad    int
	TotalPagar  float64 // valor a pagar (incluye envio/instalacion/servicio)
	Seguimiento string  // URL pública de seguimiento en vivo (vacía si el backend no la envió)
	// Lineas es el pedido completo cuando tuvo varios colores (Fase C1). Con un solo color va
	// vacía y valen Producto/Color/Cantidad, como siempre.
	Lineas []lineaPedido
}

// lineaPedido es una línea ya resuelta contra el catálogo, con lo necesario para el backend
// (IDs) y para hablarle al cliente (nombres).
type lineaPedido struct {
	Producto georoutes.Product
	Color    georoutes.Color
	Cantidad int
}

// Topes del pedido multicolor (spec C1.3): por encima de esto casi seguro es un mayorista y
// merece una persona, no un bot.
const (
	maxColoresPorPedido   = 3
	maxCilindrosPorPedido = 10
)

// describeLineas arma el texto humano de un pedido: "1 x GAS 15KG (BLANCO) + 2 x GAS 15KG
// (AMARILLO)". Con una sola línea queda como el texto de siempre.
func describeLineas(lineas []lineaPedido) string {
	partes := make([]string, len(lineas))
	for i, l := range lineas {
		partes[i] = fmt.Sprintf("%d x %s (%s)", l.Cantidad, l.Producto.Nombre, l.Color.Nombre)
	}
	return strings.Join(partes, " + ")
}

// totalPagarDe suma el precio real (unitario + envío + instalación + servicio) de todas las
// líneas. El total del backend en wppOrder viene incompleto (solo total_productos).
func totalPagarDe(lineas []lineaPedido) float64 {
	var total float64
	for _, l := range lineas {
		total += l.Producto.PrecioTotal() * float64(l.Cantidad)
	}
	return total
}

// argsDeLineas es el inverso de lineasDeArgs: arma los argumentos de registrar_pedido a partir
// de líneas ya conocidas (los interceptores en código las tienen como []ItemPedido). Con una
// sola línea produce el color+cantidad de siempre; con varias, la lista items. `extra` añade
// argumentos adicionales (p. ej. direccion_confirmada).
func argsDeLineas(items []conversation.ItemPedido, extra map[string]any) map[string]any {
	args := map[string]any{}
	for k, v := range extra {
		args[k] = v
	}
	if len(items) == 1 {
		args["color"] = items[0].Color
		args["cantidad"] = items[0].Cantidad
		return args
	}
	lista := make([]any, len(items))
	for i, it := range items {
		lista[i] = map[string]any{"color": it.Color, "cantidad": float64(it.Cantidad)}
	}
	args["items"] = lista
	return args
}

// lineasDeArgs saca las líneas del pedido de los argumentos de registrar_pedido: la lista
// "items" si vino (pedido multicolor), o el par color+cantidad de siempre. Devuelve las
// líneas SIN resolver contra el catálogo (solo texto), en el orden en que llegaron.
func lineasDeArgs(args map[string]any) []conversation.ItemPedido {
	var lineas []conversation.ItemPedido
	if raw, ok := args["items"].([]any); ok {
		for _, v := range raw {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			c := strings.TrimSpace(str(m["color"]))
			n := toInt(m["cantidad"])
			if c != "" {
				lineas = append(lineas, conversation.ItemPedido{Color: c, Cantidad: n})
			}
		}
	}
	if len(lineas) > 0 {
		return lineas
	}
	c := strings.TrimSpace(str(args["color"]))
	n := toInt(args["cantidad"])
	if c == "" && n <= 0 {
		return nil
	}
	return []conversation.ItemPedido{{Color: c, Cantidad: n}}
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
		// "El pedido ya fue cancelado" NO es un fallo: el objetivo del cliente ya se cumplió (lo
		// canceló el conductor desde su app, cosa que pasa seguido). Se trata como éxito —limpiar
		// el estado y confirmar— para no gritar "sigue vivo" por Telegram cuando en realidad está
		// muerto. El backend solo deja cancelar un pedido EN CAMINO; cualquier otro estado da esto.
		if yaEstabaCancelado(err.Error()) {
			a.store.ClearActivePedido(from)
			a.store.ClearPedidoEnCurso(from)
			return "El pedido del cliente YA estaba cancelado (lo canceló el conductor). No es un error: " +
				"confírmale con amabilidad que su pedido está cancelado y ofrécele hacer uno nuevo cuando quiera."
		}
		return "No se pudo cancelar el pedido (motivo: " + err.Error() + "). Discúlpate y dile que en un momento lo revisa el equipo."
	}
	a.store.ClearActivePedido(from)
	a.store.ClearPedidoEnCurso(from)
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

// avisarSinRepartidor manda al grupo de Telegram el pedido que quedó sin conductor. No es un
// error -el bot buscó, no encontró y se lo dijo al cliente- sino un cliente con ubicación,
// producto y cantidad que NADIE va a atender salvo que una persona lo gestione. Antes solo
// quedaba marcado en el panel, donde había que estar mirando: el 02/09 un cliente esperó los 5
// minutos, se le dijo que no había repartidor y su pedido murió ahí sin que nadie se enterara.
// orderProductsDeWait arma las líneas del backend desde una espera (todas si fue multicolor).
func orderProductsDeWait(w conversation.PendingWait) []georoutes.OrderProduct {
	lineas := w.Lineas()
	productos := make([]georoutes.OrderProduct, len(lineas))
	for i, l := range lineas {
		productos[i] = georoutes.OrderProduct{
			IDCategoria: l.IDCategoria, IDProducto: l.IDProducto,
			IDColor: l.IDColor, Cantidad: l.Cantidad,
		}
	}
	return productos
}

func (a *Agent) avisarSinRepartidor(from string, w conversation.PendingWait, motivo string) {
	partes := make([]string, 0, 2)
	for _, l := range w.Lineas() {
		partes = append(partes, fmt.Sprintf("%d × %s %s", l.Cantidad, l.ProductoNombre, l.ColorNombre))
	}
	pedido := strings.Join(partes, " + ")
	nombre := strings.TrimSpace(w.Nombres)
	if nombre == "" {
		if p, ok := a.store.GetProfile(from); ok {
			nombre = strings.TrimSpace(p.Nombres)
		}
	}
	var lat, lng float64
	if loc, ok := a.store.GetLocation(from); ok {
		lat, lng = loc.Latitude, loc.Longitude
	}
	notify.Default.SinRepartidor(from, nombre, pedido, motivo, lat, lng)
}

// cancelarEspera descarta el pedido en espera cuando el cliente NO quiere esperar. Antes de
// descartarlo lo registra como NO ASIGNADO en el backend, para gestión manual.
func (a *Agent) cancelarEspera(from string) string {
	if w, ok := a.store.GetPendingWait(from); ok {
		a.avisarSinRepartidor(from, w, "El cliente NO quiso esperar. Quedó en No asignados.")
	}
	a.registrarNoAsignado(from)
	a.store.ClearPendingWait(from)
	a.store.ClearPedidoEnCurso(from) // el pedido ya quedó en gestión manual: la ficha no aplica
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
		a.crearTicketSoporte(from, "Pedido sin conductor no quedó registrado",
			fmt.Sprintf("Falló el login para guardarlo como NO ASIGNADO: %v. El pedido no está en ninguna lista; hay que contactar al cliente.", err))
		return
	}
	if err := a.gr.WppRegistrarPedidoNoAsignado(tokens.Access, loc.Latitude, loc.Longitude, w.IDTipoPago,
		orderProductsDeWait(w)); err != nil {
		a.crearTicketSoporte(from, "Pedido sin conductor no quedó registrado",
			fmt.Sprintf("El backend rechazó guardarlo como NO ASIGNADO: %v. El pedido no está en ninguna lista; hay que contactar al cliente.", err))
	}
}

// ventanaPedidoActivo es lo máximo que un pedido puede seguir "en curso" antes de considerarlo
// huérfano. Una entrega real se resuelve en minutos; 6 horas es holgado para cualquier demora
// legítima y evita que un aviso perdido del backend bloquee al cliente para siempre.
const ventanaPedidoActivo = 6 * time.Hour

// startWaitForDriver corre en segundo plano: reintenta la asignación cada 30s durante 5 min. En
// WhatsApp NO sirve el push (token placeholder), por eso el bot reintenta activamente. Al asignarse
// o al expirar, envía el mensaje directo por WhatsApp. Best-effort: nunca tumba el proceso.
func (a *Agent) startWaitForDriver(from string, w conversation.PendingWait) {
	// IDEMPOTENTE: una sola búsqueda por cliente. El cliente ansioso que escribe "ok", "dale",
	// "👍" mientras espera disparaba una goroutine por mensaje, y cada una llama a WppOrder por
	// su cuenta: si dos aciertan, el cliente termina con dos pedidos y dos conductores. El guard
	// de registrarPedido no protege aquí, porque esta goroutine va directo al backend.
	if _, yaBuscando := a.buscandoRepartidor.LoadOrStore(from, true); yaBuscando {
		log.Printf("[espera] %s ya tiene una búsqueda de repartidor en curso; no se abre otra", from)
		return
	}
	// Contador de arranques: la búsqueda es asíncrona y no deja marca inmediata en el store,
	// así que sin esto un test no puede distinguir "se arrancó la búsqueda" de "solo se le dijo
	// al cliente que se arrancó" —que es justo el bug que los interceptores previenen—.
	a.esperasArrancadas.Add(1)
	cfg := a.cfg
	gr := a.gr
	store := a.store
	go func() {
		defer a.buscandoRepartidor.Delete(from) // libera el candado pase lo que pase
		defer func() {
			if r := recover(); r != nil {
				// El cliente aceptó esperar: si esta goroutine muere, nadie busca su repartidor
				// y él sigue esperando un aviso que no va a llegar.
				log.Printf("[espera] panic recuperado para %s: %v", from, r)
				notify.ReportarFallo(cfg, store, from, "Panic buscando repartidor",
					fmt.Sprintf("La búsqueda de repartidor murió con panic: %v. El cliente quedó esperando.", r))
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
						orderProductsDeWait(w))
					if err == nil {
						// ¡Asignado! Guardar estado igual que un pedido normal y avisar al cliente.
						store.SetProfile(from, conversation.Profile{Identificacion: w.Identificacion, Nombres: w.Nombres})
						if res.IDPedido > 0 {
							store.SetOrderPhone(res.IDPedido, from)
							store.SetActivePedido(from, res.IDPedido)
						}
						// Se guarda TAMBIÉN a dónde va, para que "repetir lo mismo" pueda decirlo.
						// Aquí no se consultan las direcciones del backend (esto corre en una
						// goroutine, minutos después): se usa lo que ya se sabe del cliente.
						calle, _ := store.GetDireccionTexto(from)
						nombre := ""
						if anterior, hay := store.GetLastOrder(from); hay &&
							mismaUbicacion(anterior.Latitude, anterior.Longitude, loc.Latitude, loc.Longitude) {
							nombre = anterior.Alias
						}
						var lastItems []conversation.ItemPedido
						if lineas := w.Lineas(); len(lineas) > 1 {
							lastItems = make([]conversation.ItemPedido, len(lineas))
							for i, l := range lineas {
								lastItems[i] = conversation.ItemPedido{Color: l.ColorNombre, Cantidad: l.Cantidad}
							}
						}
						store.SetLastOrder(from, conversation.LastOrder{
							Producto: w.ProductoNombre, Color: w.ColorNombre, Cantidad: w.Cantidad,
							Items:     lastItems,
							Fecha:     time.Now().Format("02/01/2006"),
							Latitude:  loc.Latitude,
							Longitude: loc.Longitude,
							Alias:     nombre,
							Direccion: calle,
						})
						store.ClearPendingWait(from)
						// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
						msg := "🎉 ¡Listo! Ya tienes un repartidor asignado"
						if res.ConductorAsignado != "" {
							msg += ": " + res.ConductorAsignado
						}
						msg += ". Sale con tu pedido en breve. ¡Gracias por tu espera!"
						// Si venia de una entrega agendada, deja de estar "buscando repartidor".
						store.CerrarProgramadoEnEspera(from, true)
						if err := whatsapp.SendText(cfg, from, msg); err == nil {
							store.LogMessage(from, "system", msg)
							// Tambien a la memoria del modelo: si el cliente responde a este aviso,
							// la IA tiene que saber que se lo mandamos.
							store.AppendModel(from, msg)
						}
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
						orderProductsDeWait(w)); err != nil {
						log.Printf("[no-asignado] registro (timeout) falló para %s: %v", from, err)
					}
				}
			}
		}
		// Al grupo ANTES de limpiar el estado: es cuando todavía se tiene el pedido completo.
		a.avisarSinRepartidor(from, w, "El cliente esperó los 5 minutos y no se le asignó nadie.")
		store.ClearPendingWait(from)
		// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
		msgTimeout := "Te pedimos disculpas 🙏. Por ahora no hay ningún repartidor disponible " +
			"para asignar tu pedido. Intenta más tarde, con gusto te ayudamos."
		// Se acabaron los 5 minutos sin nadie: el pedido queda en No asignados y la entrega
		// agendada lo refleja, en vez de quedarse en "buscando" para siempre.
		store.CerrarProgramadoEnEspera(from, false)
		if err := whatsapp.SendText(cfg, from, msgTimeout); err == nil {
			store.LogMessage(from, "system", msgTimeout)
			// Tambien a la memoria del modelo: si el cliente responde a este aviso,
			// la IA tiene que saber que se lo mandamos.
			store.AppendModel(from, msgTimeout)
		}
	}()
}

func (a *Agent) registrarPedido(t *turno, from string, args map[string]any) string {
	// REGLA DURA de horario: fuera del horario laboral NO se registran pedidos (no hay
	// conductores). El prompt también lo dice; esto es la garantía en código.
	if !a.dentroDeHorario(time.Now().In(zonaEcuador)) {
		return "FUERA DE HORARIO (" + a.cfg.BotHorarioInicio + " a " + a.cfg.BotHorarioFin + "): NO se registró " +
			"el pedido porque a esta hora no hay conductores. Explícaselo al cliente con amabilidad y ofrécele " +
			"PROGRAMAR la entrega con la herramienta programar_entrega."
	}
	// Líneas del pedido: varias si el cliente pidió más de un color (items), una si no. TODAS
	// completas o no se registra nada: un pedido a medias es peor que preguntar (spec C1.3).
	pedido := lineasDeArgs(args)
	if len(pedido) == 0 {
		return "Falta el color y la cantidad del pedido. Pregúntale al cliente qué cilindro desea."
	}
	for _, l := range pedido {
		if l.Cantidad <= 0 {
			return fmt.Sprintf("Falta la cantidad de cilindros %s. Pregúntale al cliente cuántos "+
				"de ese color desea; NO registres nada hasta tenerla.", l.Color)
		}
	}
	// Topes del multicolor: por encima casi seguro es un mayorista; lo atiende una persona.
	totalCilindros := 0
	for _, l := range pedido {
		totalCilindros += l.Cantidad
	}
	if len(pedido) > maxColoresPorPedido || totalCilindros > maxCilindrosPorPedido {
		t.escalado = true
		return fmt.Sprintf("El pedido es muy grande (%d colores, %d cilindros) y merece atención "+
			"personal. NO se registró. Dile al cliente con amabilidad que para pedidos grandes una "+
			"persona del equipo lo contactará enseguida, y deriva al dueño.", len(pedido), totalCilindros)
	}

	// IDEMPOTENCIA: si el cliente ya tiene un pedido activo, no se le crea otro. Pasa cuando el
	// modelo llama la herramienta dos veces en el mismo turno, o cuando un mensaje repetido
	// vuelve a disparar el flujo: el cliente terminaba con dos cilindros y dos conductores. El
	// pedido activo se limpia al entregarse o cancelarse, así que un pedido legítimo posterior
	// no se bloquea.
	if idPrevio, hay := a.store.GetActivePedido(from); hay && idPrevio > 0 {
		// El pedido activo lo limpia el backend al entregarse/cancelarse. Si ese aviso nunca
		// llega (conductor que no finaliza en su app, notificación perdida), el pedido queda
		// HUÉRFANO y este guard dejaría al cliente sin poder pedir nunca más. Pasadas unas
		// horas se asume perdido: ninguna entrega real dura tanto. Se avisa al equipo porque
		// un huérfano significa que un pedido quedó sin cerrar en el backend.
		if a.store.ActivePedidoDesde(from) > ventanaPedidoActivo {
			log.Printf("[pedido] %s: el pedido #%d lleva demasiado activo; se asume huérfano", from, idPrevio)
			notify.ReportarFallo(a.cfg, a.store, from, "Pedido activo huérfano",
				fmt.Sprintf("El pedido #%d sigue marcado como activo tras %s y el cliente quiere pedir "+
					"otra vez. Puede que nunca se cerrara en el backend; conviene revisarlo.",
					idPrevio, a.store.ActivePedidoDesde(from).Round(time.Minute)))
			a.store.ClearActivePedido(from)
		} else {
			log.Printf("[pedido] %s ya tiene el pedido #%d activo; no se crea otro", from, idPrevio)
			return fmt.Sprintf("El cliente YA tiene el pedido #%d en curso: NO se creó otro. "+
				"Confírmale que su pedido sigue en camino. Si quiere cambiarlo o pedir más, primero "+
				"hay que cancelar el actual con cancelar_pedido.", idPrevio)
		}
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

	// Catálogo: mapear CADA color pedido a (producto, color) y elegir la forma de pago. Si UN
	// color no existe, no se registra NADA: un pedido a medias es peor que preguntar (C1.3).
	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		t.escalado = true // derivación REAL (señal explícita; ya no se adivina por texto)
		return "No puedo consultar el catálogo en este momento. Discúlpate con el cliente y deriva al dueño."
	}
	lineas := make([]lineaPedido, 0, len(pedido))
	for _, l := range pedido {
		producto, color, ok := findProductByColor(contexto.Products, l.Color)
		if !ok {
			return fmt.Sprintf("El color/marca \"%s\" no está disponible: NO se registró nada del pedido. "+
				"Colores disponibles: %s. Pregúntale al cliente cuál desea.", l.Color, availableColors(contexto.Products))
		}
		lineas = append(lineas, lineaPedido{Producto: producto, Color: color, Cantidad: l.Cantidad})
	}
	idtipopago, ok := defaultPaymentID(contexto.Payments)
	if !ok {
		t.escalado = true
		return "No hay una forma de pago configurada en el sistema. Deriva al dueño."
	}
	// La primera línea encabeza los mensajes/estados que hablan de UN color (compat con el
	// pedido de siempre); todo lo que puede ser multicolor usa `lineas` completo.
	producto, color, cantidad := lineas[0].Producto, lineas[0].Color, lineas[0].Cantidad

	// GUARDIA DE DIRECCION. Si la ubicacion guardada NO es de esta conversacion, no se registra
	// nada hasta que el cliente confirme a donde va. Evita el peor error posible: que alguien
	// pida en la mañana y en la tarde, desde otro lado, y el gas salga a la direccion vieja.
	// El menu lo manda el codigo y la respuesta se resuelve en codigo (ver direccion.go).
	if args["direccion_confirmada"] != true && !a.ubicacionEsDeAhora(from) {
		if aviso, enPausa := a.pedirConfirmacionDireccionLineas(t, from, pedido); enPausa {
			return aviso
		}
		// Sin direccion legible que mostrar, lo correcto es pedirle el pin otra vez.
		a.store.ClearLocation(from)
		return "La ubicación que teníamos es de otra conversación y no sirve para entregar. " +
			"Pídele que comparta su ubicación ACTUAL por WhatsApp (botón de adjuntar → Ubicación)."
	}

	// Cuenta del bot (YA verificada, sin OTP): de la caché local, o del backend (get-or-create).
	account, ok := a.store.GetAccount(from)
	if !ok {
		nueva, err := a.gr.WppGetOrCreateClient(identificacion, nombres, telefono)
		if err != nil {
			t.escalado = true
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
			t.escalado = true
			return "No se pudo autenticar al cliente (motivo: " + err.Error() + "). Deriva al dueño."
		}
		account = conversation.Account{Username: nueva.Username, Password: nueva.Password}
		a.store.SetAccount(from, account)
		tokens, err = a.gr.Login(account.Username, account.Password)
		if err != nil {
			t.escalado = true
			return "No se pudo autenticar al cliente (motivo: " + err.Error() + "). Deriva al dueño."
		}
	}

	// Guardar JWT y refresh para reutilizar en próximos pedidos.
	account.JWT = tokens.Access
	account.Refresh = tokens.Refresh
	a.store.SetAccount(from, account)

	// ¿Esta ubicación es una que el cliente guardó con nombre ("Casa")? Si lo es, el pedido va
	// a ESA dirección: el backend la usa tal cual en vez de pisar su dirección interna de
	// WhatsApp, y así "Casa" sigue siendo Casa entre un pedido y otro. Si no, alias vacío y
	// todo se comporta como siempre.
	aliasDestino, calleDestino := a.destinoDelPedido(from, tokens.Access, loc.Latitude, loc.Longitude)

	// Pedido: con alias, el backend usa la dirección con nombre del cliente; sin él, hace
	// UPSERT de la dirección "WhatsApp" con esta ubicación. En ambos casos REUTILIZA el flujo
	// real de pedido. UNA sola llamada con TODAS las líneas: el backend crea un PedidoCabecera
	// con un PedidoDetalle por línea y exige un conductor con stock de todos los colores. Antes
	// el multicolor obligaba al modelo a llamar la tool dos veces y la segunda chocaba con la
	// idempotencia — así David (10/09) recibió "BLANCO y AMARILLO confirmados" con solo BLANCO.
	orderProducts := make([]georoutes.OrderProduct, len(lineas))
	for i, l := range lineas {
		orderProducts[i] = georoutes.OrderProduct{
			IDCategoria: l.Producto.IDCategoria,
			IDProducto:  l.Producto.IDProducto,
			IDColor:     l.Color.ID,
			Cantidad:    l.Cantidad,
		}
	}
	resultado, err := a.gr.WppOrderEnDireccion(tokens.Access, loc.Latitude, loc.Longitude, idtipopago,
		orderProducts, aliasDestino)
	if err != nil {
		// Sin repartidores / fuera de cobertura NO es error técnico: guardamos el pedido a la
		// espera y le ofrecemos al cliente esperar hasta 5 min (reintento de asignación).
		if esFalloDeCobertura(err.Error()) {
			// ANTES de ofrecer esperar: ¿hay un color EQUIVALENTE con conductor y stock reales?
			// (specs/cobertura-y-color-alterno.md). Si lo hay, se le ofrece el cambio; si la
			// consulta falla o no hay, el flujo de espera sigue exactamente como hoy. SOLO para
			// pedidos de un color: la equivalencia está pensada línea a línea y ofrecer "¿te lo
			// cambio a azul?" sobre un pedido de dos colores confundiría más de lo que ayuda.
			if len(lineas) == 1 {
				if menu, ok := a.ofrecerColorAlterno(t, from, loc.Latitude, loc.Longitude,
					producto, color, cantidad); ok {
					return menu
				}
			}
			var waitItems []conversation.PendingWaitItem
			if len(lineas) > 1 {
				waitItems = make([]conversation.PendingWaitItem, len(lineas))
				for i, l := range lineas {
					waitItems[i] = conversation.PendingWaitItem{
						IDCategoria: l.Producto.IDCategoria, IDProducto: l.Producto.IDProducto,
						IDColor: l.Color.ID, Cantidad: l.Cantidad,
						ProductoNombre: l.Producto.Nombre, ColorNombre: l.Color.Nombre,
					}
				}
			}
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
				Items:          waitItems,
			})
			t.ultimoPedido = resultadoPedido{
				enEspera: true,
				Producto: producto.Nombre,
				Color:    color.Nombre,
				Cantidad: cantidad,
				Lineas:   lineas,
			}
			return mensajeOfrecerEspera
		}
		t.escalado = true
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
	// A dónde se entregó, para que "repetir lo mismo" diga "2 Blanco a Casa" y no solo
	// "2 Blanco". Se reutiliza lo resuelto arriba (antes del pedido, para elegir la dirección)
	// en vez de volver a preguntárselo al backend.
	//
	// La calle NO se lee del store: ahí queda la de la ÚLTIMA ubicación conocida, que si el
	// cliente se movió a un sitio sin dirección registrada sería la del pedido anterior — y
	// "repetir" le ofrecería una calle a la que este pedido nunca fue.
	var lastItems []conversation.ItemPedido
	if len(lineas) > 1 {
		lastItems = make([]conversation.ItemPedido, len(lineas))
		for i, l := range lineas {
			lastItems[i] = conversation.ItemPedido{Color: l.Color.Nombre, Cantidad: l.Cantidad}
		}
	}
	a.store.SetLastOrder(from, conversation.LastOrder{
		Producto:  producto.Nombre,
		Color:     color.Nombre,
		Cantidad:  cantidad,
		Items:     lastItems,
		Fecha:     time.Now().Format("02/01/2006"),
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
		Alias:     aliasDestino,
		Direccion: calleDestino,
	})
	// La ficha ya cumplió: el pedido existe. Si no se limpiara, el próximo mensaje del cliente
	// ("gracias") seguiría arrastrando color y cantidad de un pedido que ya está en camino.
	a.store.ClearPedidoEnCurso(from)
	// Marca como CONFIRMADO cualquier pedido programado en confirmación de este cliente.
	if sch, ok := a.store.GetConfirmingSchedule(from); ok {
		a.store.SetScheduledEstado(sch.ID, conversation.ScheduleConfirmado)
	}

	seguimiento := a.urlSeguimiento(resultado.SeguimientoToken)
	// El VALOR A PAGAR se calcula con el precio del catálogo (unitario + envío + instalación +
	// servicio, ver Product.PrecioTotal) sumando TODAS las líneas, NO con resultado.Total: el
	// backend en wppOrder devuelve solo total_productos (el cilindro suelto, sin los rubros).
	totalPagar := totalPagarDe(lineas)
	t.ultimoPedido = resultadoPedido{
		ok:          true,
		IDPedido:    resultado.IDPedido,
		Conductor:   resultado.ConductorAsignado,
		Placa:       resultado.Placa,
		Producto:    producto.Nombre,
		Color:       color.Nombre,
		Cantidad:    cantidad,
		TotalPagar:  totalPagar,
		Seguimiento: seguimiento,
		Lineas:      lineas,
	}

	// Confirmación COMPLETA para el modelo (conductor + placa + valor a pagar + seguimiento). Se le
	// pasan los datos con etiquetas claras para que arme un mensaje amable; los valores van tal
	// cual. Con varias líneas, el detalle completo ("1 x ... (BLANCO) + 1 x ... (AMARILLO)").
	mensaje := fmt.Sprintf("Pedido registrado correctamente: %s. DATOS PARA CONFIRMARLE AL "+
		"CLIENTE (dáselos todos, de forma amable y clara):", describeLineas(lineas))
	if resultado.ConductorAsignado != "" {
		mensaje += " Repartidor: " + resultado.ConductorAsignado + "."
	}
	if resultado.Placa != "" {
		mensaje += " Placa del vehículo: " + resultado.Placa + "."
	}
	mensaje += fmt.Sprintf(" Valor a pagar: $%.2f", totalPagar)
	if resultado.FormaPago != "" {
		mensaje += " (" + resultado.FormaPago + ")"
	}
	mensaje += "."
	if seguimiento != "" {
		// El enlace debe ir TAL CUAL, en su propia línea; el modelo no debe reescribirlo ni omitirlo.
		mensaje += " ENLACE DE SEGUIMIENTO EN VIVO (dáselo al cliente TAL CUAL, en su propia línea, " +
			"invitándolo a seguir a su repartidor en el mapa): " + seguimiento
	}
	return mensaje
}

// destinoDelPedido averigua a dónde se acaba de entregar, para poder decirle luego "¿te lo
// envío otra vez a Casa?" en vez de mostrarle coordenadas.
//
// Devuelve (alias, direccion): el alias es el nombre que el cliente le puso a esa ubicación
// ("Casa") y la dirección la calle que resolvió el backend. Se busca la dirección guardada que
// COINCIDA con las coordenadas del pedido (±150 m): el cliente puede tener varias -Casa,
// Trabajo, y la 'WhatsApp' que el bot pisa en cada pedido- y quedarse con la primera daría un
// destino equivocado.
//
// Best-effort: ante cualquier fallo devuelve lo que tenga (posiblemente vacío). Nunca es un
// error: el pedido ya está registrado y esto solo mejora el mensaje de la próxima vez.
func (a *Agent) destinoDelPedido(from, jwt string, lat, lng float64) (alias, direccion string) {
	dirs, err := a.gr.GetDirections(jwt)
	if err != nil {
		log.Printf("[destino] %s: no se pudieron leer las direcciones (%v)", from, err)
		return "", ""
	}
	for _, d := range dirs {
		if !mismaUbicacion(d.Latitude, d.Longitude, lat, lng) {
			continue
		}
		// La calle solo sirve si el backend la resolvió de verdad; el texto de respaldo
		// ("Ubicación compartida por WhatsApp (-2.9, -79.0)") no se le muestra a nadie.
		if calle := strings.TrimSpace(d.Direccion); calle != "" &&
			!strings.HasPrefix(calle, "Ubicación compartida") {
			direccion = calle
		}
		// 'WhatsApp' es el alias interno que pone el backend, no un nombre que el cliente
		// eligió: no se le puede decir "te lo envío a WhatsApp".
		if !strings.EqualFold(d.Alias, aliasInternoWhatsApp) {
			alias = strings.TrimSpace(d.Alias)
		}
		// Con nombre puesto por el cliente ya no hay nada mejor que buscar.
		if alias != "" {
			break
		}
	}
	if direccion != "" {
		a.store.SetDireccionTexto(from, direccion)
	}
	return alias, direccion
}

// urlSeguimiento arma la URL pública del seguimiento a partir del token firmado que devuelve el
// backend. Devuelve "" si no hay token (el backend viejo no lo envía) o si no está configurada la
// base pública: en ese caso el bot simplemente no ofrece el enlace, sin romper nada.
func (a *Agent) urlSeguimiento(token string) string {
	base := strings.TrimRight(a.cfg.SeguimientoBaseURL, "/")
	if token == "" || base == "" {
		return ""
	}
	return base + "/seguimiento/" + token + "/"
}
