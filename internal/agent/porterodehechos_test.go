// Tests del portero. Cubren los cinco hechos operativos uno a uno con el estado VACÍO: si el bot
// afirma algo que el sistema no sostiene, el mensaje no sale.
//
// Los seis tickets que motivaron esto (#23, #33, #40, #51, #52, #53) dicen todos lo mismo: "el bot
// prometió al cliente que el equipo lo contactaría" sin que nadie lo avisara. Tres son de esta
// semana, y hay once candados que deberían haberlo cazado — cada uno mirando su propia lista de
// frases.
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// COBERTURA POR HECHO. Para cada uno: estado vacío + el bot lo afirma => no puede salir tal cual.
func TestNingunHechoSaleSinRespaldo(t *testing.T) {
	casos := []struct {
		hecho hechoOperativo
		frase string
	}{
		{hechoPedidoRegistrado, "¡Listo! Tu pedido quedó registrado y va en camino 🚚"},
		{hechoEntregaAgendada, "Perfecto, tu entrega quedó programada para hoy a las 18:00 📅"},
		{hechoEquipoAvisado, "Ya avisé al equipo para que te contacte enseguida 🙏"},
		{hechoRepartidorEnRuta, "El repartidor ya va en camino a tu dirección 🚚"},
	}
	for _, c := range casos {
		const from = "593999777001"
		store := conversation.NewMemStore()
		ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
		tn := &turno{}

		// El detector reconoce la frase...
		if !c.hecho.afirma(c.frase) {
			t.Errorf("%s: el detector no reconoce %q; el portero no puede protegerlo", c.hecho, c.frase)
			continue
		}
		// ...y el estado NO la sostiene.
		if a := ag.respaldado(tn, from, c.hecho); a {
			t.Errorf("%s: con el estado vacío se considera respaldado", c.hecho)
			continue
		}
		// Por tanto el mensaje se sustituye.
		salida := ag.revisarHechosAfirmados(tn, from, c.frase)
		if salida == c.frase {
			t.Errorf("%s: la afirmación sin respaldo salió tal cual: %q", c.hecho, salida)
		}
	}
}

// "Cancelado" va al revés que los demás: se respalda con la AUSENCIA de pedido. Afirmarlo con un
// pedido todavía vivo es lo que dejó a un cliente con un conductor en camino el 03/09.
func TestCanceladoConPedidoVivoNoSale(t *testing.T) {
	const from = "593999777002"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 4321) // el pedido SIGUE VIVO
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	tn := &turno{}

	frase := "Listo, cancelé tu pedido 🙏"
	if ag.respaldado(tn, from, hechoPedidoCancelado) {
		t.Fatal("con un pedido vivo, 'cancelado' no puede estar respaldado")
	}
	if salida := ag.revisarHechosAfirmados(tn, from, frase); salida == frase {
		t.Errorf("se le confirma una cancelación que no ocurrió: %q", salida)
	}

	// Y sin pedido, la misma frase SÍ sale: el portero no puede tapar la verdad.
	store.ClearActivePedido(from)
	if salida := ag.revisarHechosAfirmados(tn, from, frase); salida != frase {
		t.Errorf("una cancelación REAL debe poder confirmarse; salió %q", salida)
	}
}

// Con el estado que toca, los mensajes pasan intactos. El portero no es un censor: si el hecho es
// verdad, el tono del modelo se respeta.
func TestConRespaldoElMensajePasaIntacto(t *testing.T) {
	const from = "593999777003"
	store := conversation.NewMemStore()
	store.SetActivePedido(from, 999)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	tn := &turno{}

	for _, frase := range []string{
		"¡Listo! Tu pedido quedó registrado y va en camino 🚚",
		"El repartidor ya va en camino 🚚",
	} {
		if salida := ag.revisarHechosAfirmados(tn, from, frase); salida != frase {
			t.Errorf("un hecho REAL fue bloqueado: %q -> %q", frase, salida)
		}
	}
}

// El aviso al equipo se respalda con el ticket de ESTE turno (t.escalado) o con uno ya abierto.
func TestElAvisoAlEquipoSeRespaldaConElTicket(t *testing.T) {
	const from = "593999777004"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)

	// Sin ticket: no se puede prometer.
	if ag.respaldado(&turno{}, from, hechoEquipoAvisado) {
		t.Error("sin ticket no hay nadie avisado")
	}
	// Derivado en este turno: sí.
	if !ag.respaldado(&turno{escalado: true}, from, hechoEquipoAvisado) {
		t.Error("con la derivación hecha en este turno, la promesa es verdad")
	}
	// Con un ticket abierto de antes: también, alguien ya está al tanto.
	ag.crearTicketSoporte(from, "Cliente solicita hablar con una persona", "…")
	if !ag.respaldado(&turno{}, from, hechoEquipoAvisado) {
		t.Error("con un ticket abierto del cliente la promesa se sostiene")
	}
}

// LA CONVERSACIÓN NORMAL NO SE TOCA. El portero vigila cinco hechos, no el resto del diálogo: los
// precios, las dudas y las preguntas siguen siendo libres (memory/candados-no-guion.md).
func TestElPorteroNoSeMeteEnLaConversacion(t *testing.T) {
	const from = "593999777005"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	tn := &turno{}

	for _, frase := range []string{
		"El cilindro de 15kg cuesta $3.25, ya con envío incluido 😊",
		"Atendemos en Cuenca y sus parroquias urbanas y rurales.",
		"¿De qué color necesitas tu cilindro?",
		"¿Quieres que te lo agende para más tarde?", // OFRECIMIENTO, no afirmación
		"Claro, dime cuántos cilindros necesitas.",
		"No tienes ningún pedido en curso ahora mismo, así que no hay nada que cancelar 😊",
	} {
		if salida := ag.revisarHechosAfirmados(tn, from, frase); salida != frase {
			t.Errorf("se bloqueó conversación legítima: %q -> %q", frase, salida)
		}
	}
}

// GUARD DE COMPOSICIÓN: el texto del portero no puede activar a ningún detector.
//
// Es el bug del ticket #51 convertido en guard permanente: un candado emitió "ya avisé al equipo"
// (falso) y eso disparó al detector de aviso, que abrió un ticket describiendo el error del primer
// candado. Si el sustituto del portero afirmara algo, el portero se activaría con su propia salida.
func TestElSustitutoNoSeMuerdeLaCola(t *testing.T) {
	const from = "593999777006"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	tn := &turno{}

	// Se provoca una sustitución.
	sustituto := ag.revisarHechosAfirmados(tn, from, "Ya avisé al equipo, te contactarán enseguida 🙏")
	if sustituto == "" {
		t.Fatal("el portero devolvió vacío")
	}
	// Y su salida, reinyectada, no puede afirmar NADA.
	if malos := ag.hechosSinRespaldo(tn, from, sustituto); len(malos) != 0 {
		t.Errorf("el sustituto del portero afirma %v y se activaría a sí mismo: %q", malos, sustituto)
	}
	// Tampoco puede prometer un contacto ni dar el pedido por hecho.
	for _, det := range []struct {
		nombre string
		f      func(string) bool
	}{
		{"afirmaAvisoAlEquipo", afirmaAvisoAlEquipo},
		{"afirmaPedidoConfirmado", afirmaPedidoConfirmado},
		{"afirmaProgramado", afirmaProgramado},
		{"afirmaCancelado", afirmaCancelado},
		{"afirmaRepartidorEnRuta", afirmaRepartidorEnRuta},
	} {
		if det.f(sustituto) {
			t.Errorf("el sustituto dispara %s: %q", det.nombre, sustituto)
		}
	}
	// Y devuelve el turno al cliente en vez de dejarlo esperando.
	if !strings.Contains(sustituto, "?") {
		t.Errorf("el sustituto no le devuelve la palabra al cliente: %q", sustituto)
	}
}

// LOS SEIS TICKETS REALES. Si el portero no los atrapa, no sirve de nada.
//
// #23 (08-sep), #33 (19-sep), #40 (20-sep), #51 (25-sep), #52 (25-sep), #53 (26-sep): todos dicen
// "El bot prometió al cliente que el equipo lo contactaría" sin que nadie lo avisara. Seis
// clientes que colgaron esperando una llamada. Se prueban REDACCIONES DISTINTAS del mismo hecho,
// porque eso es lo que los once candados por frase no podían cubrir.
func TestLosSeisTicketsRealesHabrianSidoAtrapados(t *testing.T) {
	const from = "593999778001"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	tn := &turno{} // sin escalar y sin tickets: la promesa es falsa

	for _, promesa := range []string{
		"Ya avisé al equipo para que te contacte enseguida 🙏",
		"Nos pondremos en contacto contigo a la brevedad.",
		"Un asesor se comunicará contigo en unos minutos.",
		"Alguien del equipo te escribirá pronto.",
		"Ya notifiqué al equipo sobre tu caso.",
		"Te van a contactar para ayudarte con esto.",
	} {
		if salida := ag.revisarHechosAfirmados(tn, from, promesa); salida == promesa {
			t.Errorf("la promesa falsa salió tal cual: %q", promesa)
		}
	}
}
