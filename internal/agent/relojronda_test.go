package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// CONTESTAR RÁPIDO NO PUEDE GASTAR LAS RONDAS DE ESPERA.
//
// INCIDENTE 25/09 (Edison Peñafiel, 593987647266): su pedido quedó sin repartidor y el bot le
// ofreció esperar. Contestó "Esperar" dos veces, atento, y agotó sus TRES rondas en 56 segundos:
//
//	12:26:55  ronda 0   [Esperar / Programar / Cancelar]
//	12:27:14  "Esperar"
//	12:27:16  ronda 1   <- 2 segundos después
//	12:27:47  "Esperar"
//	12:27:51  ronda 2   <- ya sin la opción de esperar
//
// En el modo con búsqueda del backend, el único freno era EsperandoRespuesta: al contestar se
// limpiaba y, 7 segundos más tarde (lo que tarda el bot en volver a consultar el estado), el
// backend seguía en SIN_CONDUCTOR y se le ofrecía la ronda siguiente. El cliente atento se
// castigaba a sí mismo; el que ignoraba el mensaje conservaba sus rondas. Y
// BOT_ESPERA_RONDA_MIN —el parámetro del .env que debía gobernar esto— no se aplicaba.

// esperaConRonda arma un agente con la espera lista y el reloj de ronda configurado.
func esperaConRonda(t *testing.T, from string, ronda time.Duration) (*Agent, conversation.Store, *int) {
	t.Helper()
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: ronda}
	enviados := 0
	ag.enviarMenu = func(string, string, []string) error { enviados++; return nil }
	return ag, store, &enviados
}

// EL CASO REAL: contestar "Esperar" dos veces seguidas NO agota las rondas.
func TestIncidente_ContestarRapidoNoAgotaLasRondas(t *testing.T) {
	const from = "593987647266"
	ag, store, enviados := esperaConRonda(t, from, 10*time.Minute)

	// Ronda 0: el backend dice que no hay nadie y se le pregunta.
	ag.ofrecerEsperaAlCliente(from, nil)
	// El cliente contesta enseguida, como Edison.
	ag.liberarRespuestaDeEspera(from)
	// El bot vuelve a consultar el estado a los 7 segundos, y otra vez, y otra.
	ag.ofrecerEsperaAlCliente(from, nil)
	ag.ofrecerEsperaAlCliente(from, nil)
	ag.liberarRespuestaDeEspera(from)
	ag.ofrecerEsperaAlCliente(from, nil)

	if *enviados != 1 {
		t.Errorf("se le mandaron %d menús en segundos; debía ser 1 (las demás rondas tocan a los 10 min)", *enviados)
	}
	w, _ := store.GetPendingWait(from)
	if w.RondaEspera != 1 {
		t.Errorf("el cliente quedó en la ronda %d tras contestar rápido; debía seguir en la 1", w.RondaEspera)
	}
}

// Y pasada la ronda, la siguiente SÍ llega: el freno acota, no bloquea.
func TestLaRondaSiguienteLlegaPasadoElTiempo(t *testing.T) {
	const from = "593987647270"
	ag, store, enviados := esperaConRonda(t, from, 10*time.Minute)

	ag.ofrecerEsperaAlCliente(from, nil)
	ag.liberarRespuestaDeEspera(from)

	// Han pasado 11 minutos (se retrocede el sello en vez de dormir).
	w, _ := store.GetPendingWait(from)
	w.UltimaRondaAt = time.Now().Add(-11 * time.Minute).Unix()
	store.SetPendingWait(from, w)

	ag.ofrecerEsperaAlCliente(from, nil)
	if *enviados != 2 {
		t.Errorf("pasados 11 minutos se enviaron %d menús; debían ser 2", *enviados)
	}
}

// EL FRENO VIEJO SIGUE VIVO: los dos se suman, no se sustituyen. Mientras el cliente no conteste
// no se le repite el menú, aunque haya pasado el tiempo de sobra.
func TestElFrenoDeEsperandoRespuestaSigueVivo(t *testing.T) {
	const from = "593987647271"
	ag, store, enviados := esperaConRonda(t, from, 10*time.Minute)

	ag.ofrecerEsperaAlCliente(from, nil)
	// Pasa una hora y el cliente NO contesta.
	w, _ := store.GetPendingWait(from)
	w.UltimaRondaAt = time.Now().Add(-time.Hour).Unix()
	store.SetPendingWait(from, w)

	ag.ofrecerEsperaAlCliente(from, nil)
	ag.ofrecerEsperaAlCliente(from, nil)

	if *enviados != 1 {
		t.Errorf("se repitió el menú %d veces a quien no ha contestado; debía ser 1", *enviados)
	}
}

// BOT_ESPERA_RONDA_MIN TIENE QUE TENER EFECTO. Era config muerta: duracionDeLaRonda solo se
// usaba en el camino sin backend, y prod corre con BOT_USAR_BUSQUEDA=true. El .env la
// documentaba como si funcionara, así que subirla para dar más margen no habría hecho nada.
func TestLaDuracionDeLaRondaGobiernaElFreno(t *testing.T) {
	const from = "593987647272"
	// Ronda LARGA: tras contestar, la siguiente no llega ni a los 20 minutos.
	ag, store, enviados := esperaConRonda(t, from, 30*time.Minute)

	ag.ofrecerEsperaAlCliente(from, nil)
	ag.liberarRespuestaDeEspera(from)
	w, _ := store.GetPendingWait(from)
	w.UltimaRondaAt = time.Now().Add(-20 * time.Minute).Unix()
	store.SetPendingWait(from, w)

	ag.ofrecerEsperaAlCliente(from, nil)
	if *enviados != 1 {
		t.Errorf("con ronda de 30 min se ofreció otra a los 20; enviados=%d", *enviados)
	}

	// Pasados los 30, sí.
	w, _ = store.GetPendingWait(from)
	w.UltimaRondaAt = time.Now().Add(-31 * time.Minute).Unix()
	store.SetPendingWait(from, w)
	ag.ofrecerEsperaAlCliente(from, nil)
	if *enviados != 2 {
		t.Errorf("pasada la ronda de 30 min no se ofreció la siguiente; enviados=%d", *enviados)
	}
}

// La PRIMERA ronda no espera: es el instante en que el backend acaba de decir que no hay nadie.
// Si esperara, el cliente estaría 10 minutos sin saber nada de su pedido.
func TestLaPrimeraRondaNoEspera(t *testing.T) {
	if !tocaOtraRonda(time.Time{}, time.Now(), 10*time.Minute) {
		t.Error("la primera ronda esperó: el cliente se quedaría sin noticias del pedido")
	}
	// Y un sello en cero se lee como "nunca", no como 1970 (que dejaría pasar siempre).
	if got := ultimaRondaDe(conversation.PendingWait{}); !got.IsZero() {
		t.Errorf("un sello sin poner dio %v en vez de cero", got)
	}
}

func TestTocaOtraRondaEsElReloj(t *testing.T) {
	base := time.Date(2026, 9, 25, 12, 26, 55, 0, time.UTC)
	casos := []struct {
		nombre   string
		ahora    time.Time
		duracion time.Duration
		quiero   bool
	}{
		{"2 segundos después (el caso de Edison)", base.Add(2 * time.Second), 10 * time.Minute, false},
		{"56 segundos después", base.Add(56 * time.Second), 10 * time.Minute, false},
		{"justo al cumplirse", base.Add(10 * time.Minute), 10 * time.Minute, true},
		{"pasada de sobra", base.Add(30 * time.Minute), 10 * time.Minute, true},
	}
	for _, c := range casos {
		if got := tocaOtraRonda(base, c.ahora, c.duracion); got != c.quiero {
			t.Errorf("%s: tocaOtraRonda=%v (esperado %v)", c.nombre, got, c.quiero)
		}
	}
}

// Al aceptar esperar, el bot dice CUÁNDO volverá a escribir, y ese número sale de la config.
// Decir solo "te aviso apenas se asigne" y repreguntar a los segundos se lee como un bot roto.
func TestAlEsperarSeLeDiceElPlazoReal(t *testing.T) {
	const from = "593987647273"
	store := conversation.NewMemStore()
	store.SetPendingWait(from, esperaDePrueba())
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59", EsperaRonda: 10 * time.Minute}
	ag.enviarMenu = func(string, string, []string) error { return nil }

	respuesta, ok := ag.ResponderMenuEspera(from, "Esperar")
	if !ok {
		t.Fatal("no se resolvió la respuesta al menú de espera")
	}
	if !strings.Contains(respuesta, "10") {
		t.Errorf("no se le dijo en cuántos minutos se le escribe: %q", respuesta)
	}
}
