package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// NADIE DA SU CÉDULA SIN HABER AUTORIZADO ANTES QUE LA TRATEMOS.
//
// Pedido del dueño (18/09): "justo antes de pedir la cédula debe indicar que por políticas de
// protección de datos necesitamos su cédula y él debe aceptar o negar; para ello le debemos poner
// el botón. Solo si presiona sí, o escribe sí, ahí le pedimos la cédula, antes no."
//
// Lo que se protege aquí NO es la conversación: es que un dato personal no entre a la base cuando
// su dueño dijo que no. Por eso hay tres capas y cada una se prueba aparte (ver consentimiento.go):
// el candado (UX), el interceptor (el sí/no en código) y la compuerta (la garantía).

// --- 1. El detector: ¿está pidiendo la cédula? ---

func TestSeReconocenLasFormasDePedirLaCedula(t *testing.T) {
	pide := []string{
		"Para registrar tu pedido necesito tu cédula, por favor",
		"¿Me compartes tu número de cédula?",
		"Regálame tu cédula para continuar 😊",
		"Indícame tu identificación por favor",
		"Perfecto, ahora dime tu cédula de identidad",
		"¿Me das tu cédula? Es para registrar el pedido",
	}
	for _, texto := range pide {
		if !pideLaCedula(texto) {
			t.Errorf("no se detectó la petición de cédula en %q: el candado no se dispararía y el "+
				"cliente daría su cédula sin haber autorizado nada", texto)
		}
	}
}

// Y NO se dispara con lo que no es una petición de cédula. Un candado que salta de más rompe la
// conversación normal, que es el otro error caro.
func TestNoSeConfundeConOtrasFrases(t *testing.T) {
	noPide := []string{
		"¡Hola! 👋 ¿Qué cilindro necesitas hoy?",
		"Tu pedido va en camino 🚚",
		"Ya tengo tu cédula registrada, gracias",
		"No necesito tu cédula, ya estás registrado",
		"Compárteme tu ubicación por WhatsApp 📎",
		"¿De qué color quieres el cilindro?",
	}
	for _, texto := range noPide {
		if pideLaCedula(texto) {
			t.Errorf("se detectó una petición de cédula donde no hay ninguna: %q. El candado "+
				"reemplazaría un mensaje normal por el menú de consentimiento", texto)
		}
	}
}

// --- 2. El candado: la petición de cédula se reemplaza por el menú ---

// agenteConWhatsAppFalso devuelve un agente cuyo SendMenu no sale a internet. WhatsApp no está
// configurado en los tests, así que el menú falla y el candado cae a preguntar por TEXTO: sirve
// igual para comprobar lo que importa, que la cédula NO se pide sin permiso.
func agenteConsentimiento(t *testing.T) (*Agent, conversation.Store, string) {
	t.Helper()
	const from = "593999900001"
	store := conversation.NewMemStore()
	return agentDePrueba(nil, store), store, from
}

func TestNoSePideLaCedulaSinConsentimiento(t *testing.T) {
	ag, _, from := agenteConsentimiento(t)

	salida := ag.revisarPeticionDeCedula(&turno{}, from, "Para registrar tu pedido necesito tu cédula")

	if strings.Contains(strings.ToLower(salida), "necesito tu cedula") {
		t.Fatalf("la petición de cédula salió tal cual a un cliente que no autorizó nada: %q", salida)
	}
	// Lo que debe salir es la pregunta por el permiso, con los enlaces a las políticas.
	if !strings.Contains(strings.ToLower(salida), "proteccion de datos") &&
		!strings.Contains(strings.ToLower(salida), "protección de datos") {
		t.Errorf("no se le explicó por qué se le pide el permiso: %q", salida)
	}
	if !strings.Contains(salida, urlPrivacidad) {
		t.Errorf("no se le dieron las políticas para que pueda decidir informado: %q", salida)
	}
}

// Quien YA aceptó no vuelve a ver el menú: su petición de cédula sale normal. Es lo que se pidió
// expresamente — "verificando si el cliente no aceptó ya la protección de datos para no volver a
// preguntar".
func TestAQuienYaAceptoNoSeLeVuelveAPreguntar(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	const peticion = "Para registrar tu pedido necesito tu cédula"
	if salida := ag.revisarPeticionDeCedula(&turno{}, from, peticion); salida != peticion {
		t.Errorf("se le volvió a preguntar el permiso a quien ya lo dio: %q", salida)
	}
}

// Y a quien se NEGÓ tampoco se le vuelve a pedir la cédula, aunque el modelo insista.
func TestAQuienSeNegoNoSeLePideLaCedulaOtraVez(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})

	salida := ag.revisarPeticionDeCedula(&turno{}, from, "Necesito tu cédula para continuar")

	if strings.Contains(strings.ToLower(salida), "necesito tu cedula") {
		t.Errorf("se le pidió la cédula a quien negó el permiso: %q", salida)
	}
	if !strings.Contains(strings.ToLower(salida), "autorizacion") &&
		!strings.Contains(strings.ToLower(salida), "autorización") {
		t.Errorf("no se le explicó por qué no se puede continuar: %q", salida)
	}
}

// --- 3. El interceptor: el sí y el no se resuelven en código ---

func TestElSiDelClienteQuedaRegistradoYReciennSeLePideLaCedula(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimientoPendiente(from)

	respuesta, manejado := ag.ResponderConsentimiento(from, BotonAceptoDatos)
	if !manejado {
		t.Fatal("el botón 'Sí, acepto' no se resolvió en código: lo interpretaría el modelo")
	}
	c, hay := store.GetConsentimiento(from)
	if !hay || !c.Acepta {
		t.Fatal("la aceptación no quedó registrada: se le volvería a preguntar")
	}
	if c.Fecha.IsZero() {
		t.Error("la aceptación quedó sin fecha, y la fecha es parte del registro legal")
	}
	if store.ConsentimientoPendiente(from) {
		t.Error("la espera del menú sigue en pie tras haber respondido")
	}
	// Y AHORA sí se le pide la cédula: es el orden que se pidió.
	if !strings.Contains(strings.ToLower(respuesta), "cedula") &&
		!strings.Contains(strings.ToLower(respuesta), "cédula") {
		t.Errorf("tras aceptar no se le pidió la cédula, el flujo se queda colgado: %q", respuesta)
	}
}

func TestElNoDelClienteDetieneElFlujo(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimientoPendiente(from)

	respuesta, manejado := ag.ResponderConsentimiento(from, BotonNoAceptoDatos)
	if !manejado {
		t.Fatal("el botón 'No acepto' no se resolvió en código")
	}
	c, hay := store.GetConsentimiento(from)
	if !hay {
		t.Fatal("la negativa no quedó registrada: se le volvería a preguntar lo que ya respondió")
	}
	if c.Acepta {
		t.Fatalf("se registró como ACEPTADO a un cliente que dijo que NO. Es el peor error posible "+
			"de este flujo: trataríamos los datos de alguien que los negó. Respuesta: %q", respuesta)
	}
	// No se le pide la cédula ni se le insiste.
	if strings.Contains(strings.ToLower(respuesta), "tu cedula") ||
		strings.Contains(strings.ToLower(respuesta), "tu cédula") {
		t.Errorf("se le pidió la cédula a quien acababa de negar el permiso: %q", respuesta)
	}
}

// "sí" y "no" ESCRITOS valen igual que el botón: se pidió explícitamente ("solo si presiona sí,
// o escribe sí"). Mucha gente responde escribiendo aunque tenga el botón delante.
func TestElSiYElNoEscritosValenIgualQueElBoton(t *testing.T) {
	casos := []struct {
		texto  string
		acepta bool
	}{
		{"si", true}, {"Sí, acepto", true}, {"si acepto", true}, {"dale", true},
		{"no", false}, {"No acepto", false}, {"no gracias", false},
	}
	for _, c := range casos {
		ag, store, from := agenteConsentimiento(t)
		store.SetConsentimientoPendiente(from)

		if _, manejado := ag.ResponderConsentimiento(from, c.texto); !manejado {
			t.Errorf("%q no se resolvió en código", c.texto)
			continue
		}
		guardado, hay := store.GetConsentimiento(from)
		if !hay {
			t.Errorf("%q no dejó registro", c.texto)
			continue
		}
		if guardado.Acepta != c.acepta {
			t.Errorf("%q se registró como acepta=%v, se esperaba %v", c.texto, guardado.Acepta, c.acepta)
		}
	}
}

// Si el cliente PREGUNTA en vez de responder, lo atiende el modelo: ahí hay una duda legítima
// ("¿para qué la necesitan?") y forzarle un sí/no sería maltratarlo. La espera sigue en pie.
func TestUnaPreguntaSobreLasPoliticasLaAtiendeElModelo(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimientoPendiente(from)

	if _, manejado := ag.ResponderConsentimiento(from, "¿y para qué necesitan mi cédula?"); manejado {
		t.Error("se tomó el turno de una PREGUNTA: el cliente merece una respuesta, no un sí/no forzado")
	}
	if _, hay := store.GetConsentimiento(from); hay {
		t.Error("una pregunta quedó registrada como respuesta al consentimiento")
	}
	if !store.ConsentimientoPendiente(from) {
		t.Error("se perdió la espera del menú: su sí/no posterior ya no se resolvería en código")
	}
}

// Sin menú pendiente el interceptor no toca nada: un "no" en medio de una conversación normal
// ("no, mejor dos cilindros") no puede registrarse como una negativa de consentimiento.
func TestSinMenuPendienteNoSeInterpretaNingunSiNiNo(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)

	if _, manejado := ag.ResponderConsentimiento(from, "no"); manejado {
		t.Error("se interpretó un 'no' cualquiera como negativa de consentimiento")
	}
	if _, hay := store.GetConsentimiento(from); hay {
		t.Error("se registró un consentimiento que el cliente nunca respondió")
	}
}

// --- 4. La compuerta: la garantía de verdad ---

// backendQueRegistraTodo levanta un backend falso que APUNTA cada llamada que recibe. Así el test
// no comprueba el texto de la respuesta (que es para el modelo) sino lo que de verdad importa:
// si los datos del cliente salieron o no del bot.
type backendEspia struct {
	mu        sync.Mutex
	llamadas  []string
	cedulas   []string
	servidor  *httptest.Server
	aceptoPDP int
}

func nuevoBackendEspia(t *testing.T) *backendEspia {
	t.Helper()
	esp := &backendEspia{}
	esp.servidor = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		esp.mu.Lock()
		esp.llamadas = append(esp.llamadas, r.URL.Path)
		if strings.Contains(r.URL.Path, "acceptsPDP") {
			esp.aceptoPDP++
		}
		if ced := r.URL.Query().Get("identificacion"); ced != "" {
			esp.cedulas = append(esp.cedulas, ced)
		}
		esp.mu.Unlock()
		w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"existe":false}}`))
	}))
	t.Cleanup(esp.servidor.Close)
	return esp
}

func (e *backendEspia) llamo(fragmento string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.llamadas {
		if strings.Contains(l, fragmento) {
			return true
		}
	}
	return false
}

// LA PRUEBA QUE IMPORTA: con una negativa registrada, la cédula NO sale del bot.
func TestConNegativaLaCedulaNoLlegaAlBackend(t *testing.T) {
	const from = "593999900010"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})

	ag.verificarCliente(from, map[string]any{"identificacion": "0105566777"})

	if esp.llamo("clientExists") {
		t.Error("la cédula de un cliente que NEGÓ el permiso se consultó en el backend")
	}
}

// Y tampoco se registra su pedido ni se guarda su perfil.
func TestConNegativaNoSeRegistraElPedidoNiSeGuardaElPerfil(t *testing.T) {
	const from = "593999900011"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})
	store.SetLocation(from, -2.9, -79.0)

	// Los argumentos tienen que ser los REALES de la herramienta ("nombres_completos", no
	// "nombres"): con un nombre mal puesto el pedido muere antes por "faltan datos" y el test
	// pasaría sin haber ejercido la compuerta. Lo destapó el mutante de compuerta abierta, que
	// al principio sobrevivió a este test.
	salida := ag.registrarPedido(&turno{}, from, map[string]any{
		"identificacion":    "0105566777",
		"nombres_completos": "David Espinoza",
		"color":             "BLANCO",
		"cantidad":          2,
	})
	// Se comprueba que se detuvo POR EL CONSENTIMIENTO y no por otra validación cualquiera.
	if !strings.Contains(strings.ToLower(salida), "autoriz") {
		t.Fatalf("el pedido no se detuvo por falta de autorización, sino por otra cosa: %q. El test "+
			"no estaría probando la compuerta", salida)
	}

	if esp.llamo("startOrder") || esp.llamo("wppGetOrCreateClient") {
		t.Error("se registró el pedido de un cliente que negó el tratamiento de sus datos")
	}
	if p, hay := store.GetProfile(from); hay && p.Identificacion != "" {
		t.Errorf("se guardó el perfil de quien negó el permiso: %+v", p)
	}
}

// Con la aceptación dada, el consentimiento se consolida en el backend por CÉDULA: es el puente
// entre los dos momentos (se acepta por teléfono, se registra por cédula).
func TestAlLlegarLaCedulaLaAceptacionSeRegistraEnElBackend(t *testing.T) {
	const from = "593999900012"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	ag.verificarCliente(from, map[string]any{"identificacion": "0105566777"})

	if !esp.llamo("acceptsPDP") {
		t.Fatal("la aceptación no se registró en el backend: el consentimiento se quedaría solo en " +
			"el bot, atado a un teléfono, que no identifica a una persona")
	}
	c, _ := store.GetConsentimiento(from)
	if !c.Sincronizado {
		t.Error("no se marcó como sincronizado: se reenviaría en cada pedido")
	}
}

// Y no se reenvía una vez sincronizado.
func TestLaAceptacionNoSeReenviaDosVeces(t *testing.T) {
	const from = "593999900013"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: true})

	ag.sincronizarConsentimiento(from, "0105566777")
	ag.sincronizarConsentimiento(from, "0105566777")

	esp.mu.Lock()
	veces := esp.aceptoPDP
	esp.mu.Unlock()
	if veces != 1 {
		t.Errorf("se llamó %d veces a acceptsPDP; debía ser 1", veces)
	}
}

// Una NEGATIVA nunca se manda al backend: quien se niega no da su cédula, y guardar los datos de
// quien negó el permiso para tratarlos sería justo lo contrario de lo que pidió.
func TestLaNegativaNuncaSeMandaAlBackend(t *testing.T) {
	const from = "593999900014"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetConsentimiento(from, conversation.Consentimiento{Acepta: false})

	ag.sincronizarConsentimiento(from, "0105566777")

	if esp.llamo("acceptsPDP") {
		t.Error("se mandó al backend la cédula de alguien que NEGÓ el permiso")
	}
}

// --- 5. El consentimiento sobrevive al cierre de conversación ---

// Al negar, la conversación se cierra (arranca limpia la próxima vez) pero el consentimiento NO
// se borra: si se fuera con la limpieza, se le volvería a preguntar lo que ya respondió, que es
// exactamente lo que se pidió evitar.
func TestElConsentimientoSobreviveAlCierreDeCiclo(t *testing.T) {
	ag, store, from := agenteConsentimiento(t)
	store.SetConsentimientoPendiente(from)
	store.AppendUser(from, "quiero 2 blancos")

	if _, manejado := ag.ResponderConsentimiento(from, BotonNoAceptoDatos); !manejado {
		t.Fatal("la negativa no se resolvió en código")
	}
	// La conversación arranca limpia...
	if h := store.History(from); len(h) > 2 {
		t.Errorf("el historial no se limpió al cerrar el ciclo (%d turnos)", len(h))
	}
	// ...pero la respuesta al consentimiento sigue ahí.
	c, hay := store.GetConsentimiento(from)
	if !hay {
		t.Fatal("el cierre de ciclo se llevó el consentimiento: se le volvería a preguntar")
	}
	if c.Acepta {
		t.Error("la negativa se convirtió en aceptación al cerrar el ciclo")
	}
}

// --- 6. Las piezas están CABLEADAS, no solo escritas ---
//
// Un test que llama a la función directamente pasa igual aunque nadie la invoque en el flujo
// real. Ya pasó dos veces en este proyecto (candado de cobertura, de ubicación redundante), así
// que se comprueba sobre el archivo.

func TestElCandadoDeLaCedulaEstaCableado(t *testing.T) {
	if !archivoContiene(t, "agent.go", "a.revisarPeticionDeCedula(t, from, reply)") {
		t.Error("el candado no está cableado en HandleMessage: el modelo pediría la cédula sin " +
			"que nadie lo intercepte, y el cliente la daría sin haber autorizado nada")
	}
}

func TestElInterceptorDeConsentimientoEstaCableado(t *testing.T) {
	if !archivoContiene(t, "../../cmd/bot/main.go", "ag.ResponderConsentimiento(inc.From, inc.Text)") {
		t.Error("el interceptor no está cableado en el webhook: el sí/no del cliente lo " +
			"interpretaría el modelo, y de eso depende que un dato personal entre o no a la base")
	}
}

func TestLasCompuertasEstanCableadasEnLasTresHerramientas(t *testing.T) {
	// Las tres herramientas que tratan la cédula del cliente. Si a alguna le falta la compuerta,
	// hay un camino por el que los datos de quien negó el permiso entran igual.
	for _, archivo := range []string{"agent.go", "pedido.go", "programado.go"} {
		if !archivoContiene(t, archivo, "a.consentimientoNiega(from)") {
			t.Errorf("%s no comprueba el consentimiento: es un camino abierto para tratar los "+
				"datos de un cliente que los negó", archivo)
		}
	}
}
