package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO JOHN (06/10, #370): pidió 2 blancos, quedó en espera y el sistema se lo asignó. La ficha del
// pedido a medio armar quedó viva y a cada respuesta se le pegaba "¿Seguimos con tu pedido?",
// aunque su pedido ya iba en camino.
func TestConPedidoEnCaminoNoSeLePreguntaSiSeguimos(t *testing.T) {
	const from = "593962968093"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 2})
	store.SetActivePedido(from, 370)
	ag := agentIncidente(nil, store)

	dijo := "¡Perfecto, John! 😊 Tu pedido sigue en camino con Juan, tranquilo. En cuanto llegue te aviso por aquí mismo 🚚🙏"
	if ag.turnoQuedaColgado(&turno{}, from, dijo, dijo) {
		t.Error("tiene un pedido en camino: no está a medio pedir, no hay que preguntarle si seguimos")
	}
}

// "¡Buenas noticias! Sí llegamos a tu zona 🎉 ¿Seguimos con tu pedido? 😊" (05/10 al 07/10, 7
// conversaciones): el cliente ya había dado color, cantidad y ubicación; el modelo solo confirmó
// la cobertura y el código le preguntaba si seguía, en vez de registrar su pedido.
func TestConTodoListoSeRegistraEnVezDePreguntarSiSeguimos(t *testing.T) {
	const from = "56934111224"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 1})
	store.SetLocation(from, -2.927613, -78.971617) // la acaba de mandar
	fake := &modeloQueDice{respuestas: []string{"¡Buenas noticias! Sí llegamos a tu zona (EL VALE) 🎉"}}
	ag := agentIncidente(fake, store) // backend caído: el registro se intenta y falla

	if !ag.listoParaRegistrar(from) {
		t.Fatal("tenía color, cantidad y una ubicación de ahora: estaba listo para registrar")
	}
	res, err := ag.HandleMessage(context.Background(), from, "He compartido mi ubicación actual.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Texto, "¿Seguimos con tu pedido?") {
		t.Errorf("no se le pregunta si seguimos: se registra su pedido (got %q)", res.Texto)
	}
	// Se intentó registrar: como su WhatsApp no da un nombre, lo que falta es el nombre (caso
	// Ericka, 07/10), y eso es lo que se le pide, no un "¿seguimos?".
	if !strings.Contains(res.Texto, "tu nombre") {
		t.Errorf("debía intentar registrar y pedir el dato que falta (got %q)", res.Texto)
	}
}

func TestSiFaltaAlgoSePreguntaEsoYNoSeRegistra(t *testing.T) {
	const from = "56934111225"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{Color: "BLANCO"}) // falta la cantidad
	store.SetLocation(from, -2.92, -78.97)
	ag := agentIncidente(nil, store)
	if ag.listoParaRegistrar(from) {
		t.Error("sin cantidad no está listo para registrar")
	}
	if got := ag.preguntaQueFalta(from); got != "¿Cuántos cilindros te envío? 😊" {
		t.Errorf("debía preguntar la cantidad: %q", got)
	}
}
