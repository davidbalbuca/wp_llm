// Distancias entre coordenadas. Sin estado ni dependencias del agente.
package geo

import "math"

// RadioUbicacionMetros es cuánto pueden separarse dos coordenadas para considerarlas el mismo
// sitio. 150 m cubre el error del GPS en ciudad -entre edificios se va con facilidad a 50-100 m-
// sin llegar a confundir dos casas de manzanas distintas.
//
// El número importa en las dos direcciones: si fuera muy chico, al cliente se le preguntaría
// "¿guardo esta ubicación?" cada vez que pide desde su propia casa; si fuera muy grande, el bot
// le diría "te lo envío a Casa" apuntando a la de un vecino.
const RadioUbicacionMetros = 150.0

// MismaUbicacion indica si dos coordenadas son el mismo sitio (dentro de RadioUbicacionMetros).
func MismaUbicacion(lat1, lng1, lat2, lng2 float64) bool {
	return DistanciaMetros(lat1, lng1, lat2, lng2) <= RadioUbicacionMetros
}

// DistanciaMetros devuelve la distancia en metros entre dos coordenadas (haversine). A esta
// escala bastaría una aproximación plana, pero el haversine no tiene casos raros cerca del
// meridiano ni depende de la latitud, y se llama un puñado de veces por pedido.
func DistanciaMetros(lat1, lng1, lat2, lng2 float64) float64 {
	const radioTierraMetros = 6371000.0

	rad := func(g float64) float64 { return g * math.Pi / 180 }

	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return radioTierraMetros * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
