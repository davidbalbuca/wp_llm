package main

// Cierre amable de una conversación que quedó a medias.
//
// Un cliente empieza a pedir gas, el bot le pregunta la cédula y el cliente no vuelve. La
// conversación queda abierta para siempre: ni él sabe si tiene que contestar, ni nosotros
// sabemos si se cayó o se arrepintió. Pasó el 28/08 con 593984187615, que dio color, cantidad
// y ubicación y desapareció justo en el último dato.
//
// DOS PASOS (06/10). Antes, a los pocos minutos el bot se despedía ("Parece que te ocupaste…")
// sin decir qué faltaba: de 41 despedidas solo 9 clientes volvieron, y el operador tenía que
// escribirles a mano "solo necesitamos tu ubicación…", "¿aún necesitas tu gas?". Ahora:
//
//  1. Recordatorio, según dónde se quedó: el color, la cantidad, la ubicación, el nombre o la
//     hora. Es lo mismo que hacía el operador a mano, con el tono de una persona.
//  2. Si tampoco contesta, la despedida, dejando la puerta abierta.
//
// No borra nada: si el cliente vuelve dentro de las 24 h, el historial sigue ahí y retoma el
// pedido donde lo dejó.

import (
	"fmt"
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// mensajeCierre es la despedida VIEJA. Ya no se manda, pero sigue en el historial de las
// conversaciones de antes del 06/10: se reconoce para no despedirse dos veces en la misma sesión.
const mensajeCierre = "Parece que te ocupaste 😊 No te preocupes, aquí estoy.\n\n" +
	"Escríbeme cuando puedas y con gusto seguimos. ¡Te esperamos pronto! 🙌"

// Así empiezan el recordatorio y la despedida: con eso se reconocen en el historial.
const (
	prefijoRecordatorio = "¿Sigues por ahí"
	prefijoDespedida    = "Te dejo por ahora"
)

// Lo que le faltaba al cliente cuando se quedó callado.
const (
	faltaColor     = "color"
	faltaCantidad  = "cantidad"
	faltaUbicacion = "ubicacion"
	faltaNombre    = "nombre"
	faltaHora      = "hora"
	faltaOtra      = "otra"
)

// queLeFalta lee la pregunta que el bot dejó sin responder. Es lo más fiel: es literalmente lo
// que se le pidió, venga de un menú o de un texto del modelo.
func queLeFalta(ultimoDelBot string) string {
	t := strings.ToLower(ultimoDelBot)
	switch {
	case strings.Contains(t, "ubicaci"):
		return faltaUbicacion
	case strings.Contains(t, "color"):
		return faltaColor
	case strings.Contains(t, "cuántos") || strings.Contains(t, "cuantos") || strings.Contains(t, "[1 / 2"):
		return faltaCantidad
	case strings.Contains(t, "nombre"):
		return faltaNombre
	case strings.Contains(t, "hora"):
		return faltaHora
	}
	return faltaOtra
}

// textoRecordatorio es el primer paso: amable y diciendo exactamente qué falta.
func textoRecordatorio(nombre, falta, color string) string {
	saludo := prefijoRecordatorio + "? 😊 "
	if nombre != "" {
		saludo = prefijoRecordatorio + ", " + nombre + "? 😊 "
	}
	switch falta {
	case faltaUbicacion:
		return saludo + "Solo me falta tu ubicación para buscarte al repartidor más cercano. " +
			"Cuando puedas, tocas el 📎 → *Ubicación* y listo."
	case faltaColor:
		return saludo + "Cuéntame de qué color es tu cilindro (blanco, amarillo, naranja o azul) y " +
			"te lo busco enseguida."
	case faltaCantidad:
		cuantos := "¿Cuántos cilindros te mando?"
		if color != "" {
			cuantos = "¿Cuántos cilindros de " + strings.ToLower(color) + " te mando?"
		}
		return saludo + cuantos + " Con el número me basta."
	case faltaNombre:
		return saludo + "Solo me falta tu nombre para que el repartidor te ubique en la entrega."
	case faltaHora:
		return saludo + "Solo dime a qué hora te viene bien recibirlo y lo dejamos agendado."
	}
	return saludo + "Aquí sigo para ayudarte con tu gas cuando quieras."
}

// textoDespedida es el segundo paso, si tampoco contestó el recordatorio. No lleva la marca de
// la presentación ("*UbiGas*, tu repartidor aquísito"): saludounico.go la usa para saber si el
// bot ya se presentó, y una despedida no es una presentación.
func textoDespedida(nombre string) string {
	quien := ""
	if nombre != "" {
		quien = ", " + nombre
	}
	return prefijoDespedida + quien + " 😊 Cuando necesites tu gas, escríbeme nomás: " +
		"¡*UbiGas* está aquísito no más! 🔥🚚"
}

// esAvisoDeCierre dice si un mensaje es uno de los que manda este barrido (viejo o nuevo).
func esAvisoDeCierre(texto string) bool {
	return strings.HasPrefix(texto, "Parece que te ocupaste") ||
		strings.HasPrefix(texto, prefijoRecordatorio) || strings.HasPrefix(texto, prefijoDespedida)
}

// primerNombreBonito: "MARÍA JOSÉ" -> "María". Solo el primer nombre y con mayúscula inicial.
func primerNombreBonito(nombre string) string {
	campos := strings.Fields(nombre)
	if len(campos) == 0 {
		return ""
	}
	r := []rune(strings.ToLower(campos[0]))
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// mensajesMinimos es el recorrido que debe tener la conversación para merecer una despedida.
// A quien escribió "hola" y se fue no se le dice nada: sería hablarle a alguien que ni empezó.
// Cuatro son dos idas y vueltas.
const mensajesMinimos = 4

// cerrarConversacionesInactivas revisa cada minuto los chats callados y despide los que
// corresponde. Se llama una vez desde main, en su propia goroutine.
func cerrarConversacionesInactivas(cfg config.Config, store conversation.Store) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		revisarCierres(cfg, store)
	}
}

func revisarCierres(cfg config.Config, store conversation.Store) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cierre] panic recuperado: %v", r)
			reportarFallo(cfg, store, "", "Panic en el cierre por inactividad",
				fmt.Sprintf("El barrido que despide conversaciones cayó: %v", r))
		}
	}()

	// Los chats con un ticket abierto quedan fuera: ese cliente esta esperando que le escriba
	// una persona, no que el bot se despida. Se consultan UNA vez por barrido.
	derivados := map[string]bool{}
	for _, t := range store.ListTickets(conversation.TicketAbierto, 200) {
		derivados[t.Phone] = true
	}

	ahora := time.Now()
	// FUERA DEL HORARIO DE ATENCIÓN NO SE ESCRIBE. Este barrido corre cada minuto, las 24 h, y no
	// miraba el reloj del negocio: 6 de los 44 cierres del corpus salieron fuera de hora, uno a las
	// 00:00:10 a alguien que había escrito a las 23:52. Un mensaje automático de madrugada no
	// recupera a nadie; molesta.
	if !dentroDelHorario(ahora, cfg) {
		return
	}
	for _, chat := range store.ListConversations(100) {
		if derivados[chat.Phone] {
			continue
		}
		nombre := primerNombreBonito(conversation.NombreUsable(store, chat.Phone))
		switch {
		case mereceDespedida(store, chat, ahora, cfg.CierreDespedida, cfg.CierreVentanaMax):
			if err := avisarCliente(cfg, store, chat.Phone, textoDespedida(nombre)); err != nil {
				log.Printf("[cierre] no se pudo despedir a %s: %v", chat.Phone, err)
				continue
			}
			log.Printf("[cierre] %s no contestó el recordatorio: despedida", chat.Phone)
		case mereceCierre(store, chat, ahora, cfg.CierreInactividad, cfg.CierreVentanaMax):
			falta := queLeFalta(chat.LastMessage)
			color := ""
			if p, ok := store.GetPedidoEnCurso(chat.Phone); ok {
				color = p.Color
				// Con varios colores, el que todavía no tiene cantidad (el mismo del prompt).
				if l, falta := p.PrimeraSinCantidad(); falta {
					color = l.Color
				}
			}
			if err := avisarCliente(cfg, store, chat.Phone, textoRecordatorio(nombre, falta, color)); err != nil {
				log.Printf("[cierre] no se pudo recordar a %s: %v", chat.Phone, err)
				continue
			}
			log.Printf("[cierre] %s se quedó callado; recordatorio (le falta: %s)", chat.Phone, falta)
		}
	}
}

// mereceDespedida: el último mensaje es el recordatorio y el cliente tampoco lo contestó.
func mereceDespedida(store conversation.Store, chat conversation.ConversationSummary,
	ahora time.Time, espera, ventanaMax time.Duration) bool {

	if chat.Mode != conversation.ChatModeBot || chat.LastRole == "user" {
		return false
	}
	if !strings.HasPrefix(chat.LastMessage, prefijoRecordatorio) {
		return false
	}
	silencio := ahora.Sub(time.Unix(chat.LastAt, 0))
	if silencio < espera || silencio > ventanaMax {
		return false
	}
	// Si entre tanto empezó otro flujo con sus propios avisos, no se cruza con ellos.
	if chat.Programado || chat.EnEspera {
		return false
	}
	if _, hay := store.GetActivePedido(chat.Phone); hay {
		return false
	}
	return true
}

// mereceCierre decide si a esta conversación le toca la despedida. Todo lo que dice que NO está
// aquí junto, que es la parte delicada: mandar este mensaje donde no toca es peor que no
// mandarlo.
func mereceCierre(store conversation.Store, chat conversation.ConversationSummary,
	ahora time.Time, inactividad, ventanaMax time.Duration) bool {

	// Un humano tomó el chat: el bot no se mete.
	if chat.Mode != conversation.ChatModeBot {
		return false
	}

	silencio := ahora.Sub(time.Unix(chat.LastAt, 0))
	if silencio < inactividad {
		return false // todavía puede estar escribiendo
	}
	// TECHO. Sin esto, al reiniciar el bot (un deploy, por ejemplo) saldría a despedirse de
	// todas las conversaciones calladas del día a la vez. Recibir "parece que te ocupaste" seis
	// horas después de haber escrito es molesto y desconcierta: si ya pasó demasiado, se deja
	// morir la conversación en silencio.
	if silencio > ventanaMax {
		return false
	}

	// El último en hablar tiene que ser el bot: si quedó colgado un mensaje DEL CLIENTE sin
	// responder, lo que corresponde es contestarle, no despedirse.
	if chat.LastRole == "user" {
		return false
	}
	// Y el bot tiene que haber dejado una PREGUNTA sin responder. Si su último mensaje fue un
	// cierre normal -"¡Hasta pronto!", "ya avisé al dueño"- la conversación no quedó a medias:
	// termino. Sin esta condición pasaba lo que vio David con Juan Solano: el bot se despedía,
	// y siete minutos después soltaba "parece que te ocupaste" sobre una conversación que ya
	// estaba cerrada. En español la pregunta siempre trae "?", venga de un menú o de un texto.
	if !strings.Contains(chat.LastMessage, "?") {
		return false
	}
	// Ya se despidió antes. Como la despedida queda de último mensaje, con mirar ese basta y no
	// hace falta guardar ninguna marca aparte: si el cliente contesta, deja de ser el último.
	if esAvisoDeCierre(chat.LastMessage) {
		return false
	}
	// NI DOS VECES EN LA MISMA SESIÓN. El guard de arriba solo mira el ÚLTIMO mensaje, así que si el
	// cliente contesta y vuelve a callarse, se despide otra vez. A 593995041865 le salió dos veces
	// el 22/09 con 24 minutos de diferencia (22:57 y 23:21). Despedirse de alguien del que ya te
	// despediste hace un rato es lo que hace que el bot parezca un robot estropeado.
	//
	// Se mira el historial reciente en vez de guardar una marca nueva: el dato ya está ahí, y una
	// marca más sería un estado que alguien tendría que limpiar.
	if seDespidioHaceRato(store, chat.Phone, ahora, ventanaMax) {
		return false
	}

	// Flujos que tienen sus propios avisos y sus propios tiempos. Despedirse en medio de
	// cualquiera de ellos se cruzaría con el mensaje que el sistema ya le va a mandar.
	if chat.Programado || chat.EnEspera {
		return false
	}
	if _, hay := store.GetPendingRating(chat.Phone); hay {
		return false // se le pidió que califique; puede contestar en cualquier momento
	}
	if _, hay := store.GetPendingVerification(chat.Phone); hay {
		return false // está por mandar su código de verificación
	}
	if _, hay := store.GetActivePedido(chat.Phone); hay {
		return false // tiene un pedido en curso; el chat sigue vivo aunque él no escriba
	}

	// Y que la conversación haya arrancado de verdad. Con un mensaje menos también, si lo que quedó
	// pendiente es un dato del pedido: es el que escribió "Deseo pedir GAS 😄" (el anuncio), recibió
	// el saludo con los colores y no contestó. Era el grupo más grande que se perdía (06/10) y no
	// le llegaba nada. Al que solo saludó y recibió "¿en qué te ayudo?" se le sigue dejando en paz.
	hay := len(store.GetConversation(chat.Phone, mensajesMinimos))
	return hay >= mensajesMinimos || (hay == mensajesMinimos-1 && queLeFalta(chat.LastMessage) != faltaOtra)
}

// dentroDelHorario dice si AHORA es hora de escribirle a un cliente por iniciativa nuestra.
//
// Los mensajes automáticos (este cierre) solo salen dentro del horario de atención. El barrido
// corre cada minuto las 24 h y no miraba el reloj: 6 de los 44 cierres del corpus del 28/09 se
// mandaron fuera de hora, uno a las 00:00:10 a alguien que había escrito a las 23:52.
//
// Un mensaje que el CLIENTE provoca sí puede salir a cualquier hora —le estamos contestando—. Este
// no: lo decide el bot, y a medianoche solo molesta.
func dentroDelHorario(ahora time.Time, cfg config.Config) bool {
	ini, okIni := horaDelDia(cfg.BotHorarioInicio)
	fin, okFin := horaDelDia(cfg.HorarioFin(ahora.Weekday()))
	if !okIni || !okFin {
		return true // horario mal configurado: no se bloquea nada (el cierre no es crítico)
	}
	m := ahora.Hour()*60 + ahora.Minute()
	return m >= ini && m < fin
}

// horaDelDia convierte "07:00" en minutos desde medianoche. ok=false si no tiene ese formato.
func horaDelDia(hhmm string) (int, bool) {
	var h, m int
	if n, err := fmt.Sscanf(strings.TrimSpace(hhmm), "%d:%d", &h, &m); n != 2 || err != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// seDespidioHaceRato dice si ya se le mandó la despedida dentro de la ventana de esta sesión.
//
// La ventana es la MISMA que el techo del cierre (CierreVentanaMax), a propósito: si pasó más que
// eso, la conversación de antes ya se dio por muerta y esta es nueva —ahí despedirse otra vez es
// correcto—. Lo que no vale es repetirlo dentro de la misma sesión.
func seDespidioHaceRato(store conversation.Store, phone string, ahora time.Time,
	ventana time.Duration) bool {

	for _, m := range store.GetConversation(phone, 30) {
		if !esAvisoDeCierre(m.Content) {
			continue
		}
		if ahora.Sub(time.Unix(m.CreatedAt, 0)) <= ventana {
			return true
		}
	}
	return false
}
