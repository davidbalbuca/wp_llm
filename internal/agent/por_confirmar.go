package agent

// EL REPARTIDOR SE PRESENTA CUANDO ACEPTA (18/10, backend core/georoutes/anuncio_cliente.py).
//
// Dueño: "una vez que se asigna el pedido ya le llega al cliente 'tu conductor es X', pero si no
// acepta le sale cancelado, vuelve a cambiar de conductor y le van llegando mensajes con varios
// conductores... deberia mandar el pedido solo cuando el conductor acepta".
//
// El backend crea el pedido igual que siempre, pero si el repartidor todavía no aceptó lo marca
// `por_confirmar`. Entonces al cliente se le dice que quedó registrado y que estamos confirmando con
// el repartidor más cercano: SIN nombre, placa, tiempo ni enlace. Cuando acepta, el backend lo avisa
// por /internal/order-reassigned con primer_aviso, y recién ahí sale el mensaje del repartidor.
// Mientras tanto los cambios de repartidor no le llegan (el backend no los manda).

import (
	"fmt"
	"strings"
	"sync"

	"wp-llm-gas/internal/georoutes"
)

// porConfirmar: teléfono → pedido que espera que su repartidor acepte. En memoria: si el bot se
// reinicia en el medio, lo único que se pierde es el candado de abajo (el aviso de "aceptó" lo
// manda el backend igual).
var porConfirmar sync.Map

func marcarPorConfirmar(from string, idpedido int) { porConfirmar.Store(from, idpedido) }

// QuitarPorConfirmar se llama cuando el repartidor aceptó o el pedido terminó.
func QuitarPorConfirmar(from string) { porConfirmar.Delete(from) }

func estaPorConfirmar(from string) bool {
	_, hay := porConfirmar.Load(from)
	return hay
}

// ocultarRepartidor deja el resultado sin nada del repartidor que todavía no aceptó.
func ocultarRepartidor(res *georoutes.OrderResult) {
	res.ConductorAsignado = ""
	res.TelefonoConductor = ""
	res.Placa = ""
	res.SeguimientoToken = ""
	res.Tiempo = ""
	res.TieneTracking = false
}

// MensajePorConfirmar es lo que se le dice mientras el repartidor no acepta.
func MensajePorConfirmar(terminaOtraEntrega bool) string {
	if terminaOtraEntrega {
		return "Tu repartidor más cercano está terminando una entrega aquí cerca 🚚. Apenas la " +
			"termine confirma tu pedido y te aviso quién va 😊"
	}
	return "Estoy confirmando con el repartidor más cercano 🚚. En un momento te aviso quién va 😊"
}

// MensajeRepartidorAcepto es el aviso cuando el primer repartidor acepta.
func MensajeRepartidorAcepto(conductor, placa string, minutos int, enlace string) string {
	cuerpo := MensajeRepartidor(conductor, placa, minutos, enlace)
	if cuerpo == "" {
		return "✅ ¡Tu repartidor aceptó tu pedido y ya va en camino! 🚚"
	}
	return "✅ ¡Tu repartidor aceptó tu pedido y ya va en camino! 🚚\n\n" + cuerpo
}

// MensajeSinRepartidor es el cierre cuando NADIE aceptó: no se le dice "el conductor canceló" de un
// repartidor que nunca conoció.
func MensajeSinRepartidor(pedidoID int) string {
	return fmt.Sprintf("😔 Lo siento, no encontré un repartidor disponible para tu pedido #%d en este "+
		"momento. Cuando quieras lo intentamos de nuevo, o dime a qué hora te viene bien y te lo dejo "+
		"agendado.", pedidoID)
}

// instruccionPorConfirmar va en el resultado de registrar_pedido para el modelo.
func instruccionPorConfirmar(terminaOtraEntrega bool) string {
	return " IMPORTANTE: el repartidor TODAVÍA NO ACEPTÓ el pedido. NO le digas al cliente nombre de " +
		"repartidor, placa, tiempo de llegada ni enlace de seguimiento, y no digas que va en camino. " +
		"Dile, con estas palabras: \"" + MensajePorConfirmar(terminaOtraEntrega) + "\""
}

// prometeAlRepartidorPorConfirmar: frases que presentan al repartidor o lo dan por en camino.
func prometeAlRepartidorPorConfirmar(reply string) bool {
	t := strings.ToLower(reply)
	for _, f := range []string{"tu repartidor es", "repartidor:", "placa", "llega en", "va en camino",
		"ya va para", "sigue a tu repartidor"} {
		if strings.Contains(t, f) {
			return true
		}
	}
	return false
}
