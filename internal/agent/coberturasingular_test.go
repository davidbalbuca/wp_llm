package agent

import "testing"

// Raúl (12/10, San Sebastián): había conductores en la zona pero ninguno a 4 km por calle y el
// backend contestó en singular. El bot lo tomó como fallo técnico ("no pude completar tu pedido,
// ya avisé al equipo" + dos tickets) en vez de ponerlo a buscar repartidor.
func TestSinConductorEnSingularEsFaltaDeRepartidor(t *testing.T) {
	for _, m := range []string{
		"No existe conductores cercanos",
		"No existen conductores cerca",
		"Error del backend: No existe conductores cercanos",
	} {
		if !esFalloDeCobertura(m) {
			t.Errorf("%q no se reconoció como falta de repartidor: el cliente recibiría un error técnico", m)
		}
	}
	if esFalloDeCobertura("La direccion no existe o esta eliminada") {
		t.Error("un error de verdad no puede pasar por falta de repartidor")
	}
}
