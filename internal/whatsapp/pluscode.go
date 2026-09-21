// PLUS CODES: convertir "52C7+6W8" en coordenadas, sin API y sin pedirle nada a Google.
//
// EL PROBLEMA QUE RESUELVE. El 21/09 Carlos (593986140905) mandó su ubicación como enlace de
// Google Maps y el bot le dijo "no pude abrir tu enlace". El código ya seguía el redirect del
// link corto (ver ResolverLinkCortoDeMaps); lo que cambió es lo que hay al final. Para un lugar
// sin dirección exacta —un conjunto, una casa sin nomenclatura, que es lo normal en media
// Cuenca— Google ya no pone lat,lng en la URL:
//
//	https://www.google.com/maps/place/52C7%2B6W8+Conjunto+NANCYTA+minidepas,+Unnamed+Road,+Cuenca/...
//
// Ese "52C7+6W8" es un Open Location Code: un estándar abierto que se decodifica con aritmética.
//
// EL RIESGO, y por qué esto se hace con cuidado. El código de la URL es CORTO: le faltan los
// cuatro caracteres del prefijo, que dicen en qué cuadrante del planeta está. Cada prefijo cubre
// un grado de lado, unos 111 km. Cuenca centro (6792) y la casa de Carlos (6793) caen en
// prefijos DISTINTOS aunque estén a 8 km uno del otro. Adivinar mal manda el gas a otra
// provincia.
//
// Por eso se recupera como manda el estándar (recoverNearest): se prueban los cuadrantes vecinos
// y se elige el que queda más cerca de la referencia. Los candidatos equivocados quedan a 111 km,
// así que la decisión nunca es por poco margen.
//
// Y ADEMÁS HAY RED. La geocerca del backend valida la ubicación después de esto. Si la
// recuperación fallara, el pedido se rechaza por cobertura; no sale un camión a ciegas.
package whatsapp

import (
	"math"
	"regexp"
	"strings"
)

// alfabetoOLC son los 20 caracteres del estándar. No están el 0, el 1, la I, la L ni la O: se
// eligieron así para que nadie confunda un carácter al leerlo o dictarlo por teléfono.
const alfabetoOLC = "23456789CFGHJMPQRVWX"

// El estándar parte de un rectángulo del mundo entero (180 de alto, 360 de ancho) y cada par de
// caracteres lo divide en 20x20. Se arranca en 400 porque el primer paso ya divide.
const resolucionInicial = 400.0

// plusCodeRe reconoce un Plus Code suelto: de 4 a 8 caracteres del alfabeto, un "+", y de 2 a 3
// después. Se exige que no venga pegado a otras letras o números para no confundirlo con un
// trozo de un identificador cualquiera.
var plusCodeRe = regexp.MustCompile(`(?i)(^|[^0-9A-Z])([23456789CFGHJMPQRVWX]{4,8}\+[23456789CFGHJMPQRVWX]{2,3})($|[^0-9A-Z])`)

// CoordenadasDePlusCodeEnURL saca el Plus Code de una URL de Google Maps y lo convierte.
//
// La URL trae el código percent-encoded ("52C7%2B6W8"), así que primero se deshace ese encoding.
// refLat/refLng es desde dónde se recupera el prefijo cuando el código es corto.
func CoordenadasDePlusCodeEnURL(url string, refLat, refLng float64) (lat, lng float64, ok bool) {
	// Solo el "%2B" (el "+" escapado), que es lo único que hace falta deshacer para encontrar el
	// código. Decodificar la URL entera traería "+" espurios de otros parámetros.
	limpia := strings.ReplaceAll(url, "%2B", "+")
	limpia = strings.ReplaceAll(limpia, "%2b", "+")

	m := plusCodeRe.FindStringSubmatch(limpia)
	if m == nil {
		return 0, 0, false
	}
	return PlusCodeACoordenadas(m[2], refLat, refLng)
}

// PlusCodeACoordenadas convierte un Plus Code en el CENTRO del área que representa.
//
// Si el código es completo (8 caracteres antes del "+") la referencia no se usa. Si es corto, se
// recupera el prefijo desde refLat/refLng.
//
// Devuelve ok=false ante cualquier cosa que no sea un código válido: un falso positivo aquí
// mandaría un camión a un punto inventado, así que ante la duda es mejor que el bot siga
// pidiendo el pin.
func PlusCodeACoordenadas(codigo string, refLat, refLng float64) (lat, lng float64, ok bool) {
	codigo = strings.ToUpper(strings.TrimSpace(codigo))
	if !esPlusCodeValido(codigo) {
		return 0, 0, false
	}
	antes, _, _ := strings.Cut(codigo, "+")
	if len(antes) == 8 {
		return decodificarOLC(codigo)
	}
	return recuperarYDecodificar(codigo, refLat, refLng)
}

// CoordenadasAPlusCodeCorto genera el código corto de un punto, como el que pone Google en sus
// URLs. Existe para poder PROBAR la ida y vuelta con puntos reales de Cuenca: sin esto, los
// tests solo podrían comprobar el único código que capturamos en producción.
func CoordenadasAPlusCodeCorto(lat, lng float64) (string, bool) {
	completo, ok := codificarOLC(lat, lng)
	if !ok {
		return "", false
	}
	// El corto quita los 4 primeros caracteres, que es justo lo que hace Google.
	return completo[4:], true
}

// esPlusCodeValido comprueba la forma: caracteres del alfabeto, un solo "+", y longitudes
// admitidas a cada lado.
func esPlusCodeValido(codigo string) bool {
	antes, despues, hayMas := strings.Cut(codigo, "+")
	if !hayMas || strings.Contains(despues, "+") {
		return false
	}
	// Antes del "+": 4 (corto), 6 u 8 (completo). Después: 2 o 3.
	if antes != "" && len(antes)%2 != 0 {
		return false
	}
	if len(antes) < 4 || len(antes) > 8 || len(despues) < 2 || len(despues) > 3 {
		return false
	}
	for _, r := range antes + despues {
		if !strings.ContainsRune(alfabetoOLC, r) {
			return false
		}
	}
	return true
}

// decodificarOLC convierte un código COMPLETO en el centro de su área.
func decodificarOLC(codigo string) (lat, lng float64, ok bool) {
	limpio := strings.ReplaceAll(codigo, "+", "")
	lat, lng = -90.0, -180.0
	latRes, lngRes := resolucionInicial, resolucionInicial

	// Se consumen pares: el primero de cada par mueve la latitud, el segundo la longitud.
	pares := len(limpio) / 2
	for i := 0; i < pares; i++ {
		latRes /= 20
		lngRes /= 20
		a := strings.IndexRune(alfabetoOLC, rune(limpio[2*i]))
		b := strings.IndexRune(alfabetoOLC, rune(limpio[2*i+1]))
		if a < 0 || b < 0 {
			return 0, 0, false
		}
		lat += float64(a) * latRes
		lng += float64(b) * lngRes
	}
	// Se devuelve el CENTRO del área, no su esquina: es el punto más probable dentro de ella.
	lat += latRes / 2
	lng += lngRes / 2
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

// recuperarYDecodificar completa un código corto con el prefijo del cuadrante que queda más
// cerca de la referencia.
//
// Se prueban el cuadrante de la referencia y sus ocho vecinos. El estándar lo hace así porque el
// código corto siempre se refiere al área cercana a donde se compartió: un cuadrante mide 111 km
// y un cliente de Cuenca no está pidiendo gas a 111 km.
func recuperarYDecodificar(corto string, refLat, refLng float64) (lat, lng float64, ok bool) {
	if refLat < -90 || refLat > 90 || refLng < -180 || refLng > 180 {
		return 0, 0, false
	}
	base, hayBase := codificarOLC(refLat, refLng)
	if !hayBase {
		return 0, 0, false
	}
	prefijo := base[:4]

	mejorDist := math.Inf(1)
	var mejorLat, mejorLng float64
	hayMejor := false

	// Los dos últimos caracteres del prefijo son la celda de un grado; se mueven ±1 en cada eje.
	posLat := strings.IndexRune(alfabetoOLC, rune(prefijo[2]))
	posLng := strings.IndexRune(alfabetoOLC, rune(prefijo[3]))
	if posLat < 0 || posLng < 0 {
		return 0, 0, false
	}
	for dLat := -1; dLat <= 1; dLat++ {
		for dLng := -1; dLng <= 1; dLng++ {
			a, b := posLat+dLat, posLng+dLng
			if a < 0 || a >= len(alfabetoOLC) || b < 0 || b >= len(alfabetoOLC) {
				continue
			}
			candidato := prefijo[:2] + string(alfabetoOLC[a]) + string(alfabetoOLC[b]) + corto
			cLat, cLng, valido := decodificarOLC(candidato)
			if !valido {
				continue
			}
			// Distancia en grados: basta para elegir, porque los candidatos están a un grado
			// entero unos de otros y nunca hay empate ajustado.
			d := math.Hypot(cLat-refLat, cLng-refLng)
			if d < mejorDist {
				mejorDist, mejorLat, mejorLng, hayMejor = d, cLat, cLng, true
			}
		}
	}
	if !hayMejor {
		return 0, 0, false
	}
	return mejorLat, mejorLng, true
}

// codificarOLC genera el código completo (8 caracteres + "+" + 2) de un punto.
func codificarOLC(lat, lng float64) (string, bool) {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return "", false
	}
	// El polo norte exacto caería fuera de la última celda.
	if lat >= 90 {
		lat = 89.999999
	}
	lat += 90
	lng += 180
	latRes, lngRes := resolucionInicial, resolucionInicial

	var b strings.Builder
	for i := 0; i < 5; i++ {
		latRes /= 20
		lngRes /= 20
		a := int(lat / latRes)
		c := int(lng / lngRes)
		if a >= len(alfabetoOLC) {
			a = len(alfabetoOLC) - 1
		}
		if c >= len(alfabetoOLC) {
			c = len(alfabetoOLC) - 1
		}
		b.WriteByte(alfabetoOLC[a])
		b.WriteByte(alfabetoOLC[c])
		lat -= float64(a) * latRes
		lng -= float64(c) * lngRes
		if b.Len() == 8 {
			b.WriteByte('+')
		}
	}
	return b.String(), true
}
