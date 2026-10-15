// "TUVE UN PROBLEMA AL REGISTRAR TU PEDIDO" SIN HABER PEDIDO NADA (06/10).
//
// 4 veces entre el 23/09 y el 06/10 el bot contestó eso a un cliente que no había pedido nada:
// el candado del pedido fantasma forzaba un registro imposible y su texto de rescate culpaba a un
// "problema". Dos causas: una entrega YA agendada que el candado no veía (Edgar, 06/10 06:48) y
// el primer mensaje fuera de horario (23/09 y 02/10, ~22:40).
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

func TestCasoEdgarRecordarUnaEntregaAgendadaNoEsUnFantasma(t *testing.T) {
	const from = "593993419486"
	store := conversation.NewMemStore()
	store.CreateScheduled(conversation.ScheduledOrder{Phone: from, Cantidad: 20, ColorNombre: "BLANCO",
		HoraPropuesta: time.Now().Add(time.Hour).Unix(), Estado: conversation.SchedulePendiente})
	dice := "Tu entrega quedó programada para hoy a las 08:00 con 20 cilindros blancos 😊"
	ag := agentIncidente(&modeloQueDice{respuestas: []string{dice}}, store)

	res, err := ag.HandleMessage(context.Background(), from, "https://www.facebook.com/share/v/1eKatFx/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Texto, "tuve un problema") || !strings.Contains(res.Texto, "quedó programada") {
		t.Errorf("la entrega SÍ está agendada: el recordatorio del modelo es verdad (got %q)", res.Texto)
	}
}

func TestSinDatosNoSeCulpaAUnProblema(t *testing.T) {
	const from = "593998802233"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	got, ok := ag.forzarRegistroSiHaceFalta(&turno{}, from)
	if !ok || strings.Contains(got, "problema") || !strings.Contains(got, "de qué color es tu cilindro") {
		t.Errorf("sin datos no se intentó registrar nada: se pregunta el color con amabilidad (got %q)", got)
	}
}

func TestFueraDeHorarioNoSeFuerzaUnPedidoInmediato(t *testing.T) {
	const from = "593983984883"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{
		"¡Listo! Tu pedido está confirmado y el repartidor ya va en camino 🚚"}}, store)
	ahora := time.Now().In(zonaEcuador)
	// Cerrado justo ahora, sea la hora que sea: abre dentro de una hora y cierra en dos.
	ag.cfg = config.Config{BotHorarioInicio: ahora.Add(time.Hour).Format("15:04"),
		BotHorarioFin: ahora.Add(2 * time.Hour).Format("15:04")}
	if ag.dentroDeHorario(ahora) {
		t.Skip("no se pudo armar un horario cerrado a esta hora (cerca de medianoche)")
	}

	res, err := ag.HandleMessage(context.Background(), from, "Deseo pedir GAS 😄")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Texto, "problema") || strings.Contains(res.Texto, "va en camino") {
		t.Errorf("fuera de horario no se fuerza un pedido ni se culpa a un problema (got %q)", res.Texto)
	}
	if !strings.Contains(res.Texto, "de qué color es tu cilindro") {
		t.Errorf("debía preguntar lo que falta (el color): %q", res.Texto)
	}
}
