// EL BOT SE PRESENTA SIEMPRE AL EMPEZAR UNA CONVERSACIÓN.
//
// Pedido de David (21/09): "ahí debe lanzar siempre siempre siempre un mensaje de bienvenida
// como contando lo que hace Ubi — si alguien escribe, debe decirle hola soy Ubi, te conecto con
// el repartidor más cercano en minutos, no importa el color de gas, lo buscamos y te lo
// llevamos, alguna cosa así".
//
// LO QUE PASABA (captura del 20/09, 593990364311): el cliente escribe "Deseo pedir GAS 😄" y el
// bot contesta DIRECTO con "¿Qué color de cilindro prefieres? BLANCO / AMARILLO / NARANJA /
// AZUL". Nunca dice quién es ni qué hace.
//
// Eso cuesta de dos formas. El que llega por un anuncio no sabe con quién está hablando —y en
// WhatsApp, un número desconocido que te pregunta cosas da desconfianza—. Y el que viene con una
// duda ("¿traen a mi zona?", "¿cuánto cuesta?") recibe un formulario en vez de una respuesta.
// Dos líneas contestan las dos cosas antes de que las pregunten.
//
// POR QUÉ EN CÓDIGO Y NO EN EL PROMPT. Es la lección que este proyecto ya aprendió cuatro veces:
// el prompt PIDE y el modelo a veces no obedece. "Siempre siempre siempre" no puede depender de
// que el modelo se acuerde.
//
// A QUIÉN NO SE LE SALUDA, que es igual de importante: al que está a mitad de una conversación
// (se presentaría en cada mensaje, y eso parece roto) y al que tiene un pedido esperando
// repartidor (saludarlo de cero le haría pensar que nos olvidamos de su gas).
package agent

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"wp-llm-gas/internal/conversation"
)

// SaludoDeBienvenida devuelve la presentación si a este cliente le toca recibirla.
//
// Decide por ESTADO, igual que el resto de los interceptores: cuánto hace que escribió y si
// tiene algo pendiente. No mira el texto del mensaje —un saludo no tiene por qué ser "hola"—.
func (a *Agent) SaludoDeBienvenida(from string) (string, bool) {
	if !a.empiezaConversacion(from) {
		return "", false
	}
	// Solo un nombre que sirva: "¡Buenas noches, @sd2!" suena a robot (05/10).
	nombre := conversation.NombreUsable(a.store, from)
	log.Printf("[bienvenida] %s empieza conversación; se presenta el bot", from)
	return a.textoBienvenidaSegunHorario(nombre, time.Now().In(zonaEcuador)) + "\n\n" + LineaApp(), true
}

// LineaApp invita a la app en CADA saludo (dueño, 09/10: "cada que se pueda va lo de la app, pero
// que no sea tanto texto, las personas no leen y no se están bajando la app"). Una línea, el
// enlace al final para que sea lo que se toca.
func LineaApp() string {
	return "📲 ¡Ya tenemos app! Pide tu gas desde ahí 👉 " + LinkApp
}

// textoBienvenidaSegunHorario es la presentación de siempre en horario y, FUERA de horario, otra
// de entrada (05/10): quién somos, que ahora no estamos atendiendo y que le agendamos el pedido.
//
// Antes, a las 20:52 el saludo prometía "te lo llevamos en minutos" y recién DESPUÉS de que el
// cliente eligiera el color el modelo le decía que ya habíamos cerrado: una promesa y su
// desmentida en dos mensajes seguidos. Pedido del dueño: "hola soy Ubi etc, ahora no estamos
// atendiendo pero te agendo tu pedido para mañana desde las 7 am".
//
// "Te agendo" es un ofrecimiento, nunca "te lo dejo agendado": eso afirma algo que todavía no
// existe, el modelo lo repetía y el candado del pedido fantasma forzaba un registro inmediato que
// fallaba (probado en el simulador el 05/10 a las 21:25).
func (a *Agent) textoBienvenidaSegunHorario(nombre string, ahora time.Time) string {
	cerrado := a.avisoFueraDeHorario(ahora)
	if cerrado == "" {
		return textoBienvenidaEn(nombre, ahora, a.enTodaLaCiudad())
	}
	return saludoDeFranja(nombre, ahora) + " Soy *UbiGas*, tu repartidor aquísito no más" + a.enTodaLaCiudad() +
		" 🔥\n\n" + cerrado
}

// avisoFueraDeHorario dice que ahora no atendemos y cuándo sí; "" si estamos en horario (o si el
// horario no está configurado: sin horario no se le cierra la puerta a nadie).
func (a *Agent) avisoFueraDeHorario(ahora time.Time) string {
	ini := a.cfg.BotHorarioInicio
	if parseHoraHHMM(ini) < 0 || a.dentroDeHorario(ahora) {
		return ""
	}
	abre, ok := a.proximaApertura(ahora)
	if !ok {
		return ""
	}
	desde := "desde las " + horaAmigable(ini)
	cuando := "para mañana"
	switch dias := diasEntre(ahora, abre); {
	case dias == 0:
		cuando = "para hoy"
	case dias > 1:
		cuando = "para el " + diasEnEspanol[isoDelDia(abre)]
	}
	motivo := "🌙 Ahora no estamos atendiendo"
	switch {
	case diasEntre(ahora, abre) == 0:
		motivo = "🌙 Todavía no empezamos a atender"
	case !a.esDiaLaborable(ahora):
		motivo = "🌙 Hoy no estamos atendiendo"
	case a.esDiaLaborable(ahora) && ahora.Hour()*60+ahora.Minute() >= parseHoraHHMM(a.finDelDia(ahora)):
		motivo += " (hoy atendimos hasta las " + horaAmigable(a.finDelDia(ahora)) + ")"
	}
	// WhatsApp solo deja escribirle dentro de las 24 h desde su último mensaje: más allá no se
	// puede agendar, porque no habría cómo confirmarle la entrega.
	if abre.Sub(ahora) > 24*time.Hour {
		return motivo + ". Volvemos " + strings.TrimPrefix(cuando, "para ") + " " + desde +
			": escríbenos y te lo llevamos en minutos 🚚"
	}
	return motivo + ", pero te agendo tu pedido " + cuando + " " + desde + " 🚚"
}

// proximaApertura es el próximo momento en que empezamos a atender (hoy o un día siguiente).
func (a *Agent) proximaApertura(ahora time.Time) (time.Time, bool) {
	ini := parseHoraHHMM(a.cfg.BotHorarioInicio)
	if ini < 0 {
		return time.Time{}, false
	}
	for d := 0; d <= 7; d++ {
		dia := ahora.AddDate(0, 0, d)
		abre := time.Date(dia.Year(), dia.Month(), dia.Day(), ini/60, ini%60, 0, 0, ahora.Location())
		if abre.After(ahora) && a.esDiaLaborable(abre) {
			return abre, true
		}
	}
	return time.Time{}, false
}

// diasEntre cuenta los días de calendario entre dos instantes (0 = el mismo día).
func diasEntre(desde, hasta time.Time) int {
	a := time.Date(desde.Year(), desde.Month(), desde.Day(), 0, 0, 0, 0, desde.Location())
	b := time.Date(hasta.Year(), hasta.Month(), hasta.Day(), 0, 0, 0, 0, desde.Location())
	return int(b.Sub(a).Hours()+12) / 24
}

// horaAmigable pasa "07:00" a "7 am" y "20:30" a "8:30 pm", como se dice en la calle.
func horaAmigable(hhmm string) string {
	m := parseHoraHHMM(hhmm)
	if m < 0 {
		return hhmm
	}
	h, min, sufijo := m/60, m%60, "am"
	if h >= 12 {
		sufijo = "pm"
	}
	if h > 12 {
		h -= 12
	}
	if h == 0 {
		h = 12
	}
	if min == 0 {
		return fmt.Sprintf("%d %s", h, sufijo)
	}
	return fmt.Sprintf("%d:%02d %s", h, min, sufijo)
}

// isoDelDia es el número ISO del día (lunes=1 … domingo=7), el de la configuración.
func isoDelDia(t time.Time) int {
	if iso := int(t.Weekday()); iso != 0 {
		return iso
	}
	return 7
}

// enTodaLaCiudad es " en todo Cuenca" con las zonas del catálogo, o "" si no hay catálogo.
//
// Reemplaza al precio y la lista de parroquias que iban debajo del saludo (09/10): eran tres líneas
// que nadie leía. El precio lo sigue diciendo el bot si se lo preguntan (está en su prompt). Sale
// del catálogo, no escrito: si se suma otra ciudad, el saludo cambia solo.
func (a *Agent) enTodaLaCiudad() string {
	if a.catalog == nil {
		return ""
	}
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil || len(contexto.Zonas) == 0 {
		return ""
	}
	var ciudades []string
	for _, z := range contexto.Zonas {
		if nombre := strings.TrimSpace(z.Zona); nombre != "" {
			ciudades = append(ciudades, nombrePropio(nombre))
		}
	}
	if len(ciudades) == 0 {
		return ""
	}
	if len(ciudades) == 1 {
		return " en todo " + ciudades[0]
	}
	return " en " + strings.Join(ciudades[:len(ciudades)-1], ", ") + " y " + ciudades[len(ciudades)-1]
}

// nombrePropio pasa "SANTO DOMINGO" a "Santo Domingo".
func nombrePropio(s string) string {
	palabras := strings.Fields(strings.ToLower(s))
	for i, p := range palabras {
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		palabras[i] = string(r)
	}
	return strings.Join(palabras, " ")
}

// empiezaConversacion dice si este mensaje abre una conversación nueva.
//
// Usa la MISMA ventana que la limpieza por estado (VentanaConversacionSeguida) y el mismo
// "¿tiene algo pendiente?", a propósito: las dos preguntas son la misma —¿esto es una
// conversación nueva?— y contestarlas distinto haría que el bot saludara a quien acaba de
// perder su contexto, o al revés.
func (a *Agent) empiezaConversacion(from string) bool {
	ultima, hay := a.store.LastActivity(from)
	if hay && time.Since(ultima) < VentanaConversacionSeguida {
		return false // sigue conversando
	}
	if motivo, ocupado := a.tienePendiente(from); ocupado {
		log.Printf("[bienvenida] %s vuelve pero tiene %s; no se le presenta de cero", from, motivo)
		return false
	}
	return true
}

// textoBienvenida arma la presentación. Los tres puntos son los que pidió David, y cada uno
// responde a una duda concreta del cliente:
//
//   - QUIÉN SOY ("UbiGas"): en WhatsApp, un número desconocido que pregunta cosas da desconfianza.
//     "UbiGas" y no "Ubi" (04/10): lleva la palabra gas, así se entiende y se guarda más rápido.
//   - QUÉ HAGO ("te conecto con el repartidor más cercano"): explica por qué hay una espera y
//     por qué se pide la ubicación, antes de pedirla.
//   - EL GANCHO ("¿Se te acabó el gas? ¡Con UbiGas, tu repartidor aquísito no más!", elegido por el dueño): es la situación de quien escribe. Ya no se habla de
//     marcas (04/10): no hay marca, y el color se pregunta justo debajo con los botones.
//
// Corto porque es lo PRIMERO que se lee: un párrafo largo se salta entero y entonces no sirvió.
func textoBienvenida(nombre string) string {
	return textoBienvenidaA(nombre, time.Now())
}

// textoBienvenidaA es la misma cosa con el reloj inyectado, para poder probar las franjas sin
// esperar a que sea de noche.
//
// El saludo lleva la FRANJA DEL DÍA porque si no, el modelo la corrige: a las 20:00 el código decía
// "¡Hola, Doris!" y el modelo contestaba "¡Buenas noches, Doris!" nueve segundos después (28/09).
// Diciéndolo bien la primera vez no hay nada que corregir — y el candado de saludounico.go quita lo
// que sobre.
//
// 09/10: solo la primera línea. "Estamos a la vuelta de tu casa…", el precio y las parroquias
// eran tres líneas más que nadie leía; la invitación a la app va aparte (LineaApp).
func textoBienvenidaA(nombre string, ahora time.Time) string {
	return textoBienvenidaEn(nombre, ahora, "")
}

// textoBienvenidaEn es el saludo con la ciudad (" en todo Cuenca") pegada al gancho.
func textoBienvenidaEn(nombre string, ahora time.Time, ciudad string) string {
	return saludoConGancho(nombre, ahora, ciudad)
}

// saludoConGancho es la primera línea en horario: el saludo de la franja y el gancho de UbiGas.
// Las dos versiones (en horario y cerrado) llevan "*UbiGas*, tu repartidor aquísito", que es lo
// que saludounico.go reconoce para no repetir la presentación.
func saludoConGancho(nombre string, ahora time.Time, ciudad string) string {
	if ciudad != "" {
		ciudad = "," + ciudad
	}
	return saludoDeFranja(nombre, ahora) + " ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más" +
		ciudad + "! 🔥"
}

// saludoDeFranja es "¡Buenas noches, Ana! 👋" (o sin nombre si su WhatsApp no dice uno).
func saludoDeFranja(nombre string, ahora time.Time) string {
	if nombre == "" {
		return franjaDeSaludo(ahora) + "! 👋"
	}
	// Solo el primer nombre: "¡Hola, David Espinoza Fajardo!" suena a carta del banco.
	return franjaDeSaludo(ahora) + ", " + primerNombre(nombre) + "! 👋"
}

// primerNombre se queda con la primera palabra del nombre completo.
func primerNombre(nombre string) string {
	if i := strings.IndexByte(nombre, ' '); i > 0 {
		return nombre[:i]
	}
	return nombre
}

// franjaDeSaludo da el saludo que corresponde a la hora, sin el cierre ni el emoji.
//
// Los cortes son los de uso corriente en Ecuador: la mañana hasta las 12, la tarde hasta las 19
// (que es justo el fin del horario de atención), y la noche el resto. Quien escribe a las 22:00
// espera "buenas noches", no "hola".
func franjaDeSaludo(ahora time.Time) string {
	switch h := ahora.Hour(); {
	case h < 12:
		return "¡Buenos días"
	case h < 19:
		return "¡Buenas tardes"
	default:
		return "¡Buenas noches"
	}
}
