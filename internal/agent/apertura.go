// EL PRIMER MENSAJE YA TRAE LOS COLORES.
//
// Pedido del dueño (04/10): "que ya venga con el color de arranque". Quien escribe "Hola" o
// "Deseo pedir GAS 😄" recibe EN UN SOLO WhatsApp la presentación (quién somos, precio, dónde
// llegamos) y debajo los botones de colores. Antes eso dependía del modelo, y no siempre lo hacía:
// el 04/10 a un "Hola" le contestó "¿en qué te puedo ayudar hoy?", un paso de más para alguien que
// solo quiere su gas.
//
// SOLO para aperturas SIMPLES: un saludo o "quiero gas", con palabras de una lista cerrada. Si el
// cliente trae algo más —una pregunta, un color, una cantidad, una dirección—, eso es conversación
// y le toca al modelo (con la presentación encima, como siempre). La lista es cerrada a propósito:
// una palabra que no está en ella basta para no interceptar, así el código nunca le pisa una
// pregunta al cliente.
package agent

import (
	"log"
	"strings"
)

// cuerpoMenuColores es la pregunta que va debajo de la presentación.
const cuerpoMenuColores = "👇 ¿De qué color es tu cilindro?"

// palabrasDeApertura son las únicas que puede traer un mensaje para contestarlo con el menú.
var palabrasDeApertura = map[string]bool{
	"hola": true, "holi": true, "ola": true, "alo": true, "buenas": true, "buenos": true,
	"buen": true, "dia": true, "dias": true, "tarde": true, "tardes": true, "noche": true,
	"noches": true, "saludos": true, "hi": true, "hello": true, "que": true, "tal": true,
	"quiero": true, "quisiera": true, "deseo": true, "necesito": true, "ocupo": true,
	"pedir": true, "pido": true, "hacer": true, "un": true, "una": true, "el": true, "la": true,
	"mi": true, "de": true, "del": true, "por": true, "favor": true, "porfa": true, "porfavor": true,
	"gas": true, "cilindro": true, "tanque": true, "pedido": true, "me": true, "puede": true,
	"pueden": true, "ayudar": true, "ayuda": true, "con": true, "a": true, "domicilio": true,
	"y": true, "info": true, "informacion": true, "gracias": true, "si": true, "ok": true,
}

// esAperturaSimple dice si el mensaje es solo un saludo o un "quiero gas". Los emojis y signos
// se ignoran; cualquier dígito o palabra fuera de la lista lo descarta.
func esAperturaSimple(texto string) bool {
	norm := normalizarRespuesta(texto)
	var palabras int
	for _, campo := range strings.Fields(norm) {
		// Fuera emojis y símbolos sueltos: "GAS 😄" es "gas".
		limpio := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, campo)
		if limpio == "" {
			continue
		}
		if !palabrasDeApertura[limpio] {
			return false
		}
		palabras++
	}
	return palabras > 0
}

// ResponderAperturaConColores manda la presentación con el menú de colores debajo cuando el
// cliente abre la conversación con un saludo o un "quiero gas". Devuelve "" y true si se hizo
// cargo del turno (el menú ya salió).
func (a *Agent) ResponderAperturaConColores(from, texto string) (string, bool) {
	if strings.TrimSpace(a.store.BienvenidaPendiente(from)) == "" {
		return "", false // no es el arranque de una conversación
	}
	if !esAperturaSimple(texto) {
		return "", false
	}
	// Con una ficha a medio llenar (ya dijo un color antes) no se le pregunta el color de cero.
	if p, _ := a.store.GetPedidoEnCurso(from); len(p.Lineas()) > 0 {
		return "", false
	}
	opciones := a.coloresDisponibles()
	if len(opciones) < 2 {
		return "", false // sin catálogo no hay menú: que lo lleve el modelo
	}
	if err := a.mandarMenu(from, cuerpoMenuColores, opciones); err != nil {
		log.Printf("[apertura] %s: el menú de colores falló (%v); lo atiende el modelo", from, err)
		return "", false
	}
	log.Printf("[apertura] %s abrió con %q: presentación + menú de colores en código", from, texto)
	registro := cuerpoMenuColores + " [" + strings.Join(opciones, " / ") + "]"
	// Al historial del modelo, para que entienda el "Blanco" que viene; al panel, para que se vea.
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, registro)
	a.store.LogMessage(from, "model", "📋 "+registro)
	return "", true
}
