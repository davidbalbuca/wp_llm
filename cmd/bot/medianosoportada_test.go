// UN AUDIO SE CONTESTA CON UNA SALIDA, NO CON UNA LIMITACIÓN.
//
// AUDITORÍA 28/09 (P4 de bot-log-forensics): 15 clientes recibieron "Por ahora solo puedo leer
// mensajes de texto y ubicaciones" y OCHO no volvieron. Uno de esos audios llegó justo después de
// "🛵 El conductor llegó a tu ubicación": había un repartidor en su puerta.
package main

import (
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El mensaje normal OFRECE las dos salidas concretas: escribirlo, o el pin si era la dirección.
func TestElMensajeDeAudioOfreceUnaSalida(t *testing.T) {
	bajo := strings.ToLower(textoPideTexto)

	// La salida principal: que lo escriba.
	if !strings.Contains(bajo, "escrib") {
		t.Errorf("no le pide que lo escriba: %q", textoPideTexto)
	}
	// Y la de la dirección, que es el caso más frecuente en el corpus.
	if !strings.Contains(bajo, "ubicación") && !strings.Contains(bajo, "pin") {
		t.Errorf("no ofrece el pin de ubicación: %q", textoPideTexto)
	}
	// Lo que NO puede hacer: describir la limitación y cortar ahí. El texto viejo decía
	// "Por ahora solo puedo leer mensajes de texto y ubicaciones. Por favor, escribe tu consulta."
	// — correcto pero sin decirle qué hacer con su dirección.
	if strings.Contains(bajo, "solo puedo leer mensajes de texto y ubicaciones") {
		t.Error("sigue siendo el texto viejo, que solo describe la limitación")
	}
}

// CON PEDIDO EN CURSO se deriva a una persona, y el ticket se crea ANTES de prometerlo.
func TestConPedidoEnCursoElAudioVaAUnaPersona(t *testing.T) {
	const from = "593981446968" // el cliente real: mandó audio tras "el conductor llegó"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 301)

	responderMediaNoSoportada(cfgHorario(), store, from)

	tickets := store.ListTickets(conversation.TicketAbierto, 10)
	if len(tickets) != 1 {
		t.Fatalf("con un pedido en curso debe abrirse 1 caso, hay %d", len(tickets))
	}
	if tickets[0].Phone != from {
		t.Errorf("el caso quedó a nombre de %q", tickets[0].Phone)
	}
	// El resumen lleva el estado real (el sello de crearTicketSoporte no aplica aquí porque esto va
	// por reportarFallo, pero el motivo tiene que decir de qué se trata).
	if !strings.Contains(strings.ToLower(tickets[0].Motivo), "audio") {
		t.Errorf("el motivo no dice qué pasó: %q", tickets[0].Motivo)
	}
}

// SIN pedido no se abre ningún caso: es una molestia menor, no un cliente desatendido. Abrir un
// ticket por cada sticker llenaría la cola humana de trabajo inventado.
func TestSinPedidoElAudioNoAbreCaso(t *testing.T) {
	const from = "593999555001"
	store := conversation.NewMemStore()

	responderMediaNoSoportada(cfgHorario(), store, from)

	if n := len(store.ListTickets(conversation.TicketAbierto, 10)); n != 0 {
		t.Errorf("se abrieron %d casos por un audio sin pedido en curso", n)
	}
}

// El mensaje con pedido promete que una persona escribirá. Esa promesa solo vale si el ticket se
// creó, así que el orden importa: es la regla de specs/afirmaciones-respaldadas-por-estado.md.
func TestElTicketSeCreaAntesDePrometerLaLlamada(t *testing.T) {
	src, err := os.ReadFile("medianosoportada.go")
	if err != nil {
		t.Fatalf("no se pudo leer medianosoportada.go: %v", err)
	}
	cuerpo := string(src)
	iTicket := strings.Index(cuerpo, "reportarFallo(")
	iPromesa := strings.Index(cuerpo, "textoPideTextoConPedido)")
	if iTicket < 0 || iPromesa < 0 {
		t.Fatal("no se encontraron la creación del ticket y el envío de la promesa")
	}
	if iTicket > iPromesa {
		t.Error("se promete el contacto ANTES de crear el caso: si falla, el cliente espera en vano")
	}
}

// Y la promesa dice la verdad sobre lo que va a pasar.
func TestElMensajeConPedidoPrometeUnaPersona(t *testing.T) {
	bajo := strings.ToLower(textoPideTextoConPedido)
	if !strings.Contains(bajo, "persona") && !strings.Contains(bajo, "equipo") {
		t.Errorf("no dice que una persona lo va a atender: %q", textoPideTextoConPedido)
	}
	// Y sigue ofreciendo la alternativa rápida: muchos preferirán escribir a esperar.
	if !strings.Contains(bajo, "escríbeme") && !strings.Contains(bajo, "escribe") {
		t.Errorf("no ofrece la vía rápida de escribirlo: %q", textoPideTextoConPedido)
	}
}
