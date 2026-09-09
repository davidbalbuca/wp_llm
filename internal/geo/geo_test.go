package geo

import "testing"

// El radio de 150 m tiene que aceptar el error del GPS en ciudad (una misma casa medida dos
// veces puede dar 50-100 m de diferencia). Si no, al cliente se le preguntaría "¿guardo esta
// ubicación?" cada vez que pide desde su propia casa.
func TestMismaUbicacionToleraElErrorDelGPS(t *testing.T) {
	// ~85 m al norte del mismo punto en Cuenca.
	if !MismaUbicacion(-2.898000, -79.002000, -2.897235, -79.002000) {
		t.Error("dos medidas de la MISMA casa se tomaron por sitios distintos: " +
			"el cliente recibiría la pregunta de guardar en cada pedido")
	}
}

// Y no puede ser tan ancho como para confundir dos casas distintas: el bot diría "te lo envío
// a Casa" apuntando a la de un vecino a varias manzanas.
func TestMismaUbicacionDistingueDireccionesDistintas(t *testing.T) {
	// ~1.1 km de separación.
	if MismaUbicacion(-2.898000, -79.002000, -2.888000, -79.002000) {
		t.Error("dos ubicaciones a más de 1 km se tomaron por la misma: " +
			"el bot entregaría en la dirección equivocada")
	}
}

// El haversine tiene que medir de verdad: si esto se rompe, todo lo que decide con distancias
// (guardar ubicación, repreguntar, nombrar el destino) decide mal.
func TestDistanciaMetrosMideDeVerdad(t *testing.T) {
	// 0.001° de latitud son ~111 m en cualquier punto del planeta.
	d := DistanciaMetros(-2.898, -79.002, -2.899, -79.002)
	if d < 105 || d > 118 {
		t.Errorf("0.001° de latitud deben ser ~111 m, y midió %.1f m", d)
	}
	if d := DistanciaMetros(-2.898, -79.002, -2.898, -79.002); d != 0 {
		t.Errorf("el mismo punto debe dar 0 m: %.1f", d)
	}
}
