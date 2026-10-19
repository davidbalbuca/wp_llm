// TICKET #151 (19/10 16:49): el cliente preguntó "hasta qué hora hacen las entregas sector el vergel"
// a medio pedido (1 BLANCO, sin ubicación todavía). El modelo contestó y dijo que el cilindro salía
// "en camino" apenas mandara la ubicación; el candado del fantasma forzó un registro imposible (sin
// ubicación), el modelo reescribió "no pude completar tu pedido, ya avisé al equipo" y otro candado
// abrió un ticket. Sin ubicación el pedido no puede existir: no hay nada que forzar.
package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

func TestSinUbicacionNoSeFuerzaElRegistroYSeContestaLaPregunta(t *testing.T) {
	const from = "593993000139"
	store := conversation.NewMemStore()
	dice := "Las entregas son hasta las 20:00 😊 Apenas me compartas tu ubicación 📎, tu cilindro sale en camino 🚚"
	ag := agentIncidente(&modeloQueDice{respuestas: []string{dice}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "Disculpa hasta que hora hacen las entregas sector el vergel")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Texto, "20:00") || strings.Contains(res.Texto, "no pude completar") || res.Escalo {
		t.Errorf("se debía contestar el horario sin forzar ni derivar (escaló=%v): %q", res.Escalo, res.Texto)
	}
	if strings.Contains(res.Texto, "en camino") || !pideUbicacion(res.Texto) {
		t.Errorf("se quita la afirmación y se pide la ubicación: %q", res.Texto)
	}
	if _, hay := store.GetActivePedido(from); hay {
		t.Error("sin ubicación no se puede registrar un pedido")
	}
	t.Logf("al cliente le llega: %q", res.Texto)
}

func TestSinUbicacionUnaConfirmacionFalsaSeCambiaPorLaPregunta(t *testing.T) {
	const from = "593993000140"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{
		"¡Listo! Tu pedido está confirmado y el repartidor ya va en camino 🚚"}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Texto, "va en camino") || strings.Contains(res.Texto, "problema") || !pideUbicacion(res.Texto) {
		t.Errorf("sin ubicación no hay pedido: se le pide la ubicación (got %q)", res.Texto)
	}
}
