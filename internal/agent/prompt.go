// Armado del system prompt del agente. Se separa del bucle de conversación (agent.go) porque es
// una responsabilidad distinta: decidir QUÉ contexto ve el modelo en cada mensaje (perfil,
// último pedido, calificación pendiente, ubicación, dirección sin confirmar, horario, programado
// en confirmación). Cada bloque lleva su porqué —varios nacieron de incidentes de producción—.
package agent

import (
	"fmt"
	"strconv"
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

	// Cómo se llama el cliente EN WHATSAPP. Lo puso él mismo, así que es el único nombre fiable
	// mientras no esté registrado. Sin esto el modelo lo deducía del texto: el 10/09 Guillermo
	// Pacheco escribió "Brito por favor 2 cilindros a la iglesia" y el bot lo saludó "¡Hola,
	// Brito!" tres veces seguidas, con su nombre real llegando en el mismo mensaje.
	if perfil, ok := a.store.GetProfile(from); ok && perfil.PerfilWhatsApp != "" {
		fmt.Fprintf(&b, "\n\nNOMBRE DEL CLIENTE EN WHATSAPP: %s. Es el que él mismo puso en su "+
			"perfil. Si lo saludas por su nombre, usa ESTE (o el nombre con el que se presente "+
			"explícitamente). NUNCA deduzcas su nombre de otras palabras del mensaje: lo que "+
			"escribe suele ser el pedido, un lugar o para quién es, no cómo se llama.",
			perfil.PerfilWhatsApp)
	}

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
			// Sin el destino: de eso habla el bloque de abajo, y repetirlo confunde al modelo.
			resumen := describeItems(last.ItemsDelPedido())
			fmt.Fprintf(&b, "\n\nÚLTIMO PEDIDO DEL CLIENTE (del %s): %s de %s. "+
				"Cuando quiera pedir, en vez de preguntarle todo desde cero, ofrécele de forma amable repetir "+
				"este mismo pedido (ej: \"¿Deseas lo mismo de la última vez: %s? ¿O prefieres cambiar algo?\"). "+
				"Si acepta repetir, solo necesitas confirmar y pedirle la ubicación.",
				last.Fecha, resumen, last.Producto, resumen)
			// A dónde fue ese pedido. Sin esto el cliente confirma "lo mismo" sin saber si va a
			// su casa o a donde pidió la última vez desde otro lado.
			if destino := last.Destino(); destino != "" {
				fmt.Fprintf(&b, " Ese pedido se entregó en: %s — MENCIÓNASELO al ofrecer repetir "+
					"(ej: \"¿te lo envío otra vez a %s?\") y así no tiene que compartir la ubicación de nuevo.",
					destino, destino)
			}
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

	// DIRECCIONES GUARDADAS (Fase B.4): si el cliente tiene lugares con nombre y el pedido va a
	// necesitar ubicacion, el modelo debe OFRECERLOS como menu en vez de pedir el pin a secas.
	// Sin esto la Fase B quedaba a la mitad: el bot guardaba "Tienda" y despues no la entendia
	// (David, 10/09, escribio "Ubicacion tienda" tres veces y recibio "compárteme el 📎" tres
	// veces). La ELECCION no la resuelve el modelo: la intercepta el codigo (direcciones.go).
	// Solo se consulta si hara falta: con ubicacion fresca no se molesta al backend.
	if !a.ubicacionEsDeAhora(from) {
		if dirs := a.direccionesConNombre(from); len(dirs) > 0 {
			nombres := make([]string, 0, len(dirs))
			for _, d := range dirs {
				nombres = append(nombres, d.Alias)
			}
			fmt.Fprintf(&b, "\n\nDIRECCIONES GUARDADAS DEL CLIENTE: %s. Cuando necesites la "+
				"ubicación para un pedido, NO pidas solo el pin: usa mostrar_menu con estas "+
				"direcciones como opciones más \"%s\" (ej: cuerpo \"¿A dónde te lo envío?\" y "+
				"opciones [%s, \"%s\"]). Si el cliente elige una, el sistema la resuelve solo. "+
				"Sigue PROHIBIDO aceptar una dirección escrita a mano que no sea una de estas.",
				strings.Join(nombres, ", "), BotonOtraUbicacion,
				`"`+strings.Join(nombres, `", "`)+`"`, BotonOtraUbicacion)
		}
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
	if _, esperandoDir := a.store.GetPedidoEsperandoDireccion(from); esperandoDir {
		b.WriteString("\n\nDIRECCION SIN CONFIRMAR: hay un pedido EN PAUSA porque no sabemos " +
			"a que direccion enviarlo. Se le mostraron dos botones y respondio otra cosa. NO " +
			"registres el pedido y NO des por buena ninguna direccion: pidele que comparta su " +
			"ubicacion ACTUAL por WhatsApp (boton de adjuntar, Ubicacion). Si menciona un lugar " +
			"distinto ('en la oficina', 'donde mi mama'), con mas razon: necesitas el pin de ese " +
			"lugar, no el de antes.")
	}

	// FICHA del pedido en curso: lo que el cliente YA eligió y lo que falta. Sin esto el modelo
	// tenía que deducir del historial en qué punto iba el pedido, y volvía a preguntar cosas ya
	// dichas (el caso del 05/09: la clienta dijo la hora tres veces). Ahora lo lee.
	if p, hay := a.store.GetPedidoEnCurso(from); hay && !p.Vacio() {
		// Con varias líneas (pedido multicolor, C1) se listan todas; con una, el formato de
		// siempre. En ambos casos el modelo ve línea por línea qué cantidad falta.
		if lineas := p.Lineas(); len(lineas) > 1 {
			b.WriteString("\n\nPEDIDO EN CURSO (varios colores, es UN solo pedido):")
			for _, l := range lineas {
				fmt.Fprintf(&b, "\n- color=%s, cantidad=%s", l.Color, valorOFalta(cantidadTexto(l.Cantidad)))
			}
			fmt.Fprintf(&b, "\nhora=%s, flujo=%s.", valorOGuion(p.Hora), p.Flujo)
			b.WriteString("\nCuando no falte nada, llama registrar_pedido UNA sola vez con 'items' " +
				"(todas las líneas juntas). NUNCA la llames una vez por color.")
		} else {
			fmt.Fprintf(&b, "\n\nPEDIDO EN CURSO: color=%s, cantidad=%s, hora=%s, flujo=%s.",
				valorOFalta(p.Color), valorOFalta(cantidadTexto(p.Cantidad)), valorOGuion(p.Hora), p.Flujo)
		}
		if falta := a.loQueFalta(from, p); falta != "" {
			fmt.Fprintf(&b, "\nFALTA: %s.\nPregunta ÚNICAMENTE lo que está en FALTA, un dato por "+
				"mensaje. NO vuelvas a preguntar lo que ya aparece con valor arriba: el cliente ya te lo dijo.", falta)
		} else {
			b.WriteString("\nFALTA: nada. Tienes todo lo necesario: llama YA a la herramienta que " +
				"corresponda (registrar_pedido si es inmediato, programar_entrega si hay hora). " +
				"NO le pidas al cliente que confirme de nuevo lo que ya eligió.")
		}
	}

	// Fecha y hora de Ecuador. Sin la fecha el modelo inventa el día y agenda mal (09/09: dijo
	// "hoy es sábado" un miércoles). Va en la parte volátil: cambia cada mensaje.
	ahora := time.Now().In(zonaEcuador)
	fmt.Fprintf(&b, "\n\nHOY ES: %s. HORA ACTUAL: %s (Ecuador). HORARIO DE ENTREGAS: %s a %s.",
		fechaEnEspanol(ahora), ahora.Format("15:04"), a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
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

// loQueFalta lista, separado por comas, los datos que aún impiden registrar o programar el
// pedido. Devuelve "" cuando ya no falta nada. La ubicación no vive en la ficha (tiene su
// propio slot), pero para el modelo es un dato más de la misma lista.
func (a *Agent) loQueFalta(from string, p conversation.PedidoEnCurso) string {
	var falta []string
	lineas := p.Lineas()
	if len(lineas) == 0 {
		falta = append(falta, "color/marca del cilindro")
	}
	for _, l := range lineas {
		if l.Cantidad < 1 {
			if len(lineas) == 1 {
				falta = append(falta, "cantidad")
			} else {
				falta = append(falta, "cantidad de cilindros "+l.Color)
			}
		}
	}
	if _, hayUbicacion := a.store.GetLocation(from); !hayUbicacion {
		falta = append(falta, "ubicación")
	}
	if p.Flujo == conversation.FlujoProgramacion && p.Hora == "" {
		falta = append(falta, "hora de la entrega")
	}
	return strings.Join(falta, ", ")
}

// valorOFalta y valorOGuion formatean la ficha para el modelo: un dato ausente se marca de
// forma explícita, para que no lo confunda con un valor vacío que puede inventar.
func valorOFalta(v string) string {
	if strings.TrimSpace(v) == "" {
		return "FALTA"
	}
	return v
}

func valorOGuion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return v
}

func cantidadTexto(n int) string {
	if n < 1 {
		return ""
	}
	return strconv.Itoa(n)
}

// fechaEnEspanol devuelve "miércoles 9 de septiembre de 2026". Go no localiza nombres de
// día/mes, así que se mapean a mano.
func fechaEnEspanol(t time.Time) string {
	dias := [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	meses := [...]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio",
		"agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	return fmt.Sprintf("%s %d de %s de %d",
		dias[t.Weekday()], t.Day(), meses[t.Month()-1], t.Year())
}
