package agent

// "NO DESEO" CON UN PEDIDO EN CAMINO SE PREGUNTA (17/10, Blanca González 593984861856).
//
// Tenía el pedido #491 en camino, escribió "Gracias  no deseo" y el bot se despidió ("¡Que
// disfrutes tu día!"). El pedido siguió vivo y lo reasignaron a otro repartidor. pideCancelarPedido
// es estricto a propósito —cancelarle a alguien que no quería es peor—, así que esto no cancela:
// PREGUNTA, con el mismo botón "Cancelar pedido" que ya resuelve ResponderCancelacion. Sin pedido
// en camino no hace nada: un "no deseo" suelto es conversación y lo atiende el modelo.

import (
	"log"
	"strings"
)

// BotonLoEspero es la otra opción del menú: el cliente sí quiere su pedido.
const BotonLoEspero = "Lo espero"

// rellenoDeCancelar se quita antes de comparar: "gracias no deseo" y "no deseo, gracias" son lo mismo.
var rellenoDeCancelar = map[string]bool{"gracias": true, "muchas": true, "mil": true, "porfa": true, "por": true,
	"favor": true, "disculpe": true, "disculpa": true, "ok": true, "bueno": true, "senor": true,
	"senora": true, "senorita": true, "nomas": true}

// negativasDeCancelar: lo que queda, igual a una de estas. Un "no" suelto NO está: casi siempre
// contesta otra pregunta ("¿algo más?").
var negativasDeCancelar = map[string]bool{
	"no deseo": true, "ya no deseo": true, "no lo deseo": true, "ya no lo deseo": true,
	"no quiero": true, "ya no quiero": true, "ya no": true, "no ya no": true,
	"no necesito": true, "ya no necesito": true, "no lo necesito": true, "ya no lo necesito": true,
	"mejor no": true, "no lo quiero": true, "no deseo nada": true, "ya no deseo nada": true,
	"no voy a querer": true, "ya no voy a querer": true, "no me interesa": true,
	"ya no me interesa": true, "dejalo asi": true, "dejemoslo asi": true,
}

func dudaDeCancelar(texto string) bool {
	if strings.ContainsAny(texto, "?¿") {
		return false
	}
	var quedan []string
	for _, w := range strings.Fields(normalizarRespuesta(texto)) {
		if !rellenoDeCancelar[w] {
			quedan = append(quedan, w)
		}
	}
	return negativasDeCancelar[strings.Join(quedan, " ")]
}

// PreguntarSiCancela manda "¿Cancelo tu pedido?" si el cliente tiene un pedido en camino y dijo
// algo que suena a que ya no lo quiere. Devuelve true si se hizo cargo del turno.
func (a *Agent) PreguntarSiCancela(from, texto string) bool {
	if !dudaDeCancelar(texto) {
		return false
	}
	if _, hay := a.store.GetActivePedido(from); !hay {
		return false
	}
	cuerpo := "Tu pedido ya va en camino 🚚. ¿Quieres que lo cancele?"
	opciones := []string{BotonCancelarPedido, BotonLoEspero}
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[cancelacion-dudosa] %s: el menú falló (%v); lo atiende el modelo", from, err)
		return false
	}
	log.Printf("[cancelacion-dudosa] %s dijo %q con un pedido en camino; se le pregunta si cancela", from, texto)
	menu := cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, menu)
	a.store.LogMessage(from, "model", "📋 "+menu) // que el operador lo vea en el chat del panel
	return true
}

// ResponderLoEspero contesta el botón "Lo espero": el pedido sigue, no hay nada que hacer.
func (a *Agent) ResponderLoEspero(from, texto string) (string, bool) {
	if normalizarRespuesta(texto) != normalizarRespuesta(BotonLoEspero) {
		return "", false
	}
	if _, hay := a.store.GetActivePedido(from); !hay {
		return "", false
	}
	msg := "¡Perfecto! 😊 Tu pedido sigue en camino, el repartidor ya va para allá 🚚"
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, msg)
	return msg, true
}
