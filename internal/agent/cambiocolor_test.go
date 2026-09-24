package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// CAMBIO DE COLOR: el bot no puede prometer un intercambio de cilindros que el negocio no hace.
//
// INCIDENTE 24/09 (593980787206): el cliente tenía dos cilindros AZULES y preguntó dos veces si
// se los cambiaban por BLANCOS. El bot dijo "¡Claro que sí! Podemos cambiar esos dos azules por
// dos blancos sin problema 😊" y luego "¡Sí, claro! 😊 No hay problema, pides el color que tú
// quieras sin importar cuál tengas ahora". En la tabla del panel solo están BLANCO<->AMARILLO y
// NARANJA<->AZUL: azul->blanco no se hace. Un operador tuvo que entrar al chat a explicarlo
// ("los repartidores tienen la equivalencia de Blancos por Amarillos") y llamar a los
// repartidores a mano.

// catalogoConEquivalencias reproduce las equivalencias REALES de producción (24-sep):
// BLANCO<->AMARILLO y NARANJA<->AZUL. Los cuatro colores existen; lo que no existe es el cambio
// entre cualquier par.
func catalogoConEquivalencias() *catalog.Client {
	return catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 1, Nombre: "BLANCO"}, {ID: 2, Nombre: "AMARILLO"},
			{ID: 3, Nombre: "NARANJA"}, {ID: 4, Nombre: "AZUL"},
		}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
		Equivalencias: georoutes.Equivalencias{PorColor: map[string][]string{
			"BLANCO":   {"AMARILLO"},
			"AMARILLO": {"BLANCO"},
			"NARANJA":  {"AZUL"},
			"AZUL":     {"NARANJA"},
		}},
	})
}

// EL CASO REAL, palabra por palabra.
func TestIncidente_NoSePrometeUnCambioDeColorQueNoExiste(t *testing.T) {
	const from = "593980787206"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"¡Sí, claro! 😊 No hay problema, pides el color que tú quieras sin importar cuál tengas " +
			"ahora. Te entregamos 2 cilindros Blanco nuevos y ya está.\n\n¿Me compartes tu " +
			"ubicación por WhatsApp 📎 para coordinar la entrega?",
	}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	res, err := ag.HandleMessage(context.Background(), from,
		"Por favor necesito cambiar dos cilindros azules por blancos, cuento es el total?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	bajo := strings.ToLower(res.Texto)
	// Lo que NO puede seguir saliendo.
	if strings.Contains(bajo, "sin importar cual tengas") || strings.Contains(bajo, "sin importar cuál tengas") {
		t.Fatalf("el bot volvió a prometer cualquier cambio de color: %q", res.Texto)
	}
	// Tiene que decir que ese cambio no se hace.
	if !strings.Contains(bajo, "no es uno de los que hacemos") {
		t.Errorf("no se le dijo que ese cambio no se hace: %q", res.Texto)
	}
	// Y ofrecerle el que SÍ existe para sus azules (naranja), en vez de dejarlo sin salida.
	if !strings.Contains(bajo, "naranja") {
		t.Errorf("no se le ofreció la equivalencia real de sus azules: %q", res.Texto)
	}
	// Y avisar de que lo revisa un operador — decisión del dueño (24-sep).
	if !strings.Contains(bajo, "operador") {
		t.Errorf("no se le avisó que un operador lo revisa: %q", res.Texto)
	}
}

// La promesa "un operador lo revisa" tiene que ser VERDAD: el candado crea el ticket. Si solo
// reemplazara el texto, sería cambiar una mentira por otra.
func TestElCambioNoConfiguradoDerivaDeVerdad(t *testing.T) {
	const from = "593980787207"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"¡Claro que sí! Podemos cambiar esos dos azules por dos blancos sin problema 😊",
	}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	if _, err := ag.HandleMessage(context.Background(), from,
		"tengo cilindros azules pero necesito cambiarlos por los blancos si hay como?"); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if len(store.ListTickets(conversation.TicketAbierto, 10)) == 0 {
		t.Fatal("no se creó ticket: 'un operador lo revisa' quedaría en otra promesa vacía")
	}
}

// EL CAMBIO QUE SÍ ESTÁ CONFIGURADO PASA INTACTO. Es la mitad que un candado mal hecho rompe:
// si tapara todas las promesas, el bot dejaría de poder decir la verdad y el cliente acabaría
// esperando a un operador para algo que se resuelve solo.
func TestElCambioConfiguradoSeRespeta(t *testing.T) {
	const from = "593980787208"
	store := conversation.NewMemStore()
	original := "¡Claro que sí! Podemos cambiar tus cilindros blancos por amarillos sin problema 😊"
	fake := &modeloQueDice{respuestas: []string{original}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	res, err := ag.HandleMessage(context.Background(), from,
		"me cambian mis cilindros blancos por amarillos?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto != original {
		t.Errorf("se tapó un cambio que SÍ está configurado (blanco<->amarillo):\n  salió:    %q\n  esperado: %q",
			res.Texto, original)
	}
}

// La tabla se guarda normalizada (id menor en color_a), así que el candado tiene que funcionar
// igual preguntando por el otro lado del par. Sin la simetría, "amarillo por blanco" se
// rechazaría y "blanco por amarillo" no: el mismo trato con dos respuestas distintas.
func TestElCambioConfiguradoFuncionaEnLosDosSentidos(t *testing.T) {
	const from = "593980787209"
	store := conversation.NewMemStore()
	original := "Sí, te los cambiamos: tus amarillos por blancos, sin problema 😊"
	fake := &modeloQueDice{respuestas: []string{original}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	res, err := ag.HandleMessage(context.Background(), from,
		"puedo cambiar mis amarillos por blancos?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto != original {
		t.Errorf("el mismo cambio se rechazó al preguntarlo al revés: %q", res.Texto)
	}
}

// SIN DATOS NO SE AFIRMA. Si el backend no dio las equivalencias, el candado trata el cambio
// como no configurado y deriva. Errar hacia "que lo vea una persona" cuesta un ticket; errar
// hacia "sí, claro" cuesta lo del 24/09.
func TestSinEquivalenciasCargadasNoSePromete(t *testing.T) {
	const from = "593980787210"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"¡Claro! Te cambiamos los azules por blancos sin problema 😊",
	}}
	ag := agentIncidente(fake, store)
	// Catálogo con colores pero SIN equivalencias: es lo que queda si getColorEquivalences falla.
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 1, Nombre: "BLANCO"}, {ID: 4, Nombre: "AZUL"},
		}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})

	res, err := ag.HandleMessage(context.Background(), from, "me cambian los azules por blancos?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(strings.ToLower(res.Texto), "sin problema") {
		t.Errorf("sin datos del backend el bot igual prometió el cambio: %q", res.Texto)
	}
}

// Lo que el candado NO debe tocar: un pedido normal de un color. "Quiero 2 blancos" no es un
// intercambio, y taparlo rompería el flujo de todos los pedidos.
func TestUnPedidoNormalDeUnColorNoSeToca(t *testing.T) {
	const from = "593980787211"
	store := conversation.NewMemStore()
	original := "¡Perfecto! 2 cilindros BLANCO anotados 👍 Compárteme tu ubicación por WhatsApp 📎"
	fake := &modeloQueDice{respuestas: []string{original}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	res, err := ag.HandleMessage(context.Background(), from, "quiero 2 cilindros blancos")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto != original {
		t.Errorf("el candado se metió en un pedido normal: %q", res.Texto)
	}
}

// Ni una respuesta sobre disponibilidad. "Sí, tenemos blancos" habla de stock, no de cambiar
// envases: si el candado lo tapara, el bot no podría contestar la pregunta más común del negocio.
func TestDecirQueHayUnColorNoEsPrometerUnCambio(t *testing.T) {
	const from = "593980787212"
	store := conversation.NewMemStore()
	original := "¡Sí! Tenemos cilindros blancos disponibles 😊 ¿Cuántos necesitas?"
	fake := &modeloQueDice{respuestas: []string{original}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConEquivalencias()

	res, err := ag.HandleMessage(context.Background(), from, "cuentan con cilindros blancos?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if res.Texto != original {
		t.Errorf("se confundió disponibilidad con intercambio: %q", res.Texto)
	}
}

// --- unidad: las piezas del candado, sin turno completo ---

func TestColoresMencionadosRespetaElOrdenDeAparicion(t *testing.T) {
	ag := &Agent{catalog: catalogoConEquivalencias()}
	// El PRIMERO es el que el cliente tiene; el segundo, el que quiere. Si se invirtiera, el
	// candado consultaría el par al revés y con equivalencias asimétricas daría otra respuesta.
	got := ag.coloresMencionados("tengo dos azules y quiero cambiarlos por blancos")
	if len(got) != 2 || got[0] != "AZUL" || got[1] != "BLANCO" {
		t.Fatalf("orden de colores incorrecto: %v (esperado [AZUL BLANCO])", got)
	}
}

func TestColoresMencionadosTolerraPluralYGenero(t *testing.T) {
	ag := &Agent{catalog: catalogoConEquivalencias()}
	// Los clientes escriben "blancos", "azules", "blancas" — nunca el nombre exacto de la tabla.
	for _, texto := range []string{
		"cambio mis azules por blancos",
		"cambio mi azul por blanco",
		"las azules por las blancas",
	} {
		got := ag.coloresMencionados(texto)
		if len(got) != 2 || got[0] != "AZUL" || got[1] != "BLANCO" {
			t.Errorf("%q -> %v (esperado [AZUL BLANCO])", texto, got)
		}
	}
}

func TestCambioConfiguradoConsultaLaTabla(t *testing.T) {
	ag := &Agent{catalog: catalogoConEquivalencias()}
	casos := []struct {
		tiene, quiere string
		permitido     bool
	}{
		{"AZUL", "NARANJA", true},      // configurado
		{"NARANJA", "AZUL", true},      // y al revés
		{"BLANCO", "AMARILLO", true},   // el otro par
		{"AZUL", "BLANCO", false},      // EL CASO REAL: no está configurado
		{"AMARILLO", "NARANJA", false}, // cruzar los pares tampoco vale
	}
	for _, c := range casos {
		permitido, hayDatos := ag.cambioConfigurado(c.tiene, c.quiere)
		if !hayDatos {
			t.Fatalf("%s->%s: se reportó falta de datos con catálogo cargado", c.tiene, c.quiere)
		}
		if permitido != c.permitido {
			t.Errorf("%s->%s: permitido=%v (esperado %v)", c.tiene, c.quiere, permitido, c.permitido)
		}
	}
}

func TestPrometeElCambioNoConfundeNegacionesNiDisponibilidad(t *testing.T) {
	// Lo que SÍ es una promesa de cambio.
	for _, texto := range []string{
		"podemos cambiar esos dos azules por dos blancos sin problema",
		"sí, te los cambiamos",
		"pides el color que tú quieras sin importar cuál tengas ahora",
		"hacemos el cambio sin problema",
	} {
		if !prometeElCambio(texto) {
			t.Errorf("no se detectó la promesa: %q", texto)
		}
	}
	// Lo que NO lo es: negativas (ya corregidas) y disponibilidad.
	for _, texto := range []string{
		"ese cambio no lo hacemos, pero un operador lo revisa",
		"no podemos cambiar azules por blancos",
		"sí, tenemos cilindros blancos disponibles",
		"perfecto, 2 cilindros blancos anotados",
		"el cilindro de 15kg cuesta 3.25 cada uno",
	} {
		if prometeElCambio(texto) {
			t.Errorf("falso positivo: %q", texto)
		}
	}
}

func TestElMensajeNoDisponibleNoDejaAlClienteSinSalida(t *testing.T) {
	msg := mensajeCambioNoDisponible("AZUL", "BLANCO", []string{"NARANJA"})
	bajo := strings.ToLower(msg)
	for _, quiero := range []string{"azul", "blanco", "naranja", "operador"} {
		if !strings.Contains(bajo, quiero) {
			t.Errorf("el mensaje no menciona %q: %s", quiero, msg)
		}
	}
	// Sin equivalencias que ofrecer sigue siendo un mensaje válido (no una frase a medias).
	solo := mensajeCambioNoDisponible("AZUL", "BLANCO", nil)
	if !strings.Contains(strings.ToLower(solo), "operador") {
		t.Errorf("sin alternativas se pierde el aviso del operador: %s", solo)
	}
}
