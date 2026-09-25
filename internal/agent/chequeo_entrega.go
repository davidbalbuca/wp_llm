// Chequeo de entrega de los pedidos del FLUJO MANUAL (posible conductor / verificado sin app).
//
// Un pedido asignado por WhatsApp a un conductor sin app NO tiene quién marque la entrega: en el
// flujo normal es la app del conductor la que cierra el pedido (finalizar_pedido_producto), pero
// aquí no hay app. El pedido se quedaría "en camino" para siempre y el negocio nunca sabría si el
// gas llegó. La única persona que lo sabe es el CLIENTE.
//
// Por eso, cuando un pedido se confirma en modo "sin_tracking" o "solo_nombre" (ver los 3 modos en
// order.go), el bot programa una pregunta ~30 min después: "¿ya te entregaron tu pedido #N?".
//   - Sí  -> llama al backend (ConfirmarEntrega) para CERRAR el pedido (ENTREGADO). Eso, además,
//            dispara el aviso openwa al conductor invitándolo a la app (signal del backend).
//   - No  -> se le pide que avise cuando le entreguen (es importante para nosotros saberlo). No se
//            cierra nada; el cliente puede escribir "ya llegó" después y se vuelve a resolver aquí.
//
// El chequeo se PERSISTE en el store (no solo en la goroutine en memoria) para sobrevivir un
// reinicio del bot: al arrancar, ReprogramarChequeosEntrega vuelve a poner en marcha los pendientes.
package agent

import (
	"log"
	"strconv"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Botones de la pregunta de entrega. Registrados como sí/no en confirmacion.go init() no: se
// comparan explícitamente aquí (abajo) para no depender del orden de inicialización.
const (
	BotonEntregaSi = "Sí, ya llegó"
	BotonEntregaNo = "Todavía no"
)

// frasesYaLlego son las formas en que el cliente confirma la entrega DESPUÉS de haber dicho "aún
// no": no las cubren los afirmativos genéricos ("si", "ok") porque son afirmaciones específicas de
// entrega. Se comparan ya normalizadas (sin tildes/signos, minúsculas). Ver normalizarRespuesta.
var frasesYaLlego = map[string]bool{
	"ya llego": true, "ya me llego": true, "ya me entregaron": true, "ya lo recibi": true,
	"ya lo entregaron": true, "ya me lo entregaron": true, "si ya llego": true, "llego": true,
	"ya lo tengo": true, "ya recibi": true, "entregado": true,
}

// esModoManual dice si un modo de datos del conductor corresponde al flujo manual (sin app que
// cierre la entrega). "normal" NO lo es: ese conductor usa la app y él marca la entrega.
func esModoManual(modo string) bool {
	return modo == "sin_tracking" || modo == "solo_nombre"
}

// programarChequeoEntrega registra y agenda la pregunta de entrega para un pedido del flujo manual.
// Idempotente por teléfono: si ya hay un chequeo vivo, no abre otro (el cliente tiene un pedido a la
// vez en este canal). No lanza; best-effort.
func (a *Agent) programarChequeoEntrega(from string, idpedido int, conductor, modo string) {
	if idpedido <= 0 || !esModoManual(modo) {
		return
	}
	if _, ya := a.store.GetPendingDeliveryCheck(from); ya {
		log.Printf("[chequeo-entrega] %s ya tiene un chequeo en curso; no se abre otro", from)
		return
	}
	preguntarEn := time.Now().Add(a.demoraChequeo())
	chk := conversation.PendingDeliveryCheck{
		PedidoID:    idpedido,
		Conductor:   conductor,
		ModoDatos:   modo,
		PreguntarEn: preguntarEn.Unix(),
		Preguntado:  false,
	}
	a.store.SetPendingDeliveryCheck(from, chk)
	log.Printf("[chequeo-entrega] %s: pedido %d (%s), se preguntará por la entrega a las %s",
		from, idpedido, modo, preguntarEn.Format("15:04:05"))
	a.lanzarChequeoEntrega(from, chk)
}

// demoraChequeo es cuánto falta para preguntar. Sale de la config (30 min por defecto); un valor
// cero o negativo cae al default para que un .env incompleto no dispare la pregunta al instante.
func (a *Agent) demoraChequeo() time.Duration {
	if a.cfg.ChequeoEntrega > 0 {
		return a.cfg.ChequeoEntrega
	}
	return 30 * time.Minute
}

// lanzarChequeoEntrega corre la espera en su propia goroutine y, al vencer, pregunta si el chequeo
// sigue vivo y aún no se ha preguntado. Best-effort: nunca tumba el proceso.
func (a *Agent) lanzarChequeoEntrega(from string, chk conversation.PendingDeliveryCheck) {
	espera := time.Until(time.Unix(chk.PreguntarEn, 0))
	if espera < 0 {
		espera = 0
	}
	a.chequeosArrancados.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[chequeo-entrega] panic recuperado para %s: %v", from, r)
			}
		}()
		timer := time.NewTimer(espera)
		defer timer.Stop()
		<-timer.C
		a.preguntarEntrega(from)
	}()
}

// preguntarEntrega manda la pregunta [Sí / No] si el chequeo sigue vigente. Marca Preguntado para
// que el dispatcher interprete la próxima respuesta del cliente y para no volver a preguntar.
func (a *Agent) preguntarEntrega(from string) {
	chk, ok := a.store.GetPendingDeliveryCheck(from)
	if !ok || chk.Preguntado {
		return // el cliente ya lo resolvió, o ya se le preguntó (otra goroutine tras un reinicio)
	}
	cuerpo := "Hola 👋 Queremos confirmar contigo: ¿ya recibiste tu pedido"
	if chk.PedidoID > 0 {
		cuerpo += " #" + strconv.Itoa(chk.PedidoID)
	}
	cuerpo += "? 📦"
	opciones := []string{BotonEntregaSi, BotonEntregaNo}

	chk.Preguntado = true
	a.store.SetPendingDeliveryCheck(from, chk)
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[chequeo-entrega] no se pudo preguntar la entrega a %s: %v", from, err)
		return
	}
	a.store.LogMessage(from, "system", cuerpo)
	a.store.AppendModel(from, cuerpo)
}

// ResponderChequeoEntrega resuelve, SIN pasar por el modelo, la respuesta del cliente a la pregunta
// "¿ya te entregaron?". Devuelve (mensaje, true) si se hizo cargo; (_, false) si el mensaje no es
// una respuesta a ESTE chequeo y debe seguir su curso normal.
//
// Hay DOS momentos en que un mensaje cierra (o no) el pedido:
//   - Preguntado: acabamos de preguntar y esperamos un Sí/No inmediato.
//   - EsperaYaLlego: el cliente ya dijo "aún no"; ahora escuchamos su aviso futuro ("ya llegó").
// Si no está en ninguno de los dos (p. ej. la goroutine aún no preguntó), un "sí" suelto NO debe
// cerrar el pedido: se deja pasar al modelo.
func (a *Agent) ResponderChequeoEntrega(from, texto string) (string, bool) {
	chk, ok := a.store.GetPendingDeliveryCheck(from)
	if !ok {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)
	if respuesta == "" {
		return "", false
	}

	// Fase 2: el cliente había dicho "aún no" y ahora avisa que ya le entregaron.
	if chk.EsperaYaLlego {
		if frasesYaLlego[respuesta] || respuesta == normalizarRespuesta(BotonEntregaSi) {
			return a.confirmarEntregaCliente(from, chk, texto), true
		}
		// Cualquier otra cosa (una pregunta, otro pedido) es conversación: al modelo. El chequeo
		// sigue vivo hasta su TTL, listo para un "ya llegó" más adelante.
		return "", false
	}

	// Fase 1: acabamos de preguntar; esperamos Sí/No.
	if !chk.Preguntado {
		return "", false
	}
	switch {
	case respuesta == normalizarRespuesta(BotonEntregaSi) || frasesYaLlego[respuesta] || respuestasAfirmativas[respuesta]:
		return a.confirmarEntregaCliente(from, chk, texto), true

	case respuesta == normalizarRespuesta(BotonEntregaNo) || respuestasNegativas[respuesta]:
		// No cerramos nada: el pedido sigue abierto. Pasamos a ESPERAR un "ya llegó" futuro (deja de
		// exigir Sí/No; ahora reconoce las frases de entrega). Ver frasesYaLlego.
		chk.Preguntado = false
		chk.EsperaYaLlego = true
		a.store.SetPendingDeliveryCheck(from, chk)
		salida := "Gracias por avisar 🙏. Por favor, cuando te lo entreguen escríbeme *\"ya llegó\"*: " +
			"para nosotros es muy importante saber que recibiste tu pedido. 📦"
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, salida)
		return salida, true

	default:
		// Ni sí ni no: el cliente pregunta o dice otra cosa. Eso es conversación: al modelo.
		return "", false
	}
}

// confirmarEntregaCliente cierra el pedido en el backend cuando el cliente confirma la entrega.
func (a *Agent) confirmarEntregaCliente(from string, chk conversation.PendingDeliveryCheck, texto string) string {
	account, okA := a.store.GetAccount(from)
	if okA {
		if tokens, err := a.gr.Login(account.Username, account.Password); err == nil {
			if err := a.gr.ConfirmarEntrega(tokens.Access, chk.PedidoID); err != nil {
				// El backend rechazó el cierre (p. ej. ya estaba cerrado por otra vía). No es un
				// error que el cliente deba ver: se agradece igual y se cierra el chequeo.
				log.Printf("[chequeo-entrega] backend no cerró el pedido %d de %s: %v",
					chk.PedidoID, from, err)
			} else {
				log.Printf("[chequeo-entrega] %s confirmó la entrega del pedido %d", from, chk.PedidoID)
			}
		} else {
			log.Printf("[chequeo-entrega] no se pudo autenticar a %s para cerrar el pedido %d: %v",
				from, chk.PedidoID, err)
		}
	}
	a.store.ClearPendingDeliveryCheck(from)
	salida := "¡Gracias por confirmar! 🙌 Nos alegra que ya tengas tu gas. Cuando necesites otro, " +
		"aquí estoy para ayudarte. 😊"
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, salida)
	return salida
}

// ReprogramarChequeosEntrega vuelve a lanzar, al arrancar el bot, los chequeos que quedaron
// pendientes en el store (sobreviven a un reinicio). Los ya preguntados no se relanzan: esperan la
// respuesta del cliente por el dispatcher. Devuelve cuántos re-agendó (útil en logs/tests).
func (a *Agent) ReprogramarChequeosEntrega() int {
	n := 0
	for from, chk := range a.store.PendingDeliveryChecks() {
		if chk.Preguntado {
			continue // ya se le preguntó; su respuesta entra por ResponderChequeoEntrega
		}
		a.lanzarChequeoEntrega(from, chk)
		n++
	}
	if n > 0 {
		log.Printf("[chequeo-entrega] se re-agendaron %d chequeos de entrega tras el arranque", n)
	}
	return n
}
