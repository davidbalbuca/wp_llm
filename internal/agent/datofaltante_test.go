package agent

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// UN FALLO RECUPERABLE SE PREGUNTA, NO SE DISCULPA.
//
// INCIDENTE 25/09 (Edison Peñafiel, 593987647266): pidió 1 cilindro naranja, compartió su
// ubicación y aceptó las políticas de datos. El modelo llamó `registrar_pedido` sin el nombre,
// la herramienta lo rechazó ("Faltan datos del cliente"), y el candado del fantasma —que no
// distinguía este fallo de uno de cobertura o catálogo— respondió:
//
//	"Disculpa 🙏, no pude completar tu pedido ahora mismo. Ya avisé al equipo para que te
//	 contacte y lo resuelva. ¿Me confirmas tu ubicación 📎 mientras tanto?"
//
// Dos cosas mal: (1) una disculpa por un problema que no existía —solo faltaba preguntarle el
// nombre—, y (2) "ya avisé al equipo" era falso, y esa frase disparó al candado del aviso al
// equipo, que creó el ticket #51 describiendo el error de ESTE candado. Además el cliente tuvo
// que volver a mandar su ubicación, que ya había mandado.
//
// Lo vieron 3 clientes: 593978630801, 593983643258 y 593987647266.

// EL CASO REAL: falta el nombre.
func TestIncidente_FaltaElNombreSePideNoSeDisculpa(t *testing.T) {
	const from = "593987647266"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.893630, -79.012623)
	// Cédula sí, nombre no: exactamente el estado de Edison a las 12:22.
	store.SetProfile(from, conversation.Profile{Identificacion: "0923064976"})
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	fake := &modeloQueDice{respuestas: []string{
		"¡Gracias por tu paciencia! 🧡 Tu pedido de 1 cilindro naranja ya quedó registrado.",
	}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Naranja")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "listo gracias")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	bajo := strings.ToLower(res.Texto)

	// Lo que NO puede volver a salir.
	for _, prohibido := range []string{"avisé al equipo", "avise al equipo", "disculpa"} {
		if strings.Contains(bajo, prohibido) {
			t.Errorf("se disculpó por un fallo recuperable (contiene %q): %q", prohibido, res.Texto)
		}
	}
	// Lo que TIENE que salir: la pregunta por el dato que falta.
	if !strings.Contains(bajo, "nombre") {
		t.Errorf("no se le pidió el nombre, que era lo único que faltaba: %q", res.Texto)
	}
	// Y no puede pedirle otra vez la ubicación: ya la tenía.
	if strings.Contains(bajo, "ubicación") || strings.Contains(bajo, "ubicacion") {
		t.Errorf("se le volvió a pedir la ubicación que ya había compartido: %q", res.Texto)
	}
}

// Un fallo recuperable NO crea ticket: no hay nada que un operador tenga que atender.
func TestElDatoFaltanteNoCreaTicket(t *testing.T) {
	const from = "593987647260"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.89, -79.01)
	store.SetProfile(from, conversation.Profile{Identificacion: "0923064976"})
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	fake := &modeloQueDice{respuestas: []string{"¡Tu pedido quedó registrado!"}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Naranja")
	ag.anotarDelMensaje(from, "1")

	if _, err := ag.HandleMessage(context.Background(), from, "listo"); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if n := len(store.ListTickets(conversation.TicketAbierto, 10)); n != 0 {
		t.Errorf("se crearon %d ticket(s) por un fallo que se arregla preguntando", n)
	}
}

// Si falta la CÉDULA, la pregunta pasa por el camino de protección de datos: pedir una cédula sin
// autorización es justo lo que el candado de PDP impide, y este atajo no puede saltárselo.
func TestFaltaLaCedulaPasaPorElConsentimiento(t *testing.T) {
	const from = "593987647261"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.89, -79.01)
	// Sin cédula y SIN consentimiento respondido.
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"¡Tu pedido quedó registrado!"}}, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Naranja")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "listo")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	// El candado de PDP tuvo que intervenir: o se le pide la autorización, o se manda el menú
	// (en cuyo caso el texto va vacío y la pregunta viajó en el menú).
	bajo := strings.ToLower(res.Texto)
	pidioCedulaASecas := strings.Contains(bajo, "cédula") &&
		!strings.Contains(bajo, "polític") && !strings.Contains(bajo, "autoriz") && !res.MenuEnviado
	if pidioCedulaASecas {
		t.Errorf("se pidió la cédula sin pasar por el consentimiento: %q", res.Texto)
	}
}

// LA MITAD QUE NO HAY QUE ROMPER: un fallo de VERDAD (sin ubicación, catálogo caído) sí se
// disculpa. Si el candado dejara de hacerlo, el cliente se quedaría sin saber que algo falló.
func TestElFalloRealSiSeDisculpa(t *testing.T) {
	const from = "593987647262"
	store := conversation.NewMemStore()
	// Con cédula y nombre, pero SIN ubicación: el pedido no puede registrarse y no es por un
	// dato que se pregunte aquí (la ubicación se pide con el pin).
	store.SetProfile(from, conversation.Profile{Identificacion: "0923064976", Nombres: "Edison Peñafiel"})
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	fake := &modeloQueDice{respuestas: []string{"¡Tu pedido quedó registrado!"}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Naranja")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "listo")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto == "" {
		return // salió por menú: también es una respuesta válida
	}
	bajo := strings.ToLower(res.Texto)
	// Tiene que decirle algo útil: o se disculpa, o le pide la ubicación que falta.
	if !strings.Contains(bajo, "disculpa") && !strings.Contains(bajo, "ubicaci") {
		t.Errorf("ante un fallo real no se le dijo nada útil al cliente: %q", res.Texto)
	}
}

// GUARD ESTRUCTURAL: UN CANDADO NO PUEDE PROMETER EL AVISO AL EQUIPO SIN HABER DERIVADO.
//
// Es la lección de fondo del 25/09. El candado del fantasma respondió "Ya avisé al equipo" —sin
// que nadie hubiera avisado— y esa frase activó a `afirmaAvisoAlEquipo`, que creó el ticket #51
// describiendo el error del PRIMER candado. Un candado corrigiendo a otro es una cadena que nadie
// diseñó y que nadie vigila.
//
// EL ORDEN ES LO QUE IMPORTA, y por eso el guard mira SOLO esta promesa y no cualquier frase que
// active a un detector. En agent.go los candados de forzado (líneas ~510-541) corren ANTES de
// `afirmaAvisoAlEquipo` (línea ~548): lo que escriban puede dispararlo. Los demás detectores
// (cancelado, programado, confirmado) se evalúan ANTES de esos candados, así que un texto suyo
// que los mencione ya no puede reactivarlos en el mismo turno — "Listo, cancelé tu pedido" es la
// confirmación legítima de un candado que YA canceló de verdad, y prohibirla sería absurdo.
//
// El test es ESTRUCTURAL a propósito: no prueba el mensaje de hoy, prohíbe la FORMA. Si mañana
// alguien añade otro candado que promete el aviso sin derivar, esto falla.
//
// Excepción declarada y única: el `default` de forzarRegistroSiHaceFalta, donde el fallo es real
// y registrarPedido YA derivó. Ahí la frase es verdad.
func TestNingunCandadoPrometeElAvisoSinHaberDerivado(t *testing.T) {
	src := codigoSinComentariosDelAgente(t, "forzar.go")

	// Los textos que PUEDEN acabar en WhatsApp: los de `return "...", true` y los que se
	// concatenan para el cliente. Se excluyen los resúmenes de ticket (van al panel, no al chat).
	literales := regexp.MustCompile(`"([^"\\]|\\.){20,}"`).FindAllString(src, -1)
	if len(literales) < 3 {
		t.Fatalf("se encontraron solo %d textos en forzar.go; el guard no está mirando nada", len(literales))
	}

	const excepcionDeclarada = "Ya avisé al equipo para que te "
	var conAviso, sinJustificar []string
	for _, lit := range literales {
		texto := strings.Trim(lit, `"`)
		if !afirmaAvisoAlEquipo(texto) {
			continue
		}
		conAviso = append(conAviso, lit)
		if !strings.Contains(texto, excepcionDeclarada) {
			sinJustificar = append(sinJustificar, lit)
		}
	}

	// Los textos con la excepción declarada son los del fallo REAL, donde sí se derivó. El resto
	// tiene que justificarse aquí o desaparecer.
	for _, lit := range sinJustificar {
		if strings.Contains(lit, "tuve un problema al cancelar") {
			continue // cancelación fallida: registrarPedido/cancelar ya derivaron por su cuenta
		}
		t.Errorf("un candado promete el aviso al equipo y no está en la lista de los que derivan:\n  %s\n"+
			"Si de verdad deriva, añádelo a este test con el motivo; si no, quita la promesa.", lit)
	}
	if len(conAviso) == 0 {
		t.Error("ningún texto promete el aviso al equipo: el guard dejó de mirar lo que debía")
	}
}

// Y el candado del DATO FALTANTE en concreto no puede prometer nada de eso: ese fallo se arregla
// preguntando, no derivando. Es el caso exacto del 25/09.
func TestElTextoDelDatoFaltanteNoPrometeNada(t *testing.T) {
	for _, falta := range []string{"nombre", "cedula"} {
		store := conversation.NewMemStore()
		from := "59399900000" + falta[:1]
		store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})
		ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)

		texto := ag.pedirElDatoQueFalta(from, falta)
		if afirmaAvisoAlEquipo(texto) {
			t.Errorf("falta %q: el texto promete aviso al equipo sin derivar: %q", falta, texto)
		}
		if afirmaPedidoConfirmado(texto) {
			t.Errorf("falta %q: el texto afirma un pedido que no existe: %q", falta, texto)
		}
		if strings.Contains(strings.ToLower(texto), "disculpa") {
			t.Errorf("falta %q: se disculpa por un fallo que se arregla preguntando: %q", falta, texto)
		}
	}
}

// REGRESIÓN DEL CRITERIO DEL DUEÑO (25/09): "la idea es que conteste cosas como precios o dudas
// del cliente siempre y cuando sean relacionadas al pedido del gas".
//
// Edison preguntó "q precio tiene" en mitad del menú de consentimiento y el bot respondió el
// precio y retomó. Eso es lo que se quiere, y ningún candado puede estorbarlo: los candados
// existen para que el bot no afirme falsedades, no para forzar al cliente por un guion.
func TestUnaDudaDePrecioAMitadDeFlujoSeContesta(t *testing.T) {
	const from = "593987647263"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.89, -79.01)

	respuesta := "¡Claro! El cilindro de GAS 15KG cuesta $3.25 🧡 (ya incluye envío e instalación)."
	ag := agentIncidente(&modeloQueDice{respuestas: []string{respuesta}}, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Naranja")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "q precio tiene")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !strings.Contains(res.Texto, "3.25") {
		t.Errorf("un candado se comió la respuesta al precio: %q", res.Texto)
	}
}
