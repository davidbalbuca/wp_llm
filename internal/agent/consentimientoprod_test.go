package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// LOS DOS AGUJEROS QUE PRODUCCIÓN ENCONTRÓ (21/09).
//
// El diseño original tenía tres capas (candado, interceptor, compuerta) y aun así por aquí pasaron
// cuatro consentimientos que no valen nada. Los dos fallos son de la misma familia: el bot trataba
// "todavía no respondió" como si fuera "dijo que sí".
//
//  1. LA COMPUERTA SOLO CERRABA CON UN NO EXPLÍCITO. Con el menú enviado y sin responder, las
//     herramientas seguían abiertas. El 21/09 a las 09:06 Carlos (593986140905) recibió el menú,
//     lo ignoró, escribió su cédula suelta y a las 09:07:26 su pedido estaba registrado. El
//     consentimiento se grabó a las 09:07:42: DIECISÉIS SEGUNDOS DESPUÉS del pedido.
//
//  2. MIENTRAS ESTABA PENDIENTE, CUALQUIER PALABRA AFIRMATIVA CONTABA. respuestasAfirmativas
//     tiene "ok", "listo", "dale", "👍" — pensadas para confirmar una dirección, donde equivocarse
//     cuesta un viaje. Aquí lo que se firma es el permiso para tratar la cédula de una persona.
//     Cuatro de diecisiete consentimientos de producción (24%) salieron de un "Ok", un "Listo", un
//     "👍" y un "Si" dichos respondiendo a OTRA cosa.
//
// La regla que sale de esto: EL SILENCIO NO ES UN SÍ, Y UN "OK" A OTRA PREGUNTA TAMPOCO.

// --- 1. Pendiente = compuerta cerrada ---

// EL CASO CARLOS, tal cual pasó. Es el test que más importa de este archivo.
func TestConElMenuPendienteLaCedulaSueltaNoRegistraNada(t *testing.T) {
	const from = "593986140905"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetLocation(from, -2.829392, -78.985058)
	// Se le mandó el menú y NO ha respondido: ni aceptó ni negó.
	store.SetConsentimientoPendiente(from)

	// Carlos escribe su cédula sin tocar ningún botón, y el modelo la toma.
	salida := ag.registrarPedido(&turno{}, from, map[string]any{
		"identificacion":    "0103519336",
		"nombres_completos": "Carlos Fajardo",
		"color":             "BLANCO",
		"cantidad":          5,
	})

	// Se comprueba que se detuvo POR EL CONSENTIMIENTO y no por otra validación cualquiera (un
	// dato que falte, el catálogo caído): si no, el test pasaría sin haber ejercido la compuerta.
	bajo := strings.ToLower(salida)
	if !strings.Contains(bajo, "proteccion de datos") && !strings.Contains(bajo, "protección de datos") {
		t.Fatalf("el pedido no se detuvo por el consentimiento, sino por otra cosa: %q", salida)
	}
	if esp.llamo("startOrder") || esp.llamo("wppGetOrCreateClient") {
		t.Error("se registró el pedido de alguien que todavía no respondió al menú de datos")
	}
	if p, hay := store.GetProfile(from); hay && p.Identificacion != "" {
		t.Errorf("se guardó el perfil de quien no ha autorizado nada: %+v", p)
	}
}

// Y tampoco se consulta su cédula en el backend: consultarla YA es tratarla.
func TestConElMenuPendienteLaCedulaNoSeConsulta(t *testing.T) {
	const from = "593999900020"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimientoPendiente(from)

	ag.verificarCliente(&turno{}, from, map[string]any{"identificacion": "0103519336"})

	if esp.llamo("clientExists") {
		t.Error("se consultó la cédula de alguien que no ha respondido al menú de datos")
	}
	if esp.aceptoPDP > 0 {
		t.Error("se registró una aceptación en el backend que el cliente nunca dio")
	}
}

// La contracara: quien YA aceptó sigue pasando. Sin este test, un mutante que bloquee a todo el
// mundo (y de paso rompa el negocio entero) sobreviviría a los dos de arriba.
func TestConElConsentimientoDadoElPedidoSiPasa(t *testing.T) {
	const from = "593999900021"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	ag.verificarCliente(&turno{}, from, map[string]any{"identificacion": "0103519336"})

	if !esp.llamo("clientExists") {
		t.Error("se bloqueó a un cliente que SÍ había aceptado; la compuerta cierra de más")
	}
}

// Y el cliente de siempre, al que nunca se le preguntó nada porque el bot ya lo conoce, tampoco
// se puede quedar fuera: "a los que ya están registrados no podemos hacer nada, deben continuar
// igual" (pedido del dueño, 18/09).
func TestSinMenuNiRespuestaElClienteDeSiempreNoSeBloquea(t *testing.T) {
	const from = "593999900022"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	// Ni consentimiento registrado ni menú pendiente: nunca pasó por aquí.

	ag.verificarCliente(&turno{}, from, map[string]any{"identificacion": "0103519336"})

	if !esp.llamo("clientExists") {
		t.Error("se bloqueó a un cliente al que nunca se le preguntó; eso rompe a los de siempre")
	}
}

// A QUIEN NO RESPONDIÓ NO SE LE DICE QUE SE NEGÓ. Son dos situaciones opuestas: al que se negó
// hay que despedirlo con amabilidad; al que no contestó hay que recordarle que conteste, porque
// su venta sigue viva. Acusarlo de algo que no hizo la mata.
func TestAQuienNoRespondioNoSeLeDiceQueSeNego(t *testing.T) {
	const from = "593999900023"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)

	instruccion := ag.instruccionSinConsentimiento(from, "NO se registró el pedido")

	bajo := strings.ToLower(instruccion)
	if strings.Contains(bajo, "no autorizó") || strings.Contains(bajo, "no autorizo") {
		t.Errorf("se le trata como si hubiera negado el permiso, y solo no ha respondido: %q", instruccion)
	}
	if !strings.Contains(bajo, "todavia no") && !strings.Contains(bajo, "todavía no") {
		t.Errorf("no se le dice al modelo que la respuesta está pendiente: %q", instruccion)
	}
}

// Y al que SÍ se negó se le sigue tratando como tal.
func TestAQuienSeNegoSeLeDiceQueSeNego(t *testing.T) {
	const from = "593999900024"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})

	instruccion := ag.instruccionSinConsentimiento(from, "NO se registró el pedido")

	if !strings.Contains(strings.ToLower(instruccion), "autoriz") {
		t.Errorf("a quien negó el permiso no se le trata como tal: %q", instruccion)
	}
}

// --- 2. Un "sí" solo vale si contesta al menú de datos ---

// menuFueLoUltimo deja el historial como queda cuando el menú de datos es lo último que el bot
// preguntó: es el estado normal justo después de enviarlo.
func menuFueLoUltimo(store conversation.Store, from string) {
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, cuerpoConsentimiento())
}

// elBotDijoOtraCosaDespues reproduce el escenario de los cuatro consentimientos falsos: salió el
// menú y DESPUÉS el bot preguntó o dijo otra cosa, así que el cliente ya no está contestando al
// menú.
func elBotDijoOtraCosaDespues(store conversation.Store, from, loOtro string) {
	menuFueLoUltimo(store, from)
	store.AppendUser(from, "...")
	store.AppendModel(from, loOtro)
}

// LOS CUATRO CONSENTIMIENTOS FALSOS DE PRODUCCIÓN, con el mensaje al que de verdad contestaban.
func TestUnSiQueContestaOtraCosaNoEsConsentimiento(t *testing.T) {
	casos := []struct {
		telefono string
		dijo     string
		aQue     string // lo último que el bot había dicho, sacado del historial real
	}{
		{"593986140905", "Ok", "¡Tu pedido quedó registrado! 👍 Estamos buscando al repartidor más cercano."},
		{"593978630801", "Listo", "¡Perfecto! 🚚 Ya estoy buscando un repartidor para ti. Te aviso apenas se asigne."},
		{"593998015677", "👍", "¡Excelente! Tu pedido está confirmado 🎉 Tu repartidor es Jonathan Sacta."},
		{"593999900030", "dale", "Estoy esperando que compartas tu ubicación 📎"},
		{"593999900031", "claro", "¿Quieres agregar más cilindros de otros colores?"},
	}
	for _, c := range casos {
		t.Run(c.dijo, func(t *testing.T) {
			store := conversation.NewMemStore()
			ag := agentDePrueba(nil, store)
			elBotDijoOtraCosaDespues(store, c.telefono, c.aQue)

			_, manejado := ag.ResponderConsentimiento(c.telefono, c.dijo)

			if manejado {
				t.Errorf("%q se tomó como respuesta al menú de datos, y contestaba a %q", c.dijo, c.aQue)
			}
			if _, hay := store.GetConsentimiento(c.telefono); hay {
				t.Errorf("%q quedó registrado como consentimiento legal", c.dijo)
			}
			// Y la espera sigue en pie: hay que volver a preguntarle.
			if !store.ConsentimientoPendiente(c.telefono) {
				t.Errorf("con %q se perdió la espera; el cliente se queda sin que le pregunten", c.dijo)
			}
		})
	}
}

// EL "SI" QUE SÍ VALÍA. Andreina (593969027308, 20/09 07:06): el bot le contestó cuánto tarda la
// entrega Y le volvió a preguntar por las políticas en el mismo mensaje, así que su "Si" sí
// estaba aceptando. Distinguirlo de los cuatro de arriba es justo el objetivo del criterio.
func TestElSiQueContestaAlMenuSiVale(t *testing.T) {
	const from = "593969027308"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, "En unos 30 a 45 minutos aproximadamente 😊\n\n"+
		"Ahora sí, ¿aceptas nuestras políticas de protección de datos?")

	_, manejado := ag.ResponderConsentimiento(from, "Si")

	if !manejado {
		t.Fatal("el 'Si' que contestaba al menú no se reconoció: el cliente queda sin poder aceptar")
	}
	c, hay := store.GetConsentimiento(from)
	if !hay || !c.Acepta {
		t.Errorf("no quedó registrada la aceptación: %+v hay=%v", c, hay)
	}
}

// EL BLOQUEO PERMANENTE QUE ESTO EVITA, y que el primer intento de arreglo sí provocaba.
//
// Mucha gente escribe "si" aunque tenga el botón delante. Si ese "si" no vale y el recordatorio
// tampoco le acepta nada, el cliente se queda dando vueltas para siempre: no puede aceptar, no
// puede negar, y la compuerta le impide pedir gas. Un cliente bloqueado en silencio es peor que
// el bug que veníamos a arreglar.
func TestElSiEscritoConElMenuDelanteNoDejaAlClienteAtrapado(t *testing.T) {
	const from = "593999900050"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	menuFueLoUltimo(store, from)

	if _, manejado := ag.ResponderConsentimiento(from, "si"); !manejado {
		t.Fatal("un 'si' escrito con el menú recién enviado no se reconoció: el cliente que " +
			"escribe en vez de tocar el botón queda atrapado sin forma de continuar")
	}
	if ag.consentimientoNiega(from) {
		t.Error("el cliente sigue bloqueado después de haber aceptado")
	}
}

// El botón literal vale SIEMPRE, aunque el hilo se haya movido: es inequívoco venga cuando venga.
func TestElBotonLiteralValeAunqueElHiloSeHayaMovido(t *testing.T) {
	for _, caso := range []struct {
		toco   string
		quiere bool
	}{
		{BotonAceptoDatos, true},
		{BotonNoAceptoDatos, false},
		{"si, acepto", true}, // escrito a mano: normalizarRespuesta le quita la coma
		{"SÍ, ACEPTO", true},
		{"no acepto", false},
	} {
		t.Run(caso.toco, func(t *testing.T) {
			const from = "593999900051"
			store := conversation.NewMemStore()
			ag := agentDePrueba(nil, store)
			// El bot dijo otra cosa después del menú: el sí/no suelto ya no valdría.
			elBotDijoOtraCosaDespues(store, from, "¡Tu pedido quedó registrado!")

			_, manejado := ag.ResponderConsentimiento(from, caso.toco)

			if !manejado {
				t.Fatalf("no se reconoció %q, que es el botón del menú", caso.toco)
			}
			c, hay := store.GetConsentimiento(from)
			if !hay {
				t.Fatalf("no se registró nada tras tocar %q", caso.toco)
			}
			if c.Acepta != caso.quiere {
				t.Errorf("con %q quedó Acepta=%v, se esperaba %v", caso.toco, c.Acepta, caso.quiere)
			}
		})
	}
}

// --- 3. Las salidas de emergencia: nadie se queda bloqueado para siempre ---

// La espera CADUCA. Sin esto, quien recibió el menú, no contestó y vuelve dos días después se
// queda sin poder pedir gas para siempre, y en silencio.
func TestLaEsperaDelMenuCaduca(t *testing.T) {
	const from = "593999900052"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)

	if !ag.consentimientoNiega(from) {
		t.Fatal("con la espera recién puesta el cliente debería estar bloqueado")
	}

	forzar, ok := store.(interface{ ForzarEsperaPDP(string, time.Time) })
	if !ok {
		t.Skip("el store de prueba no permite envejecer la espera")
	}
	forzar.ForzarEsperaPDP(from, time.Now().Add(-conversation.VidaEsperaPDP-time.Hour))

	if ag.consentimientoNiega(from) {
		t.Error("la espera caducada sigue bloqueando: el cliente no puede pedir gas nunca más")
	}
}

// Y el cierre de ciclo la limpia: la RESPUESTA sobrevive, la ESPERA no.
func TestElCierreDeCicloLimpiaLaEsperaPeroNoLaRespuesta(t *testing.T) {
	const from = "593999900053"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)

	ag.cerrarCicloDeConversacion(from, "prueba")

	if store.ConsentimientoPendiente(from) {
		t.Error("la espera sobrevivió al cierre de ciclo: el cliente arranca su próxima " +
			"conversación bloqueado por un menú que ya no está en pantalla")
	}
	if ag.consentimientoNiega(from) {
		t.Error("el cliente sigue bloqueado tras cerrar el ciclo")
	}

	// La respuesta REAL, en cambio, tiene que sobrevivir: no se le vuelve a preguntar.
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})
	ag.cerrarCicloDeConversacion(from, "prueba")
	if c, hay := store.GetConsentimiento(from); !hay || !c.Acepta {
		t.Error("el cierre de ciclo borró un consentimiento que el cliente sí dio")
	}
}
