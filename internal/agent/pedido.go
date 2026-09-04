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
	ok        bool // el pedido se creo en el backend
	enEspera  bool // se creo pero no habia repartidor: quedo en cola (PendingWait)
	IDPedido  int
	Conductor string
	Producto  string
	Color     string
	Cantidad  int
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
			return "El pedido del cliente YA estaba cancelado (lo canceló el conductor). No es un error: " +
				"confírmale con amabilidad que su pedido está cancelado y ofrécele hacer uno nuevo cuando quiera."
		}
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

// avisarSinRepartidor manda al grupo de Telegram el pedido que quedó sin conductor. No es un
// error -el bot buscó, no encontró y se lo dijo al cliente- sino un cliente con ubicación,
// producto y cantidad que NADIE va a atender salvo que una persona lo gestione. Antes solo
// quedaba marcado en el panel, donde había que estar mirando: el 02/09 un cliente esperó los 5
// minutos, se le dijo que no había repartidor y su pedido murió ahí sin que nadie se enterara.
func (a *Agent) avisarSinRepartidor(from string, w conversation.PendingWait, motivo string) {
	pedido := fmt.Sprintf("%d × %s %s", w.Cantidad, w.ProductoNombre, w.ColorNombre)
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
		[]georoutes.OrderProduct{{IDCategoria: w.IDCategoria, IDProducto: w.IDProducto, IDColor: w.IDColor, Cantidad: w.Cantidad}}); err != nil {
		a.crearTicketSoporte(from, "Pedido sin conductor no quedó registrado",
			fmt.Sprintf("El backend rechazó guardarlo como NO ASIGNADO: %v. El pedido no está en ninguna lista; hay que contactar al cliente.", err))
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
						[]georoutes.OrderProduct{{IDCategoria: w.IDCategoria, IDProducto: w.IDProducto, IDColor: w.IDColor, Cantidad: w.Cantidad}}); err != nil {
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

	// GUARDIA DE DIRECCION. Si la ubicacion guardada NO es de esta conversacion, no se registra
	// nada hasta que el cliente confirme a donde va. Evita el peor error posible: que alguien
	// pida en la mañana y en la tarde, desde otro lado, y el gas salga a la direccion vieja.
	// El menu lo manda el codigo y la respuesta se resuelve en codigo (ver direccion.go).
	if args["direccion_confirmada"] != true && !a.ubicacionEsDeAhora(from) {
		if aviso, enPausa := a.pedirConfirmacionDireccion(from, colorNombre, cantidad); enPausa {
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
			a.ultimoPedido = resultadoPedido{
				enEspera: true,
				Producto: producto.Nombre,
				Color:    color.Nombre,
				Cantidad: cantidad,
			}
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

	a.ultimoPedido = resultadoPedido{
		ok:        true,
		IDPedido:  resultado.IDPedido,
		Conductor: resultado.ConductorAsignado,
		Producto:  producto.Nombre,
		Color:     color.Nombre,
		Cantidad:  cantidad,
	}

	// Se guarda la direccion legible que el backend acaba de resolver, para poder preguntarle
	// "¿te lo enviamos a X?" la proxima vez sin tener que mostrarle coordenadas.
	if dirs, err := a.gr.GetDirections(tokens.Access); err == nil {
		for _, d := range dirs {
			if strings.EqualFold(d.Alias, "WhatsApp") && strings.TrimSpace(d.Direccion) != "" &&
				!strings.HasPrefix(d.Direccion, "Ubicación compartida") {
				a.store.SetDireccionTexto(from, d.Direccion)
				break
			}
		}
	}

	mensaje := fmt.Sprintf("Pedido registrado correctamente: %d x %s (%s).", cantidad, producto.Nombre, color.Nombre)
	if resultado.ConductorAsignado != "" {
		mensaje += " Repartidor asignado: " + resultado.ConductorAsignado + "."
	}
	return mensaje
}
