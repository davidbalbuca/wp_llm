package texto

import "testing"

// AfirmaSecuencia es el mecanismo único de detección de los candados del bot. Debe: calzar
// palabras en orden con huecos, rechazar el orden inverso, y cortar ante una negación. Si esto
// falla, fallan los cuatro candados (cancelar, programar, registrar, avisar) a la vez.
func TestAfirmaSecuencia(t *testing.T) {
	sec := [][]string{{"pedido", "cancelado"}}

	calza := []string{
		"pedido cancelado",
		"tu pedido ha sido cancelado",
		"tu pedido ya quedo cancelado por completo",
	}
	for _, txt := range calza {
		if !AfirmaSecuencia(txt, sec, 3) {
			t.Errorf("debía calzar (palabras en orden con hueco): %q", txt)
		}
	}

	noCalza := []string{
		"cancelado el pedido",                      // orden inverso
		"pedido ... y luego mucho texto cancelado", // hueco > 3 palabras
		"pedido no cancelado",                      // negación en el hueco
		"el pedido aun no ha sido cancelado",       // negación 'aun' + 'no'
		"quieres cancelar el pedido",               // no está la secuencia
	}
	for _, txt := range noCalza {
		if AfirmaSecuencia(txt, sec, 3) {
			t.Errorf("NO debía calzar: %q", txt)
		}
	}
}

// La negación puede ir ANTES del inicio de la secuencia: "no he cancelado tu pedido" calza
// {"cancelado","pedido"} pero el "no" está antes del verbo.
func TestNegacionAntesDeLaSecuencia(t *testing.T) {
	sec := [][]string{{"cancelado", "pedido"}}
	if AfirmaSecuencia("no he cancelado tu pedido", sec, 3) {
		t.Error("una negación previa al verbo debe invalidar la afirmación")
	}
	if !AfirmaSecuencia("he cancelado tu pedido", sec, 3) {
		t.Error("sin negación sí debe calzar")
	}
}

// La holgura es configurable: con holgura 0 solo calza si las palabras están pegadas.
func TestAfirmaSecuenciaHolguraCero(t *testing.T) {
	sec := [][]string{{"pedido", "cancelado"}}
	if !AfirmaSecuencia("pedido cancelado", sec, 0) {
		t.Error("con holgura 0 debe calzar el par contiguo")
	}
	if AfirmaSecuencia("pedido ya cancelado", sec, 0) {
		t.Error("con holgura 0 NO debe calzar si hay una palabra en medio")
	}
}

// Normalizar deja el texto comparable: sin tildes, minúsculas, signos como espacios.
func TestNormalizar(t *testing.T) {
	casos := map[string]string{
		"¿Cuántos conductores?": "cuantos conductores",
		// La ñ se conserva: en español es una letra propia, no una n con tilde.
		"  ÉL   Ñoño  ": "el ñoño",
		"Sí, gracias!":  "si gracias",
	}
	for entrada, esperado := range casos {
		if got := Normalizar(entrada); got != esperado {
			t.Errorf("Normalizar(%q) = %q, se esperaba %q", entrada, got, esperado)
		}
	}
}
