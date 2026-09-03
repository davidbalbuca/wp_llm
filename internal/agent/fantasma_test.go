package agent

import "testing"

// El caso real del 03/09: el bot le dijo a Angel que su pedido estaba "confirmado y en camino"
// sin haber llamado a registrar_pedido. Esas frases deben detectarse como afirmación de pedido
// hecho; las preguntas y promesas legítimas NO.
func TestAfirmaPedidoConfirmado(t *testing.T) {
	// Frases REALES que el bot le dijo a Angel sin registrar nada: deben saltar.
	fantasma := []string{
		"Tu pedido de 2 GAS 15KG BLANCO está confirmado y en camino. El repartidor ya fue asignado y te llegará en breve 🚚.",
		"Tu pedido de 2 GAS 15KG BLANCO ya está confirmado y el repartidor está en camino hacia ti 🚚.",
		"Tu pedido de 3 GAS 15KG BLANCO está confirmado y en camino. El repartidor ya fue asignado.",
		"El repartidor está asignado y en camino hacia ti.",
		"Tu pedido sigue en camino con el repartidor 🚚.",
	}
	for _, f := range fantasma {
		if !afirmaPedidoConfirmado(f) {
			t.Errorf("NO detectó pedido fantasma: %q", f)
		}
	}

	// Mensajes LEGÍTIMOS del flujo normal: NO deben saltar (si no, bloquearíamos respuestas buenas).
	legitimos := []string{
		"¿Qué color de cilindro necesitas?",
		"Perfecto, 2 cilindros blancos 👍. Compárteme tu ubicación 📎.",
		"¿Confirmas que quieres 2 cilindros blancos?",
		"En cuanto tenga un repartidor disponible te aviso 😊.",
		"Estoy buscando un repartidor en tu zona, mantén atento el WhatsApp.",
		"Los repartidores están un poco lejos, ¿deseas esperar?",
		"¡Hola! ¿En qué te puedo ayudar?",
		"Tu cédula, por favor.",
	}
	for _, l := range legitimos {
		if afirmaPedidoConfirmado(l) {
			t.Errorf("FALSO POSITIVO, bloquearía un mensaje bueno: %q", l)
		}
	}
}
