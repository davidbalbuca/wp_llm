package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/georoutes"
)

func paresDeColor(pares ...[2]string) georoutes.Equivalencias {
	var eq georoutes.Equivalencias
	for _, p := range pares {
		eq.Pares = append(eq.Pares, struct {
			ColorA string `json:"color_a"`
			ColorB string `json:"color_b"`
		}{p[0], p[1]})
	}
	return eq
}

// CASO 04/10: "tengo amarillo pero quiero cambiarlo por blanco, ¿se puede?". La IA tiene que ver
// la tabla del panel para contestar sin adivinar.
func TestElPromptLlevaLosCambiosDeColor(t *testing.T) {
	texto := renderCambiosDeColor(paresDeColor([2]string{"BLANCO", "AMARILLO"}, [2]string{"NARANJA", "AZUL"}))
	for _, esperado := range []string{"AMARILLO ↔ BLANCO", "AZUL ↔ NARANJA", "mismo precio", "Nunca prometas", "NO ofrezcas vender"} {
		if !strings.Contains(texto, esperado) {
			t.Errorf("falta %q en:\n%s", esperado, texto)
		}
	}
}

// El orden de los pares no puede depender de cómo los mande el backend: el texto va en la parte
// cacheada del prompt, y un orden distinto la invalidaría en cada llamada.
func TestCambiosDeColorEnOrdenEstable(t *testing.T) {
	a := renderCambiosDeColor(paresDeColor([2]string{"BLANCO", "AMARILLO"}, [2]string{"NARANJA", "AZUL"}))
	b := renderCambiosDeColor(paresDeColor([2]string{"AZUL", "NARANJA"}, [2]string{"AMARILLO", "BLANCO"}, [2]string{"BLANCO", "AMARILLO"}))
	if a != b {
		t.Errorf("el texto cambia según el orden del backend:\n%s\n---\n%s", a, b)
	}
}

// Sin datos no se afirma ni se niega nada.
func TestCambiosDeColorSinDatos(t *testing.T) {
	if texto := renderCambiosDeColor(georoutes.Equivalencias{}); !strings.Contains(texto, "NO lo afirmes ni lo niegues") {
		t.Errorf("sin tabla, el prompt debía pedir no afirmar nada: %s", texto)
	}
}
