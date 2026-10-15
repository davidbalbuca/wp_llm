// OTRA CIUDAD NO ES UN BARRIO (06/10): a "Loja?" el candado contestaba "¡Claro que sí!".
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

func TestLugarFueraMencionado(t *testing.T) {
	cuenca := []georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: []string{"BANOS", "EL VECINO", "SUCRE"}}}
	casos := map[string]string{
		"Loja?":                          "Loja",
		"Q es de Machala":                "Machala",
		"¿llegan a Gualaceo?":            "Gualaceo",
		"yo vivo en santa isabel":        "Santa Isabel",
		"estoy por el ex CREA":           "",
		"¿llegan a La Gloria?":           "",
		"¿llegan a Baños?":               "", // es parroquia de Cuenca
		"¿al barrio Sucre llegan?":       "",
		"Hola, necesito un gas amarillo": "",
	}
	for texto, want := range casos {
		if got := lugarFueraMencionado(texto, cuenca); got != want {
			t.Errorf("lugarFueraMencionado(%q) = %q; quería %q", texto, got, want)
		}
	}
	// Si el backend un día informa esa zona como cubierta, deja de contar como "fuera".
	conZamora := append(cuenca, georoutes.ZonaCobertura{Zona: "ZAMORA"})
	if got := lugarFueraMencionado("¿llegan a Zamora?", conZamora); got != "" {
		t.Errorf("Zamora está en la cobertura del backend: no es 'fuera' (got %q)", got)
	}
}

func TestCasoLojaLaNegativaDelModeloSeRespeta(t *testing.T) {
	const from = "593980260147"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalogoConZonas()
	store.LogMessage(from, "user", "Loja?")

	const negativa = "Uy 😔 por ahora no llegamos a Loja. Atendemos en Cuenca y sus parroquias."
	if got := ag.revisarNegativaDeCobertura(from, negativa); got != negativa {
		t.Errorf("Loja es otra ciudad: el 'no llegamos' es verdad y no se puede cambiar por un sí: %q", got)
	}
}

func TestSiElModeloPrometeOtraCiudadSeCorrige(t *testing.T) {
	const from = "593980260148"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalogoConZonas()
	store.LogMessage(from, "user", "¿llegan a Machala?")

	got := ag.revisarCoberturaAfirmada(from, "¡Sí llegamos a Machala! 😊 ¿De qué color es tu cilindro?")
	if !strings.HasPrefix(got, "Por ahora no llegamos a Machala 😔") {
		t.Errorf("prometió cobertura en otra ciudad: debía corregirse a un no amable: %q", got)
	}
}

func TestUnBarrioSigueSinRechazarse(t *testing.T) {
	const from = "593980260149"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	ag.catalog = catalogoConZonas()
	store.LogMessage(from, "user", "¿al barrio La Gloria llegan?")

	got := ag.revisarNegativaDeCobertura(from, "Lo siento, no llegamos a La Gloria.")
	if strings.Contains(got, "no llegamos") || strings.HasPrefix(got, "¡Claro") {
		t.Errorf("un barrio no se rechaza ni se promete: se pide la ubicación (got %q)", got)
	}
	if !strings.Contains(got, "ubicación") {
		t.Errorf("debía pedir la ubicación: %q", got)
	}
}
