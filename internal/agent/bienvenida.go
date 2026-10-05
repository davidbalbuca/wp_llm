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
	return textoBienvenida(nombre) + a.datosDeArranque(), true
}

// datosDeArranque son el PRECIO y la COBERTURA que acompañan a la presentación (pedido del dueño,
// 04/10: "la idea es ya dar información precisa desde el arranque"). Son las dos preguntas que
// más se hacen antes de pedir, así que se contestan antes de que las hagan.
//
// Salen del catálogo del backend, nunca quemados: si cambia el precio o se agrega una zona, el
// saludo cambia solo. Sin catálogo no se dice nada (mejor callar que dar un precio viejo).
func (a *Agent) datosDeArranque() string {
	if a.catalog == nil {
		return ""
	}
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return ""
	}
	var b strings.Builder
	switch len(contexto.Products) {
	case 0:
	case 1:
		fmt.Fprintf(&b, "\n💵 $%.2f por cilindro, con envío e instalación incluidos.",
			contexto.Products[0].PrecioTotal())
	default:
		var precios []string
		for _, p := range contexto.Products {
			precios = append(precios, fmt.Sprintf("%s $%.2f", p.Nombre, p.PrecioTotal()))
		}
		fmt.Fprintf(&b, "\n💵 %s (envío e instalación incluidos).", strings.Join(precios, ", "))
	}
	if zonas := ZonasEnTexto(contexto.Zonas); zonas != "" {
		b.WriteString("\n📍 Llegamos a las parroquias urbanas y rurales de " + zonas + ".")
	}
	return b.String()
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
func textoBienvenidaA(nombre string, ahora time.Time) string {
	saludo := franjaDeSaludo(ahora) + "! 👋"
	if nombre != "" {
		// Solo el primer nombre: "¡Hola, David Espinoza Fajardo!" suena a carta del banco.
		saludo = franjaDeSaludo(ahora) + ", " + primerNombre(nombre) + "! 👋"
	}
	return saludo + " ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\n" +
		"Estamos a la vuelta de tu casa y te lo llevamos en minutos 🚚💨"
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
