package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Cobertura: el modelo NO decide si llegamos a un lugar. Solo la geocerca del backend, con
// coordenadas, puede decidirlo.
//
// INCIDENTE 11/09 (Israel, 593978730922): preguntó "¿Al barrio la gloria? Por el ex crea
// llegan?" y el bot respondió "Déjame revisar... La Gloria no está en nuestras zonas de
// cobertura 😔". Diez minutos después el cliente se despidió. Las dos frases eran falsas: no
// revisó nada (no hay herramienta para consultar un nombre) y La Gloria es un barrio de Cuenca,
// donde SÍ atendemos. El prompt le pedía buscar el lugar en una lista de PARROQUIAS; un barrio
// nunca está ahí, así que el error caía siempre del lado caro: rechazar a un cliente real.

// catalogoConZonas arma un catálogo con las zonas REALES de producción (AZUAY, 36 parroquias).
func catalogoConZonas() *catalog.Client {
	return catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG",
			Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
		Zonas: []georoutes.ZonaCobertura{{
			Zona:       "AZUAY",
			Parroquias: []string{"BANOS", "BELLAVISTA", "CAÑARIBAMBA", "SAN BLAS", "TOTORACOCHA"},
		}},
	})
}

// El turno completo: el modelo niega cobertura por el nombre del barrio y el candado lo
// reemplaza por la respuesta en positivo que pide la ubicación.
func TestIncidente_NoSeRechazaAlClientePorElNombreDeSuBarrio(t *testing.T) {
	const from = "593978730922"
	store := conversation.NewMemStore()
	fake := &modeloQueDice{respuestas: []string{
		"Déjame revisar si llegamos a La Gloria...\n\nLamentablemente, La Gloria no está en " +
			"nuestras zonas de cobertura por ahora 😔",
	}}
	ag := agentIncidente(fake, store)
	ag.catalog = catalogoConZonas()

	res, err := ag.HandleMessage(context.Background(), from, "Al barrio la gloria? Por el ex crea llegan?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(strings.ToLower(res.Texto), "no está en nuestras zonas") {
		t.Fatalf("se rechazó al cliente por el nombre de su barrio: %q", res.Texto)
	}
	// Tiene que pedirle la ubicación, que es lo único que decide de verdad.
	if !strings.Contains(strings.ToLower(res.Texto), "ubicación") {
		t.Errorf("no se le pidió la ubicación para confirmar: %q", res.Texto)
	}
	// Y nombrar la zona que SÍ atendemos, con el dato del backend.
	if !strings.Contains(res.Texto, "AZUAY") {
		t.Errorf("no se le dijo dónde sí atendemos: %q", res.Texto)
	}
}

// La zona sale del BACKEND, nunca quemada: si mañana se agrega otra provincia, el bot la
// nombra sola. Con dos zonas configuradas, el mensaje menciona las dos.
func TestLaZonaSaleDelBackendNoDelCodigo(t *testing.T) {
	msg := mensajeCoberturaEnPositivo([]georoutes.ZonaCobertura{
		{Zona: "AZUAY", Parroquias: []string{"BANOS", "SAN BLAS"}},
		{Zona: "LOJA", Parroquias: []string{"EL VALLE"}},
	})
	for _, quiero := range []string{"AZUAY", "LOJA", "ubicación"} {
		if !strings.Contains(msg, quiero) {
			t.Errorf("el mensaje no menciona %q: %q", quiero, msg)
		}
	}

	// Sin zonas del backend NO se inventa ninguna, pero igual se pide la ubicación.
	vacio := mensajeCoberturaEnPositivo(nil)
	if strings.Contains(vacio, "AZUAY") {
		t.Errorf("se inventó una zona sin datos del backend: %q", vacio)
	}
	if !strings.Contains(vacio, "ubicación") {
		t.Errorf("sin zonas igual hay que pedir la ubicación: %q", vacio)
	}
}

// El detector reconoce las formas en que el modelo redacta una negativa, y NO confunde una
// respuesta legítima ni una afirmación.
func TestDetectorDeNegativaDeCobertura(t *testing.T) {
	niegan := []string{
		"Lamentablemente, La Gloria no está en nuestras zonas de cobertura por ahora 😔",
		"Uy, a esa zona no llegamos todavía",
		"Ese sector no está dentro de nuestra cobertura",
		"No tenemos cobertura en ese barrio",
		"Por ahí aún no llegamos",
	}
	for _, texto := range niegan {
		if !niegaCobertura(texto) {
			t.Errorf("no se detectó la negativa: %q", texto)
		}
	}

	noNiegan := []string{
		"¡Claro! Atendemos en todo Azuay 😊 Compárteme tu ubicación 📎",
		"¡Sí llegamos! ¿Me compartes tu ubicación?",
		"¿Qué cilindro necesitas?",
		"Atendemos en Azuay: Baños, Bellavista y más.",
	}
	for _, texto := range noNiegan {
		if niegaCobertura(texto) {
			t.Errorf("se tomó por negativa un texto que no lo es: %q", texto)
		}
	}
}

// LA NEGATIVA LEGÍTIMA SÍ PASA. Si al cliente ya se le verificó la ubicación por coordenadas y
// cayó fuera de zona, puede repetírselo cuando insista ("¿de verdad no llegan?"). Taparla lo
// devolvería al bucle de pedirle el pin para darle la misma respuesta (caso Ambato, 08/09).
func TestLaNegativaVerificadaPorCoordenadasSeRespeta(t *testing.T) {
	const from = "593999500001"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalogoConZonas()

	const negativa = "Uy 😔 ya revisé tu ubicación y por ahora no llegamos a esa zona."

	// Sin verificación previa: se tapa (es una deducción por el nombre).
	if got := ag.revisarNegativaDeCobertura(from, negativa); strings.Contains(got, "no llegamos") {
		t.Errorf("una negativa SIN verificar debía taparse: %q", got)
	}

	// Con la ubicación ya verificada fuera de zona: la negativa es verdad y se respeta.
	store.MarcarFueraDeCobertura(from)
	if got := ag.revisarNegativaDeCobertura(from, negativa); got != negativa {
		t.Errorf("se pisó una negativa verificada por coordenadas: %q", got)
	}

	// Si comparte una ubicación NUEVA, el rechazo anterior ya no aplica (pudo moverse).
	store.LimpiarFueraDeCobertura(from)
	if got := ag.revisarNegativaDeCobertura(from, negativa); strings.Contains(got, "no llegamos") {
		t.Errorf("tras una ubicación nueva la negativa vieja no puede seguir valiendo: %q", got)
	}
}

// La marca de fuera-de-cobertura funciona igual en los dos backends del store.
func TestMarcaFueraDeCoberturaEnLosDosBackends(t *testing.T) {
	for nombre, abrir := range storesDePrueba(t) {
		t.Run(nombre, func(t *testing.T) {
			const from = "593999500002"
			store := abrir()
			if store.FueraDeCoberturaVerificado(from) {
				t.Fatal("un cliente nuevo no puede nacer marcado fuera de cobertura")
			}
			store.MarcarFueraDeCobertura(from)
			if !store.FueraDeCoberturaVerificado(from) {
				t.Error("no se guardó el rechazo verificado")
			}
			store.LimpiarFueraDeCobertura(from)
			if store.FueraDeCoberturaVerificado(from) {
				t.Error("el rechazo no se limpió con la ubicación nueva")
			}
		})
	}
}

// EL PROMPT ya no le entrega la lista completa de parroquias (era lo que lo invitaba a "buscar"
// el lugar del cliente), pero sí la zona y la regla de no decidir él.
func TestElPromptNoInvitaABuscarElLugarEnUnaLista(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{
		Zona:       "AZUAY",
		Parroquias: []string{"BANOS", "BELLAVISTA", "CAÑARIBAMBA", "SAN BLAS", "TOTORACOCHA", "ZAMORA"},
	}}
	texto := renderCobertura(zonas)

	if !strings.Contains(texto, "AZUAY") {
		t.Errorf("el bloque no nombra la zona del backend:\n%s", texto)
	}
	if !strings.Contains(texto, "NUNCA respondas que NO llegamos") {
		t.Errorf("falta la regla que le impide rechazar por nombre:\n%s", texto)
	}
	// La lista COMPLETA ya no va: era la que lo hacía deducir "no está => no hay cobertura".
	if strings.Contains(texto, "Lista COMPLETA") {
		t.Errorf("el prompt sigue dándole la lista para buscar el lugar:\n%s", texto)
	}
	// Una parroquia del final de la lista no debe aparecer (solo van 3 de ejemplo).
	if strings.Contains(texto, "ZAMORA") {
		t.Errorf("se sigue enviando la lista entera de parroquias:\n%s", texto)
	}
}
