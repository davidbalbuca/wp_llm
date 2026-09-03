// Armado del system prompt del agente. Se separa del bucle de conversación (agent.go) porque es
// una responsabilidad distinta: decidir QUÉ contexto ve el modelo en cada mensaje (perfil,
// último pedido, calificación pendiente, ubicación, dirección sin confirmar, horario, programado
// en confirmación). Cada bloque lleva su porqué —varios nacieron de incidentes de producción—.
package agent

import (
	"fmt"
	"strings"
	"time"

	"wp-llm-gas/internal/conversation"
)

// construirSistema arma el prompt en DOS piezas: la FIJA (reglas de comportamiento + información
// del servicio) y la VOLÁTIL (lo que cambia con el cliente y el momento). Juntas dan el mismo
// texto que vería un solo string; la división existe para cachear la fija en Anthropic, que es
// ~65% de lo que se paga y viaja idéntica unas seis veces por conversación.
func (a *Agent) construirSistema(from string) (fijo, volatil string) {
	contexto, disponible := a.catalog.Get()
	fijo = strings.TrimSpace(behaviorPrompt) + "\n\nINFORMACIÓN DEL SERVICIO:\n" + renderServiceInfo(contexto, disponible)

	var b strings.Builder

	// Si ya conocemos al cliente (pidió antes), inyectamos sus datos para que el bot NO se
	// los vuelva a pedir. La IA los reutiliza directamente al registrar el pedido.
	if perfil, ok := a.store.GetProfile(from); ok && perfil.Identificacion != "" {
		fmt.Fprintf(&b, "\n\nDATOS DEL CLIENTE (ya registrado, NO se los vuelvas a pedir; úsalos "+
			"directamente al registrar el pedido):\n- Cédula/identificación: %s\n- Nombres: %s\n- Correo: %s\n"+
			"Salúdalo por su nombre. Para un nuevo pedido solo necesitas: color/marca, cantidad y su ubicación de WhatsApp.",
			perfil.Identificacion, perfil.Nombres, perfil.Correo)

		// Si tiene un pedido anterior, ofrécele repetir lo mismo: es más amigable que
		// preguntarle todo desde cero.
		if last, ok := a.store.GetLastOrder(from); ok && last.Cantidad > 0 {
			fmt.Fprintf(&b, "\n\nÚLTIMO PEDIDO DEL CLIENTE (del %s): %d x %s color/marca %s. "+
				"Cuando quiera pedir, en vez de preguntarle todo desde cero, ofrécele de forma amable repetir "+
				"este mismo pedido (ej: \"¿Deseas lo mismo de la última vez: %d %s %s? ¿O prefieres cambiar algo?\"). "+
				"Si acepta repetir, solo necesitas confirmar y pedirle la ubicación.",
				last.Fecha, last.Cantidad, last.Producto, last.Color, last.Cantidad, last.Producto, last.Color)
		}
	}

	// Si el cliente tiene un pedido recién entregado sin calificar, se lo indicamos al modelo
	// para que le pida (o registre) la calificación del repartidor.
	if rating, ok := a.store.GetPendingRating(from); ok && rating.PedidoID > 0 {
		fmt.Fprintf(&b, "\n\nCALIFICACIÓN PENDIENTE: el cliente tiene un pedido recién ENTREGADO por el "+
			"repartidor %s. Si el cliente responde con un número del 1 al 5 (y opcionalmente un comentario), llama "+
			"PRIMERO a calificar_conductor con ese número, ANTES de ofrecer menús, repetir pedidos o cualquier otro "+
			"tema. Si aún no la ha dado, pídele con amabilidad que califique del 1 al 5 a su repartidor. Si prefiere "+
			"no calificar o lo ignora, no insistas ni lo vuelvas a mencionar.", rating.Conductor)
	}

	// UBICACION: si el cliente ya la compartio, hay que DECIRSELO al modelo. El bot la guarda
	// bien, pero el modelo no tiene forma de saberlo y la seguia pidiendo una y otra vez. Paso
	// el 03/09 con 593963943000: mando su ubicacion dos veces y el bot se la pidio tres, hasta
	// que el cliente empezo a contestar con la direccion escrita ("Tarqui y Sucre frente hotel
	// casa Laura") creyendo que era eso lo que faltaba.
	if _, hayUbicacion := a.store.GetLocation(from); hayUbicacion {
		b.WriteString("\n\nUBICACION: el cliente YA compartio su ubicacion y la tenemos " +
			"guardada. NO se la vuelvas a pedir por ningun motivo. Si te escribe una direccion " +
			"en texto, agradecele y usala como referencia, pero NO le pidas que mande el pin de " +
			"nuevo: ya lo hizo. Sigue con lo que falte del pedido.")
	}

	// Pedido EN PAUSA esperando que confirme la direccion. Si el cliente no toco ninguno de los
	// dos botones -pasa seguido: escribe en vez de tocar- el modelo tiene que saber que hay algo
	// pendiente. Sin esto intentaria registrar el pedido, chocaria otra vez con el guardia y le
	// volveria a mandar el mismo menu, en bucle.
	if _, _, esperandoDir := a.store.GetPedidoEsperandoDireccion(from); esperandoDir {
		b.WriteString("\n\nDIRECCION SIN CONFIRMAR: hay un pedido EN PAUSA porque no sabemos " +
			"a que direccion enviarlo. Se le mostraron dos botones y respondio otra cosa. NO " +
			"registres el pedido y NO des por buena ninguna direccion: pidele que comparta su " +
			"ubicacion ACTUAL por WhatsApp (boton de adjuntar, Ubicacion). Si menciona un lugar " +
			"distinto ('en la oficina', 'donde mi mama'), con mas razon: necesitas el pin de ese " +
			"lugar, no el de antes.")
	}

	// Hora actual + horario laboral: fuera de horario NO se registran pedidos (regla dura,
	// también validada en código); se ofrece PROGRAMAR la entrega.
	ahora := time.Now().In(zonaEcuador)
	fmt.Fprintf(&b, "\n\nHORA ACTUAL: %s (Ecuador). HORARIO DE ENTREGAS: %s a %s.",
		ahora.Format("15:04"), a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
	if !a.dentroDeHorario(ahora) {
		b.WriteString(" ESTAMOS FUERA DE HORARIO: a esta hora NO hay conductores disponibles, así que NO llames " +
			"a registrar_pedido. Explícaselo con amabilidad y ofrécele PROGRAMAR la entrega con la herramienta " +
			"programar_entrega: pide color, cantidad, su ubicación de WhatsApp, cédula y nombre (si es cliente " +
			"nuevo) y la hora deseada. Para la hora, DILE EL HORARIO DE ATENCIÓN y deja que el cliente escriba " +
			"la que prefiera (dentro de ese horario y de las próximas 24 horas): NO le ofrezcas horas como " +
			"opciones ni uses mostrar_menu para eso.")
	} else {
		// Decirlo EN POSITIVO es necesario: si solo se avisa cuando estamos fuera, el modelo ve
		// la hora cerca del cierre y deduce solo que "la jornada terminó". Paso en produccion el
		// 26/08 a las 17:50 (con el horario hasta las 19:00): ofrecio programar para el dia
		// siguiente y luego se contradijo en la misma frase.
		b.WriteString(" ESTAMOS DENTRO DEL HORARIO: el servicio está ACTIVO y SÍ hay entregas ahora mismo, " +
			"aunque falte poco para cerrar. Si el cliente quiere su gas, usa registrar_pedido. NO ofrezcas " +
			"programar_entrega salvo que el cliente PIDA EXPRESAMENTE otra hora, y NUNCA le digas que la " +
			"jornada terminó ni que no hay disponibilidad.")
	}

	// Pedido PROGRAMADO esperando confirmación: el scheduler ya le escribió al cliente.
	if sch, ok := a.store.GetConfirmingSchedule(from); ok {
		a.store.SetLocation(from, sch.Latitude, sch.Longitude)
		if sch.Identificacion != "" {
			a.store.SetProfile(from, conversation.Profile{Identificacion: sch.Identificacion, Nombres: sch.Nombres})
		}
		fmt.Fprintf(&b, "\n\nPEDIDO PROGRAMADO EN CONFIRMACIÓN: %d x %s color %s (los datos y la "+
			"ubicación del cliente ya están guardados). Si el cliente CONFIRMA (\"sí\", \"dale\", \"confirmo\"), "+
			"llama registrar_pedido con color=%s y cantidad=%d SIN pedirle nada más. Si dice que ya no lo desea, "+
			"agradécele y no registres nada.",
			sch.Cantidad, sch.ProductoNombre, sch.ColorNombre, sch.ColorNombre, sch.Cantidad)
	}

	return fijo, b.String()
}
