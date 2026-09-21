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
	nombre := strings.TrimSpace(conversation.NombreDe(a.store, from))
	log.Printf("[bienvenida] %s empieza conversación; se presenta el bot", from)
	return textoBienvenida(nombre), true
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
//   - QUIÉN SOY ("Ubi"): en WhatsApp, un número desconocido que pregunta cosas da desconfianza.
//   - QUÉ HAGO ("te conecto con el repartidor más cercano"): explica por qué hay una espera y
//     por qué se pide la ubicación, antes de pedirla.
//   - NO IMPORTA EL COLOR: es lo que más frena a quien tiene en casa un cilindro de otra marca
//     y cree que no se lo van a cambiar.
//
// Corto porque es lo PRIMERO que se lee: un párrafo largo se salta entero y entonces no sirvió.
func textoBienvenida(nombre string) string {
	saludo := "¡Hola! 👋"
	if nombre != "" {
		// Solo el primer nombre: "¡Hola, David Espinoza Fajardo!" suena a carta del banco.
		saludo = "¡Hola, " + primerNombre(nombre) + "! 👋"
	}
	return saludo + " Soy *Ubi* 🔥\n\n" +
		"Te conecto con el repartidor de gas más cercano a ti, en minutos. " +
		"No importa el color ni la marca de tu cilindro: lo buscamos y te lo llevamos hasta tu puerta 🚚"
}

// primerNombre se queda con la primera palabra del nombre completo.
func primerNombre(nombre string) string {
	if i := strings.IndexByte(nombre, ' '); i > 0 {
		return nombre[:i]
	}
	return nombre
}
