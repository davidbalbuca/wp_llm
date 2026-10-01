// EL TIEMPO DE ENTREGA QUE VE EL CLIENTE = TIEMPO DE RUTA DEL BACKEND + MARGEN DE PREPARACIÓN.
//
// El backend ya calcula el tiempo de ruta real (OSRM, conductor -> cliente) y lo devuelve como
// "HH:MM:SS" en la respuesta de wppOrder, pero el bot lo ignoraba (OrderResult no lo capturaba). Se
// le suma un margen (5 min por defecto, BOT_MARGEN_ENTREGA_MIN) porque el conductor no sale en el
// instante cero: carga el cilindro y arranca.
package georoutes

import "testing"

func TestMinutosEntregaConMargen(t *testing.T) {
	casos := []struct {
		tiempo    string
		margen    int
		wantMin   int
		wantHayOK bool
	}{
		{"00:15:00", 5, 20, true},  // 15 min de ruta + 5 = 20
		{"00:15:30", 5, 21, true},  // 15:30 -> los 30s cuentan como 1 min más (16) + 5 = 21
		{"00:00:00", 5, 0, false},  // sin conductor con posición: no se promete tiempo
		{"", 5, 0, false},          // el backend no envió tiempo (compat)
		{"01:05:00", 5, 70, true},  // más de una hora: 65 + 5
		{"basura", 5, 0, false},    // ilegible: no se promete tiempo
		{"00:03:00", 0, 3, true},   // margen 0 configurable: solo la ruta
		{"00:20:00", 10, 30, true}, // margen configurable a 10
	}
	for _, c := range casos {
		r := &OrderResult{Tiempo: c.tiempo}
		got, hay := r.MinutosEntregaConMargen(c.margen)
		if hay != c.wantHayOK {
			t.Errorf("tiempo %q margen %d: hay=%v, esperado %v", c.tiempo, c.margen, hay, c.wantHayOK)
			continue
		}
		if got != c.wantMin {
			t.Errorf("tiempo %q margen %d: min=%d, esperado %d", c.tiempo, c.margen, got, c.wantMin)
		}
	}
}
