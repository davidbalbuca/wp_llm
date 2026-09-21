// NO SE PROMETE UN REPARTIDOR QUE TODAVÍA SE ESTÁ BUSCANDO.
//
// El fin de semana del 19-21/09 pasó doce veces. El peor caso, Nancy (593939943691, 20/09):
//
//	11:20:45  bot    → "Tu pedido está registrado 👍 Estamos buscando al repartidor más cercano"
//	11:21:23  Nancy  → "Por favor necesito que me confirme si viene gracias"
//	11:21:25  bot    → "Aquí está la confirmación 🚚 ... Tu repartidor está en camino"
//	11:27:19  sistema→ Ticket #35 — Pedido sin conductor
//
// Seis minutos después de que le dijeran que su repartidor venía, le dijeron que no había
// ninguno. Canceló, y rechazó el cupón de compensación que le ofrecieron después.
//
// POR QUÉ NO BASTA EL CANDADO DEL PEDIDO FANTASMA. Aquel pregunta "¿el pedido EXISTE?" —y el de
// Nancy existía, se registró a las 11:20:45—. La pregunta que faltaba es otra: "¿tiene
// CONDUCTOR?". Un pedido en la cola de espera está vivo y sin repartidor a la vez, y en ese
// hueco cabían las doce promesas.
//
// QUÉ ARREGLA ESTO Y QUÉ NO. Que 13 pedidos murieran sin conductor es un problema de flota, no
// del bot, y esto no lo toca. Lo que arregla es la diferencia entre un cliente que espera
// tranquilo y uno que sale a la puerta a mirar un camión que no viene.
package agent

import (
	"log"
	"strconv"
	"strings"

	"wp-llm-gas/internal/conversation"
)

// revisarRepartidorPrometido es el CANDADO: si el pedido está en la cola esperando conductor y
// el modelo afirma que el repartidor viene —o cuánto tarda—, se reemplaza por la verdad.
//
// Decide por ESTADO (¿hay espera viva?) y usa el texto solo para saber si la afirmación está
// ahí. Es el mismo orden que el resto de los candados: el estado manda, el texto acompaña.
func (a *Agent) revisarRepartidorPrometido(from, reply string) string {
	if strings.TrimSpace(reply) == "" {
		return reply
	}
	// Solo actúa con la espera VIVA. Sin ella el pedido o no existe —de eso se ocupa el candado
	// del fantasma— o ya tiene conductor, y entonces la frase es cierta y tiene que salir.
	espera, esperando := a.store.GetPendingWait(from)
	if !esperando {
		return reply
	}
	if !prometeRepartidor(reply) {
		return reply
	}

	log.Printf("[repartidor] %s: el modelo prometió repartidor y el pedido sigue en la cola; "+
		"se le dice la verdad", from)
	return a.mensajeBuscandoRepartidor(espera)
}

// mensajeBuscandoRepartidor dice lo que de verdad está pasando. No se disculpa ni dramatiza: el
// pedido está bien tomado y lo único pendiente es el repartidor.
//
// Se repiten los datos del pedido a propósito. Quien pregunta "¿ya viene?" quiere saber que su
// pedido sigue en pie; verlo escrito tranquiliza más que una promesa de tiempo que nadie puede
// sostener.
func (a *Agent) mensajeBuscandoRepartidor(espera conversation.PendingWait) string {
	var b strings.Builder
	b.WriteString("Tu pedido está tomado y en pie 👍\n\n")
	if detalle := describeLineasEspera(espera); detalle != "" {
		b.WriteString("📦 " + detalle + "\n\n")
	}
	b.WriteString("Ahora mismo estoy buscando al repartidor más cercano a tu zona 🔎 " +
		"En cuanto uno acepte te aviso por aquí con su nombre y su placa, sin que tengas que " +
		"hacer nada.\n\n" +
		"No te doy una hora todavía porque sería inventarla, y prefiero decirte las cosas como son 🙏")
	return b.String()
}

// prometeRepartidor detecta que el texto afirma que el repartidor VIENE, o cuánto tarda.
//
// Son dos mentiras distintas y las dos duelen igual: "está en camino" hace salir al cliente a la
// puerta, y "en 30 minutos" lo deja mirando el reloj. Ambas se dicen sin saberlo cuando todavía
// no hay nadie asignado.
func prometeRepartidor(texto string) bool {
	return afirmaRepartidorEnRuta(texto) || prometeTiempoDeEntrega(texto)
}

// afirmaRepartidorEnRuta: "está en camino", "ya salió", "repartidor asignado".
func afirmaRepartidorEnRuta(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"repartidor", "en", "camino"}, {"conductor", "en", "camino"},
		{"pedido", "en", "camino"}, {"gas", "en", "camino"},
		{"esta", "en", "camino"}, {"va", "en", "camino"},
		{"repartidor", "asignado"}, {"conductor", "asignado"},
		{"tienes", "repartidor"}, {"tienes", "conductor"},
		{"repartidor", "ya", "salio"}, {"conductor", "ya", "salio"},
		{"sale", "con", "tu", "pedido"},
		{"esta", "llegando"}, {"va", "llegando"},
	}, 3)
}

// prometeTiempoDeEntrega: "en 30 minutos", "entre 30 y 45 minutos", "llega en media hora".
//
// Va por NÚMERO + unidad y no por una lista de frases porque el modelo inventa la cifra cada
// vez: el 20/09 dijo "30 a 45 minutos" a una clienta cuyo pedido murió 50 minutos después.
func prometeTiempoDeEntrega(texto string) bool {
	normalizado := normalizar(texto)
	if strings.Contains(normalizado, "media hora") {
		return true
	}
	campos := strings.Fields(normalizado)
	for i, palabra := range campos {
		if !esNumero(palabra) {
			continue
		}
		// La unidad puede venir pegada al número o unas palabras después ("30 a 45 minutos").
		for j := i + 1; j < len(campos) && j <= i+4; j++ {
			if esUnidadDeTiempo(campos[j]) {
				return true
			}
		}
	}
	return false
}

func esNumero(palabra string) bool {
	if palabra == "" {
		return false
	}
	for _, r := range palabra {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func esUnidadDeTiempo(palabra string) bool {
	switch strings.TrimRight(palabra, ".,!¡?¿") {
	case "minuto", "minutos", "min", "mins", "hora", "horas":
		return true
	}
	return false
}

// describeLineasEspera arma "2 x GAS 15KG BLANCO" a partir del pedido en espera, con todas sus
// líneas si tuvo varios colores.
func describeLineasEspera(espera conversation.PendingWait) string {
	var partes []string
	for _, linea := range espera.Lineas() {
		if linea.Cantidad <= 0 {
			continue
		}
		texto := strconv.Itoa(linea.Cantidad) + " x " + strings.TrimSpace(linea.ProductoNombre)
		if color := strings.TrimSpace(linea.ColorNombre); color != "" {
			texto += " " + color
		}
		partes = append(partes, texto)
	}
	return strings.Join(partes, " + ")
}
