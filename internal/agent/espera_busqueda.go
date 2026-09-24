package agent

// Espera de repartidor manejada por el BACKEND (interruptor BOT_USAR_BUSQUEDA).
//
// El camino de siempre (startWaitForDriver en pedido.go) reintenta el pedido completo cada 30 s
// durante 5 minutos escritos en el código del bot. Funciona, pero deja tres cosas sin resolver:
// el backend no sabe que alguien está esperando —y por eso no puede avisarle por push a los
// conductores desconectados—, los minutos no se pueden cambiar sin recompilar el bot, y si el bot
// se reinicia a mitad de una espera (pasa en cada despliegue) la espera se pierde.
//
// Acá el backend registra la búsqueda y el bot solo pregunta el estado. Los tiempos, la etapa en
// que se avisa a los conductores y a cuáles se avisa se configuran en el panel.
//
// Los mensajes al cliente son EXACTAMENTE los mismos que el camino de siempre: se comparten las
// funciones de cierre (avisarRepartidorAsignado y avisarEsperaVencida), que los dos caminos usan.

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

// intervaloBusqueda es cada cuánto se le pregunta al backend. No es el reloj de la espera: los
// plazos los decide el backend.
//
// Estaba en 15 s y se bajó a 7: el reintento de asignación NO corre solo en el backend, corre
// cuando alguien pregunta, así que el intervalo del bot y el parámetro del panel se suman. Con 15
// aquí y 20 allá, los intentos caían cada 30 s y un pedido tardaba 34 s en asignarse con el
// conductor ya en línea (medido el 19/09). Con 7 aquí y 10 en el panel, baja a ~10-15 s. Cada
// consulta es un intento de asignación, así que tampoco conviene bajarlo mucho más.
const intervaloBusqueda = 7 * time.Second

// topeBusqueda es una red de seguridad del bot, no un plazo del negocio: si el backend quedara
// devolviendo "buscando" para siempre (un parámetro absurdo en el panel, por ejemplo), esta
// goroutine no puede quedarse viva indefinidamente.
//
// Tiene que estar MUY por encima de lo que dura el flujo real, porque los dos relojes corren a
// la vez y gana el que venza primero. Estaba en 30 minutos, que es exactamente lo que el
// backend busca hoy (BUSQUEDA_ESPERA_SEGUNDOS=1800): si ganaba este, el cliente recibía el
// corte seco "no hay repartidor, intenta más tarde" en vez de la última ronda con disculpas y
// [Reprogramar / Cancelar] — justo la venta que las rondas existen para rescatar.
//
// 90 minutos deja margen para triplicar el plazo del panel sin volver a tocar el bot, y sigue
// siendo un techo: una goroutine colgada muere en hora y media, no vive para siempre. Hay un
// test que falla si alguien sube las rondas y este tope se queda corto.
const topeBusqueda = 90 * time.Minute

// esperaBackend abre la búsqueda en el backend y le pregunta el estado hasta que se resuelve.
// Corre en su propia goroutine. Best-effort: nunca tumba el proceso.
func (a *Agent) esperaBackend(from string, w conversation.PendingWait) {
	defer a.buscandoRepartidor.Delete(from) // libera el candado pase lo que pase
	defer func() {
		if r := recover(); r != nil {
			// El cliente aceptó esperar: si esta goroutine muere, nadie le avisa nada.
			log.Printf("[busqueda] panic recuperado para %s: %v", from, r)
			notify.ReportarFallo(a.cfg, a.store, from, "Panic buscando repartidor",
				fmt.Sprintf("La búsqueda de repartidor murió con panic: %v. El cliente quedó esperando.", r))
		}
	}()

	account, okA := a.store.GetAccount(from)
	loc, okL := a.store.GetLocation(from)
	if !okA || !okL {
		log.Printf("[busqueda] %s sin cuenta o sin ubicación; no se puede buscar", from)
		return
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		a.fallaBusqueda(from, "no se pudo autenticar al cliente: "+err.Error())
		return
	}

	res, err := a.gr.BuscarConductor(tokens.Access, loc.Latitude, loc.Longitude, w.IDTipoPago,
		orderProductsDeWait(w), "")
	if err != nil {
		a.fallaBusqueda(from, "el backend no aceptó la búsqueda: "+err.Error())
		return
	}
	if res.Estado == georoutes.BusquedaAsignado && res.Pedido != nil {
		a.avisarRepartidorAsignado(from, w, res.Pedido)
		return
	}

	// El id se guarda en la espera para poder cerrarla si el cliente se arrepiente.
	w.IDBusqueda = res.IDBusqueda
	a.store.SetPendingWait(from, w)
	log.Printf("[busqueda] %s espera la busqueda %d (estado %s)", from, res.IDBusqueda, res.Estado)

	limite := time.Now().Add(topeBusqueda)
	ticker := time.NewTicker(intervaloBusqueda)
	defer ticker.Stop()
	for {
		<-ticker.C
		// Si el cliente canceló (o ya se resolvió) mientras esperaba, salir sin avisar.
		if _, sigue := a.store.GetPendingWait(from); !sigue {
			return
		}

		estado, err := a.gr.EstadoBusqueda(tokens.Access, res.IDBusqueda)
		if err != nil {
			// Un fallo puntual de red no cierra nada: se reintenta en la próxima vuelta.
			log.Printf("[busqueda] %s no se pudo consultar la busqueda %d: %v", from, res.IDBusqueda, err)
			if time.Now().After(limite) {
				a.avisarEsperaVencida(from, w, "No se pudo consultar la búsqueda en el backend.")
				return
			}
			continue
		}

		switch estado.Estado {
		case georoutes.BusquedaAsignado:
			a.avisarRepartidorAsignado(from, w, estado.Pedido)
			return
		case georoutes.BusquedaSinConductor:
			// Venció la búsqueda inicial sin nadie. ESTE es el momento de preguntarle al cliente
			// si quiere esperar: ya se buscó de verdad, y el número que se le dice (lo que dura la
			// espera) sale del backend, no del bot.
			a.ofrecerEsperaAlCliente(from, estado)
		case georoutes.BusquedaNoAsignado:
			// El backend ya lo dejó en no asignados y abrió el ticket: acá solo se avisa.
			a.avisarEsperaVencida(from, w, "El cliente esperó y no se le asignó nadie.")
			return
		case georoutes.BusquedaCancelado:
			log.Printf("[busqueda] %s la busqueda %d quedo cancelada", from, res.IDBusqueda)
			a.store.ClearPendingWait(from)
			return
		}

		if time.Now().After(limite) {
			log.Printf("[busqueda] %s se alcanzo el tope del bot para la busqueda %d", from, res.IDBusqueda)
			a.avisarEsperaVencida(from, w, "La búsqueda pasó el tope de tiempo del bot.")
			return
		}
	}
}

// ofrecerEsperaAlCliente le manda el menú cuando la búsqueda no encontró a nadie.
//
// Se le ofrece hasta rondasConEspera veces seguir esperando, y en la última solo reprogramar o
// cancelar (ver rondasespera.go y el caso de Carlos). Mientras no conteste NO se repite el
// menú: la búsqueda se consulta cada pocos segundos y se queda en SIN_CONDUCTOR, así que sin
// esa marca se le mandaría el mismo menú sin parar.
//
// Los minutos que se le dicen salen del backend cuando los manda (espera_segundos); si no,
// del bot (BOT_ESPERA_RONDA_MIN). Nunca se inventa un número distinto del real.
func (a *Agent) ofrecerEsperaAlCliente(from string, estado *georoutes.BusquedaResult) {
	w, ok := a.store.GetPendingWait(from)
	if !ok || w.EsperandoRespuesta {
		return
	}
	ronda := w.RondaEspera

	minutos := int(a.cfg.EsperaRonda.Minutes())
	if minutos <= 0 {
		minutos = 15
	}
	if estado != nil && estado.EsperaSegundos > 0 {
		minutos = (estado.EsperaSegundos + 59) / 60
	}

	cuerpo := cuerpoDeLaRonda(ronda, minutos)
	opciones := opcionesDeLaRonda(ronda)

	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[busqueda] %s no se pudo ofrecer la espera (ronda %d): %v", from, ronda, err)
		// Si el menú no sale, el cliente se queda sin saber nada: se le pasa la decisión al
		// backend para que siga buscando, en vez de cerrarle la búsqueda por un fallo de WhatsApp.
		a.decidirEsperaBackend(from, w.IDBusqueda, true)
		return
	}

	// El estado se guarda DESPUÉS de que el menú salió: si el envío falla, la ronda no avanza y
	// en la próxima vuelta se vuelve a intentar. Al revés, un fallo de WhatsApp le gastaría una
	// ronda al cliente sin que él haya visto nada.
	w.EsperandoRespuesta = true
	w.RondaEspera = ronda + 1
	a.store.SetPendingWait(from, w)

	a.store.LogMessage(from, "system", cuerpo)
	a.store.AppendModel(from, cuerpo)
	log.Printf("[busqueda] %s ronda %d de espera (opciones %v) para la busqueda %d",
		from, ronda, opciones, w.IDBusqueda)
}

// decidirEsperaBackend le dice al backend si el cliente espera o no. Best-effort: si falla, se
// registra; el backend cierra la búsqueda por su cuenta cuando venza.
func (a *Agent) decidirEsperaBackend(from string, idbusqueda int, esperar bool) {
	account, ok := a.store.GetAccount(from)
	if !ok {
		log.Printf("[busqueda] %s sin cuenta para responder la espera", from)
		return
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		log.Printf("[busqueda] %s no se pudo autenticar para responder la espera: %v", from, err)
		return
	}
	if _, err := a.gr.DecisionBusqueda(tokens.Access, idbusqueda, esperar); err != nil {
		log.Printf("[busqueda] %s no se pudo responder la espera de %d: %v", from, idbusqueda, err)
	}
}

// cerrarBusquedaBackend cancela la búsqueda en el backend, sin dejarla como no asignada: la usa
// el cliente que prefirió PROGRAMAR la entrega en vez de esperar. No es una venta perdida que haya
// que perseguir, así que no corresponde el ticket. Best-effort.
func (a *Agent) cerrarBusquedaBackend(from string, idbusqueda int) {
	account, ok := a.store.GetAccount(from)
	if !ok {
		log.Printf("[busqueda] %s sin cuenta para cerrar la busqueda %d", from, idbusqueda)
		return
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		log.Printf("[busqueda] %s no se pudo autenticar para cerrar la busqueda %d: %v", from, idbusqueda, err)
		return
	}
	if _, err := a.gr.CancelarBusqueda(tokens.Access, idbusqueda); err != nil {
		log.Printf("[busqueda] %s no se pudo cerrar la busqueda %d: %v", from, idbusqueda, err)
		return
	}
	log.Printf("[busqueda] %s cerro la busqueda %d (programo la entrega)", from, idbusqueda)
}

// fallaBusqueda avisa al grupo cuando la búsqueda ni siquiera pudo abrirse. Al cliente ya se le
// dijo que se estaba buscando, así que quedarse callado es lo peor que se puede hacer.
func (a *Agent) fallaBusqueda(from, motivo string) {
	log.Printf("[busqueda] %s %s", from, motivo)
	if w, ok := a.store.GetPendingWait(from); ok {
		a.avisarSinRepartidor(from, w, "No se pudo abrir la búsqueda de repartidor: "+motivo)
	}
	a.avisarEsperaVencida(from, conversation.PendingWait{}, motivo)
}

// avisarRepartidorAsignado cierra una espera con éxito: guarda el estado igual que un pedido
// normal y le avisa al cliente por WhatsApp. La usan los DOS caminos de espera (el del bot y el
// del backend), para que el cliente reciba lo mismo en los dos.
func (a *Agent) avisarRepartidorAsignado(from string, w conversation.PendingWait, res *georoutes.OrderResult) {
	if res == nil {
		log.Printf("[espera] %s asignado sin datos del pedido; no se puede avisar", from)
		return
	}
	cfg, store := a.cfg, a.store
	loc, _ := store.GetLocation(from)

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
	// El enlace de seguimiento queda registrado como el VIVO de este pedido, igual que en el
	// pedido normal: es lo que permite detectar despues un enlace de otro pedido.
	if enlace := a.urlSeguimiento(res.SeguimientoToken); enlace != "" {
		store.SetSeguimientoActivo(from, enlace)
	}
	// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
	msg := mensajeAsignado(a, w, res)
	// Si venia de una entrega agendada, deja de estar "buscando repartidor".
	store.CerrarProgramadoEnEspera(from, true)
	if err := whatsapp.SendText(cfg, from, msg); err == nil {
		store.LogMessage(from, "system", msg)
		// Tambien a la memoria del modelo: si el cliente responde a este aviso,
		// la IA tiene que saber que se lo mandamos.
		store.AppendModel(from, msg)
	}
}

// mensajeAsignado arma el aviso de "ya tienes repartidor" con TODO lo que el cliente necesita:
// que le llega, quien se lo lleva y el enlace para ver el camion en el mapa.
//
// Existe porque este camino lo cierra una goroutine, sin un turno del modelo que redacte la
// confirmacion. El texto anterior era de una linea y servia para el cliente que habia esperado
// cinco minutos; desde que TODO pedido sin conductor inmediato pasa por aqui, ese texto dejaba
// sin detalle ni enlace a un cliente que solo espero treinta segundos.
func mensajeAsignado(a *Agent, w conversation.PendingWait, res *georoutes.OrderResult) string {
	var b strings.Builder
	b.WriteString("🎉 ¡Listo! Tu pedido está confirmado")
	if res.IDPedido > 0 {
		b.WriteString(fmt.Sprintf(" (#%d)", res.IDPedido))
	}
	b.WriteString(".\n\n")

	for _, l := range w.Lineas() {
		nombre := strings.TrimSpace(l.ProductoNombre + " " + l.ColorNombre)
		if nombre == "" {
			nombre = "cilindro"
		}
		b.WriteString(fmt.Sprintf("📦 %d x %s\n", l.Cantidad, nombre))
	}

	if res.ConductorAsignado != "" {
		b.WriteString("🚚 Repartidor: " + res.ConductorAsignado)
		if res.Placa != "" {
			b.WriteString(" (placa " + res.Placa + ")")
		}
		b.WriteString("\n")
	}
	if enlace := a.urlSeguimiento(res.SeguimientoToken); enlace != "" {
		b.WriteString("\n📍 Sigue tu pedido en vivo aquí:\n" + enlace + "\n")
	}
	b.WriteString("\nYa va en camino. ¡Gracias por tu paciencia!")
	return b.String()
}

// avisarEsperaVencida cierra una espera sin repartidor: avisa al grupo, limpia el estado y le
// pide disculpas al cliente. La usan los DOS caminos de espera.
func (a *Agent) avisarEsperaVencida(from string, w conversation.PendingWait, motivo string) {
	cfg, store := a.cfg, a.store
	// Al grupo ANTES de limpiar el estado: es cuando todavía se tiene el pedido completo.
	if w.IDProducto > 0 || len(w.Items) > 0 {
		a.avisarSinRepartidor(from, w, motivo)
	}
	store.ClearPendingWait(from)
	// El historial NO se borra (memoria de 24h). El mensaje queda AUDITADO.
	msgTimeout := "Te pedimos disculpas 🙏. Por ahora no hay ningún repartidor disponible " +
		"para asignar tu pedido. Intenta más tarde, con gusto te ayudamos."
	// Se acabó la espera sin nadie: el pedido queda en No asignados y la entrega
	// agendada lo refleja, en vez de quedarse en "buscando" para siempre.
	store.CerrarProgramadoEnEspera(from, false)
	if err := whatsapp.SendText(cfg, from, msgTimeout); err == nil {
		store.LogMessage(from, "system", msgTimeout)
		// Tambien a la memoria del modelo: si el cliente responde a este aviso,
		// la IA tiene que saber que se lo mandamos.
		store.AppendModel(from, msgTimeout)
	}
}
