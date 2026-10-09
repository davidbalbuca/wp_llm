package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Qué cuenta como apertura simple (saludo o "quiero gas") y qué no: todo lo demás es del modelo.
func TestEsAperturaSimple(t *testing.T) {
	simples := []string{"Hola", "Deseo pedir GAS 😄", "buenas noches", "Hola, quiero un gas por favor",
		"necesito un cilindro", "Buenos días!!", "gas"}
	for _, s := range simples {
		if !esAperturaSimple(s) {
			t.Errorf("%q debería ser apertura simple", s)
		}
	}
	noSimples := []string{"quiero 2 amarillos", "cuánto cuesta?", "llegan a Ricaurte?", "hola quiero 1",
		"https://maps.app.goo.gl/x", "hola, tienen naranja?", "😄", "", "quiero cancelar mi pedido"}
	for _, s := range noSimples {
		if esAperturaSimple(s) {
			t.Errorf("%q NO debería interceptarse: trae algo que le toca al modelo", s)
		}
	}
}

// "Hola" de un cliente nuevo: UN mensaje con la presentación y el menú de colores debajo.
func TestAperturaMandaPresentacionYColoresEnUnSoloMensaje(t *testing.T) {
	const from = "593900700001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()
	var cuerpos []string
	var opcionesEnviadas []string
	ag.enviarMenu = func(_ string, cuerpo string, opciones []string) error {
		cuerpos = append(cuerpos, cuerpo)
		opcionesEnviadas = opciones
		return nil
	}
	ag.DejarBienvenidaPendiente(from, textoBienvenida("Adrián"))

	_, manejado := ag.ResponderAperturaConColores(from, "Hola")

	if !manejado {
		t.Fatal("un \"Hola\" al abrir la conversación no salió con el menú de colores")
	}
	if len(cuerpos) != 1 {
		t.Fatalf("salieron %d mensajes, se esperaba uno solo", len(cuerpos))
	}
	if !strings.Contains(cuerpos[0], marcaDePresentacion) || !strings.HasSuffix(cuerpos[0], cuerpoMenuColores) {
		t.Errorf("el mensaje no trae la presentación arriba y la pregunta del color abajo: %q", cuerpos[0])
	}
	if len(opcionesEnviadas) < 2 {
		t.Errorf("menú sin colores: %v", opcionesEnviadas)
	}
	if _, pendiente := ag.HayBienvenidaPendiente(from); pendiente {
		t.Error("la presentación sigue pendiente: saldría además suelta")
	}
	// El modelo tiene que saber qué se preguntó, para entender el "Blanco" que viene.
	hist := store.History(from)
	if len(hist) == 0 || !strings.Contains(hist[len(hist)-1].Parts[0].Text, cuerpoMenuColores) {
		t.Error("el menú no quedó en el historial del modelo")
	}
}

// Con algo más que un saludo, o sin ser el arranque, el turno es del modelo.
func TestAperturaNoInterceptaLoQueNoEsSaludo(t *testing.T) {
	const from = "593900700002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.catalog = catalogoDePrueba()
	ag.enviarMenu = func(string, string, []string) error { t.Error("no debía mandarse menú"); return nil }

	if _, manejado := ag.ResponderAperturaConColores(from, "Hola"); manejado {
		t.Error("sin presentación pendiente (conversación ya empezada) no se intercepta")
	}
	ag.DejarBienvenidaPendiente(from, textoBienvenida(""))
	if _, manejado := ag.ResponderAperturaConColores(from, "cuánto cuesta el gas?"); manejado {
		t.Error("una pregunta se interceptó: le toca al modelo")
	}
}

// Precio y cobertura en el arranque, sacados del catálogo; sin catálogo, nada.
func TestDatosDeArranque(t *testing.T) {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", PrecioUnitario: 1.65,
			CostoEnvio: 1, CostoInstalacion: 0.45, CostoServicio: 0.15}},
		Zonas: []georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: []string{"MONAY", "TURI"}}},
	})
	// 09/10: debajo del saludo ya no van el precio ni las parroquias (no se leían): solo la ciudad.
	if got := ag.enTodaLaCiudad(); got != " en todo Cuenca" {
		t.Errorf("la ciudad sale del catálogo: %q", got)
	}
	if got := (&Agent{}).enTodaLaCiudad(); got != "" {
		t.Errorf("sin catálogo no se dice nada: %q", got)
	}
	saludo, _ := ag.SaludoDeBienvenida("593999300001")
	for _, no := range []string{"$3.25", "parroquias", "Estamos a la vuelta"} {
		if strings.Contains(saludo, no) {
			t.Errorf("el saludo volvió a crecer (%q): %q", no, saludo)
		}
	}
	if !strings.Contains(saludo, "en todo Cuenca") || !strings.Contains(saludo, LinkApp) {
		t.Errorf("el saludo debía decir la ciudad e invitar a la app: %q", saludo)
	}
	if got := ZonasEnTexto([]georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: []string{"MONAY"}}}); got != "CUENCA (MONAY)" {
		t.Errorf("ZonasEnTexto = %q", got)
	}
}
