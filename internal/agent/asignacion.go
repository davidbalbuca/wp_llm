// EL MISMO MENSAJE CADA VEZ QUE UN PEDIDO QUEDA CON REPARTIDOR.
//
// Pedido del dueño (05/10): "cuando asigne el pedido, si salió de cambio de conductor o de
// agendamiento, le diga igual que los otros: el repartidor tal está asignado, llegará en tantos
// minutos, puedes revisar su ubicación en este link". Hasta entonces cada camino decía otra cosa:
//
//	asignación normal     -> nombre, placa, valor, minutos y enlace (lo redacta el modelo)
//	tras buscar repartidor -> nombre, placa y enlace (mensajeAsignado)
//	entrega agendada      -> "Tu repartidor es X" (sin placa, sin minutos, sin enlace)
//	reasignación          -> "Tu pedido fue asignado a un nuevo repartidor: X" (nada más)
//
// MensajeRepartidor arma el bloque común; cada camino pone su encabezado.
package agent

import (
	"fmt"
	"strings"
)

// MensajeRepartidor es el bloque "quién te lo lleva, cuándo llega y dónde seguirlo". Cada dato
// sale solo si se tiene: sin placa (conductor sin verificar) no se inventa, sin minutos (sin GPS
// real) no se promete un tiempo, y sin enlace no se manda un mapa que no existe.
func MensajeRepartidor(conductor, placa string, minutos int, enlace string) string {
	var b strings.Builder
	if conductor = strings.TrimSpace(conductor); conductor != "" {
		b.WriteString("👤 Repartidor: " + conductor)
		if placa = strings.TrimSpace(placa); placa != "" {
			b.WriteString("\n🚗 Placa: " + placa)
		}
	}
	if minutos > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("⏱️ Llega en unos %d minutos", minutos))
	}
	if enlace = strings.TrimSpace(enlace); enlace != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("📍 Sigue a tu repartidor en vivo aquí:\n" + enlace)
	}
	return b.String()
}

// MensajeReasignado es lo que recibe el cliente cuando su pedido pasa a otro repartidor.
func MensajeReasignado(conductor, placa string, minutos int, enlace string) string {
	cuerpo := MensajeRepartidor(conductor, placa, minutos, enlace)
	if cuerpo == "" {
		return "🔄 Tu pedido fue asignado a un nuevo repartidor. ¡Ya va en camino! 🚚"
	}
	return "🔄 Tu pedido tiene un nuevo repartidor y ya va en camino 🚚\n\n" + cuerpo
}
