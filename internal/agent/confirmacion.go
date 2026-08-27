package agent

// Confirmación de una entrega AGENDADA, resuelta en código y no por el modelo.
//
// Por qué existe este archivo: el 27/08, en producción, pasó tres veces que un cliente
// confirmó su entrega programada y el bot no creó nada. Con Ana (Gemini) le contestó como si
// nada; con David (Claude) le respondió "¿Necesitas algo más? 😊". En los tres casos el
// prompt SÍ llevaba la instrucción explícita —"si el cliente confirma, llama a
// registrar_pedido con color=X y cantidad=N SIN pedirle nada más"— y el modelo simplemente no
// la ejecutó. Dos modelos distintos, el mismo fallo.
//
// La conclusión es que el error no era el prompt: era depender de que el modelo decidiera. Un
// "sí" a una entrega ya agendada no es una conversación, es un botón. Así que cuando hay una
// entrega esperando confirmación y el cliente responde algo inequívoco, el pedido se crea (o
// se cancela) aquí, antes de que el mensaje llegue al modelo. Es el mismo camino que ya usaba
// la verificación por OTP, que nunca falló por esto.
//
// Si la respuesta NO es inequívoca ("y si mejor mañana?", "cuánto cuesta?"), no se toca nada y
// sigue al modelo, que para conversar es bueno. Lo que no puede es decidir si se registra o no
// un pedido que el cliente ya pidió.

import (
	"context"
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/conversation"
)

// Textos de los botones con los que se pregunta por una entrega agendada. Un boton tocado
// llega como texto EXACTO, sin faltas ni variantes, asi que la confirmacion deja de depender
// de adivinar que quiso decir el cliente. Se mantienen cortos: WhatsApp corta los titulos de
// boton a 20 caracteres.
const (
	BotonConfirmarEntrega = "Sí, enviar"
	BotonCancelarEntrega  = "No, cancelar"
)

func init() {
	// Los botones se registran solos como respuestas validas: si mañana se les cambia el texto,
	// el clasificador no se queda atras.
	respuestasAfirmativas[normalizarRespuesta(BotonConfirmarEntrega)] = true
	respuestasNegativas[normalizarRespuesta(BotonCancelarEntrega)] = true
}

// Respuestas que cuentan como un sí. La lista es corta y cerrada a propósito: se compara el
// mensaje ENTERO ya normalizado, no se busca dentro. "si" confirma; "si pero mañana" no.
var respuestasAfirmativas = map[string]bool{
	"si": true, "sii": true, "siii": true, "sip": true, "sipi": true, "sisi": true,
	"si confirmo": true, "si quiero": true, "si por favor": true, "si porfa": true,
	"si dale": true, "si gracias": true, "confirmo": true, "confirmado": true,
	"dale": true, "ok": true, "oka": true, "okay": true, "okey": true, "oki": true,
	"listo": true, "claro": true, "va": true, "de una": true, "afirmativo": true,
	"adelante": true, "por supuesto": true, "envialo": true, "enviar": true,
	"correcto": true, "exacto": true, "yes": true, "confirmar": true,
	"si enviar": true, "si envialo": true, "quiero": true, "lo quiero": true,
	"👍": true, "👌": true, "✅": true, "si 👍": true,
}

// Respuestas que cuentan como un no.
var respuestasNegativas = map[string]bool{
	"no": true, "nop": true, "no gracias": true, "ya no": true, "ya no quiero": true,
	"no quiero": true, "mejor no": true, "cancelar": true, "cancela": true,
	"cancelalo": true, "cancelado": true, "no por ahora": true, "no gracias 🙏": true,
	"ya no lo necesito": true, "no lo necesito": true, "negativo": true,
}

// normalizarRespuesta deja el mensaje comparable: minúsculas, sin tildes, sin signos y sin
// espacios de más. "¡Sí, dale!" y "si dale" tienen que caer en el mismo casillero.
func normalizarRespuesta(texto string) string {
	sinTildes := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n",
	).Replace(strings.ToLower(strings.TrimSpace(texto)))

	var limpio strings.Builder
	for _, r := range sinTildes {
		switch r {
		case '.', ',', ';', ':', '!', '¡', '?', '¿', '"', '\'', '*', '-', '_':
			continue
		default:
			limpio.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(limpio.String()), " ")
}

// ConfirmarProgramado resuelve, SIN pasar por el modelo, la respuesta del cliente a una entrega
// agendada que está esperando confirmación. Devuelve el mensaje para el cliente y si se hizo
// cargo del turno; si devuelve false, el mensaje sigue su camino normal hacia el modelo.
func (a *Agent) ConfirmarProgramado(ctx context.Context, from, texto string) (string, bool) {
	sch, hay := a.store.GetConfirmingSchedule(from)
	if !hay {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)
	if respuesta == "" {
		return "", false
	}

	var salida string
	switch {
	case respuestasAfirmativas[respuesta]:
		salida = a.confirmarYRegistrar(from, sch)
	case respuestasNegativas[respuesta]:
		a.store.SetScheduledEstado(sch.ID, conversation.ScheduleExpirado)
		log.Printf("[confirmacion] %s rechazó su entrega agendada #%d", from, sch.ID)
		salida = "Entendido, cancelé tu entrega programada. Cuando necesites tu gas escríbeme y lo " +
			"vemos al instante. ¡Que tengas buen día! 🙌"
	default:
		// Ni sí ni no: el cliente está preguntando o pidiendo otra cosa. Eso sí es conversación.
		return "", false
	}

	// Este turno no pasó por HandleMessage, que es quien normalmente guarda la conversación. Se
	// guarda aquí para que el modelo no quede ciego a lo que acaba de ocurrir: si el cliente
	// escribe después ("¿cuánto falta?"), tiene que saber que confirmó y que hay un pedido.
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, salida)
	return salida, true
}

// confirmarYRegistrar crea el pedido de la entrega agendada y redacta la respuesta al cliente.
func (a *Agent) confirmarYRegistrar(from string, sch conversation.ScheduledOrder) string {
	// Los datos del cliente viven en la entrega agendada: se restauran antes de registrar, igual
	// que hace HandleMessage cuando arma el prompt.
	a.store.SetLocation(from, sch.Latitude, sch.Longitude)
	if sch.Identificacion != "" {
		a.store.SetProfile(from, conversation.Profile{Identificacion: sch.Identificacion, Nombres: sch.Nombres})
	}

	log.Printf("[confirmacion] %s confirmó la entrega #%d; registrando el pedido sin pasar por el modelo",
		from, sch.ID)

	a.ultimoPedido = resultadoPedido{}
	// Se reutiliza tal cual la herramienta de siempre: mismo camino, mismas validaciones, mismo
	// backend. Lo único que cambia es quién decide llamarla.
	a.runTool(from, "registrar_pedido", map[string]any{
		"color":    sch.ColorNombre,
		"cantidad": sch.Cantidad,
	})
	res := a.ultimoPedido

	switch {
	case res.ok:
		mensaje := fmt.Sprintf("¡Confirmado! 🎉 Tu pedido de %d x %s color %s ya está en camino.",
			res.Cantidad, res.Producto, res.Color)
		if res.Conductor != "" {
			mensaje += fmt.Sprintf(" Tu repartidor es %s.", res.Conductor)
		}
		return mensaje + " Te aviso apenas esté llegando. ¡Gracias por tu confianza! 🙌"

	case res.enEspera:
		// No hay repartidor libre. NO se le vuelve a preguntar si quiere esperar: este cliente ya
		// esperó su turno agendado y volver a mandarlo a una cola es justo lo que no puede pasar.
		// Se arranca la búsqueda sola y se le avisa por WhatsApp en cuanto haya alguien.
		if w, ok := a.store.GetPendingWait(from); ok {
			a.startWaitForDriver(from, w)
		}
		// La entrega queda ATENDIDA aunque todavia no haya repartidor: el cliente ya confirmo y
		// su pedido esta en cola. Sin esto, un segundo "si" (el cliente impaciente que insiste)
		// volveria a entrar aqui, arrancaria otra busqueda en paralelo y le llegarian avisos
		// duplicados. registrar_pedido solo marca la entrega cuando el pedido sale asignado.
		a.store.SetScheduledEstado(sch.ID, conversation.ScheduleEnEspera)
		log.Printf("[confirmacion] %s confirmó #%d pero no hay repartidor; se arrancó la espera",
			from, sch.ID)
		// El plazo va EXPLICITO. La primera version decia solo "te escribo apenas se asigne", y lo
		// primero que preguntó David al probarlo fue: ¿y si nunca me responde? Con razon: el
		// cliente no tiene forma de saber si son dos minutos o una hora. La espera siempre cierra
		// en 5 minutos, asi que se lo decimos.
		return "¡Confirmado! 🎉 Tu pedido ya quedó registrado. En este momento los repartidores " +
			"están un poco lejos, así que estoy buscando uno para ti 🚚. En menos de 5 minutos " +
			"te confirmo si lo conseguí. No tienes que hacer nada; si prefieres cancelar, " +
			"escríbeme \"cancelar\"."

	default:
		// Falló el registro (backend caído, credenciales, etc.). La entrega NO se marca como
		// atendida: queda en confirmando para que se pueda retomar, y el equipo se entera.
		log.Printf("[confirmacion] ERROR registrando el pedido confirmado de %s (#%d)", from, sch.ID)
		return "Recibí tu confirmación ✅, pero tuve un inconveniente técnico al registrar el " +
			"pedido. Ya avisé a nuestro equipo para que te contacte enseguida. Disculpa la demora."
	}
}
