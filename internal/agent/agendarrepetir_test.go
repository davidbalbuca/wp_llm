package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// DE NOCHE, AL CLIENTE QUE YA COMPRÓ SE LE OFRECE AGENDAR, NO "ENVIAR" (dueño, 13/10): el saludo
// decía "ahora no estamos atendiendo" y justo debajo "¿Te envío lo mismo de la última vez?".

func agenteDeNoche(store conversation.Store, hora int) *Agent {
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "20:30", BotDiasLaborables: "1-7"}
	ag.reloj = func() time.Time { return time.Date(2026, 10, 13, hora, 0, 0, 0, zonaEcuador) }
	return ag
}

func menuDeApertura(t *testing.T, ag *Agent, from string) (string, []string) {
	t.Helper()
	var cuerpo string
	var opciones []string
	ag.enviarMenu = func(_ string, c string, o []string) error { cuerpo, opciones = c, o; return nil }
	ag.DejarBienvenidaPendiente(from, "saludo")
	if _, manejado := ag.ResponderAperturaConColores(from, "hola"); !manejado {
		t.Fatal("la apertura no se resolvió en código")
	}
	return cuerpo, opciones
}

func TestDeNocheElBotonEsAgendar(t *testing.T) {
	const from = "593900950001"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1})
	cuerpo, opciones := menuDeApertura(t, agenteDeNoche(store, 22), from)

	if !strings.Contains(cuerpo, "¿Te agendo lo mismo de la última vez: 1 BLANCO?") {
		t.Errorf("de noche se debía ofrecer agendar: %q", cuerpo)
	}
	if len(opciones) != 2 || opciones[0] != BotonAgendarRepetir {
		t.Errorf("opciones = %v, se esperaba [%s, %s]", opciones, BotonAgendarRepetir, BotonCambiarPedido)
	}
}

func TestDeDiaSigueSiendoRepetir(t *testing.T) {
	const from = "593900950002"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1})
	cuerpo, opciones := menuDeApertura(t, agenteDeNoche(store, 10), from)

	if !strings.Contains(cuerpo, "¿Te envío lo mismo") || opciones[0] != BotonRepetirPedido {
		t.Errorf("de día se ofrece enviarlo ya: %q %v", cuerpo, opciones)
	}
}

func TestDeMadrugadaElBotonAgendaParaHoy(t *testing.T) {
	const from = "593900950003"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "AMARILLO", Cantidad: 2})
	reply, _ := agenteDeNoche(store, 5).ResponderAgendarRepetir(from, "Agendar")
	if !strings.Contains(reply, "hoy desde las 7 am") {
		t.Errorf("a las 5 am se agenda para hoy: %q", reply)
	}
}

func TestAgendarSinUbicacionDejaLaFichaProgramadaYLaPide(t *testing.T) {
	const from = "593900950004"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2})
	ag := agenteDeNoche(store, 22)

	reply, manejado := ag.ResponderAgendarRepetir(from, "Agendar")
	if !manejado {
		t.Fatal("el botón Agendar no se resolvió en código")
	}
	p, hay := store.GetPedidoEnCurso(from)
	if !hay || p.Flujo != conversation.FlujoProgramacion || p.Hora != "07:00" || p.Color != "BLANCO" || p.Cantidad != 2 {
		t.Fatalf("la ficha debía quedar programada para las 07:00 con su último pedido: %+v", p)
	}
	if !strings.Contains(reply, "2 BLANCO") || !strings.Contains(reply, "mañana desde las 7 am") ||
		!strings.Contains(reply, "ubicación") {
		t.Errorf("respuesta: %q", reply)
	}
}

func TestAgendarDeDiaNoSeInterpreta(t *testing.T) {
	const from = "593900950005"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1})
	if _, manejado := agenteDeNoche(store, 10).ResponderAgendarRepetir(from, "Agendar"); manejado {
		t.Error("en horario el botón Agendar no existe: lo atiende el flujo normal")
	}
}

func TestElSaludoDiceQueElSeEncarga(t *testing.T) {
	saludo := textoBienvenidaA("Ana", time.Date(2026, 10, 13, 10, 0, 0, 0, zonaEcuador))
	for _, quiero := range []string{"tú no te preocupes", "yo te busco el distribuidor más cercano", "te lo envío en minutos"} {
		if !strings.Contains(saludo, quiero) {
			t.Errorf("el saludo no dice %q: %q", quiero, saludo)
		}
	}
}
