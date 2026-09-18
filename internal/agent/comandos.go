// LOS COMANDOS DE HERRAMIENTAS NO SON CONVERSACIÓN.
//
// El caso (Ángel, 18/09): escribió en el chat comandos de Claude Code —`/clear`, `/compact`,
// `/model`— y el bot los contestó como si fueran mensajes de un cliente. Se equivocó al teclear
// ("comdel", "comtacto") y el bot siguió respondiendo cada intento con naturalidad.
//
// POR QUÉ IMPORTA, aunque parezca anecdótico:
//
//  1. El bot quedó respondiendo cosas sin sentido en un chat real. Si hubiera pasado con un
//     cliente mirando —o si el chat se revisa después— parece que el asistente está roto.
//  2. Es un mensaje que consume una llamada al modelo, con su costo, para nada.
//  3. Y sobre todo: quien escribe `/clear` está pidiendo ALGO CONCRETO —empezar de nuevo— y el
//     bot le contestó cualquier otra cosa. Que el comando sea de otra herramienta no quita que
//     la intención se entienda perfectamente.
//
// Se resuelve en código y antes del modelo, como el resto de los interceptores: un comando es un
// conjunto cerrado, no hay nada que interpretar.
//
// QUÉ SE HACE CON CADA UNO:
//
//   - `/clear` y similares → se cumple: la conversación arranca limpia. Es lo que pidió.
//   - cualquier otro comando → se responde que aquí no aplica, con amabilidad y recordando qué SÍ
//     se puede hacer. No se le regaña ni se le explica qué es Claude Code: al cliente que teclea
//     algo raro por error le sirve más un empujón hacia el pedido.
//
// NO se interceptan mensajes que solo CONTIENEN una barra ("vivo en la 10 de agosto s/n"): el
// comando tiene que ser el mensaje entero.
package agent

import (
	"log"
	"strings"
)

// comandosDeLimpieza son los que piden empezar de cero. Se cumplen de verdad.
var comandosDeLimpieza = map[string]bool{
	"clear": true, "reset": true, "reiniciar": true, "limpiar": true,
	"nuevo": true, "new": true, "restart": true,
}

// comandosConocidos son los de herramientas de IA que alguien puede teclear por costumbre, más
// las variantes mal escritas que se vieron en producción ("comdel", "comtacto" por "compact").
//
// La lista NO pretende ser exhaustiva: lo que decide es la forma (empieza por "/" y es una sola
// palabra), no estar en esta lista. Sirve para el log, para saber qué se está tecleando.
var comandosConocidos = map[string]bool{
	"compact": true, "model": true, "help": true, "exit": true, "quit": true,
	"login": true, "logout": true, "cost": true, "doctor": true, "init": true,
	"status": true, "config": true, "review": true, "undo": true, "resume": true,
	"comdel": true, "comtacto": true, "compac": true, "comact": true,
}

// EsComando dice si el mensaje es un comando de herramienta y no algo que un cliente diría.
//
// La regla es la FORMA, no una lista: empieza por "/" y es una sola palabra sin espacios. Así
// cubre los que no previmos —que es justo lo que falló— sin tragarse frases normales.
//
// Devuelve también el nombre sin la barra, para el log.
func EsComando(texto string) (string, bool) {
	t := strings.TrimSpace(texto)
	if len(t) < 2 || t[0] != '/' {
		return "", false
	}
	cuerpo := strings.TrimSpace(t[1:])
	// Una sola palabra: "/clear" sí, "/ hola que tal" no. Un cliente no escribe una frase entera
	// empezando por barra, pero sí podría mandar una dirección con "/" dentro.
	if cuerpo == "" || strings.ContainsAny(cuerpo, " \t\n") {
		return "", false
	}
	// Solo letras, dígitos, guiones y dos puntos (los comandos con plugin: "plugin:skill").
	for _, r := range cuerpo {
		esLetra := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		esDigito := r >= '0' && r <= '9'
		if !esLetra && !esDigito && r != '-' && r != '_' && r != ':' {
			return "", false
		}
	}
	return strings.ToLower(cuerpo), true
}

// ResponderComando resuelve, sin pasar por el modelo, un mensaje que es un comando de
// herramienta. Devuelve el mensaje para el cliente y si se hizo cargo del turno.
func (a *Agent) ResponderComando(from, texto string) (string, bool) {
	comando, esCmd := EsComando(texto)
	if !esCmd {
		return "", false
	}

	if comandosDeLimpieza[comando] {
		// Se CUMPLE, no se explica. Quien escribe /clear quiere empezar de nuevo, y eso el bot
		// sabe hacerlo: no tiene sentido negárselo por venir en forma de comando.
		log.Printf("[comando] %s escribió /%s; se limpia la conversación de verdad", from, comando)
		a.cerrarCicloDeConversacion(from, "el cliente pidió empezar de nuevo con /"+comando)
		respuesta := "¡Listo! Empezamos de cero 🧹 ¿En qué te ayudo? Puedo tomarte un pedido de gas " +
			"cuando quieras 😊"
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, respuesta)
		return respuesta, true
	}

	conocido := comandosConocidos[comando]
	log.Printf("[comando] %s escribió /%s (conocido=%v); no se pasa al modelo", from, comando, conocido)
	respuesta := "Por aquí no uso comandos 🙂 Escríbeme normal y te ayudo: puedo tomarte un pedido " +
		"de gas, decirte precios o ver cómo va tu entrega."
	// El turno queda en el historial para que el modelo no vea un hueco: si el cliente escribe
	// después, sabe que ya se le contestó algo y no arranca como si nada.
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, respuesta)
	return respuesta, true
}
