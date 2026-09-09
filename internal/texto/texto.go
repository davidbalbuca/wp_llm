// Comparación de texto libre del chat: normalizar y detectar afirmaciones. Sin estado ni
// dependencias del agente, para poder probarse y razonarse aparte.
package texto

import (
	"strings"
	"unicode"
)

// Normalizar deja el texto comparable: minúsculas, sin tildes y con los signos convertidos en
// espacios, para que "¿Cuántos conductores?" y "cuantos conductores" sean lo mismo.
func Normalizar(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			b.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			b.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			b.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô':
			b.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			b.WriteRune('u')
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// negaciones invalida una afirmación si aparece junto a las palabras clave: "el pedido NO se ha
// cancelado" no es una cancelación hecha.
var negaciones = map[string]bool{
	"no": true, "sin": true, "aun": true, "todavia": true, "nunca": true, "tampoco": true,
}

// lookbackNegacion es cuántas palabras se miran ANTES del inicio de la secuencia buscando una
// negación. Cubre "NO he cancelado tu pedido": el "no" cae antes del verbo, no entre las claves.
const lookbackNegacion = 2

// AfirmaSecuencia indica si el texto contiene ALGUNA de las secuencias dadas, con sus palabras
// EN ORDEN y hasta `holgura` palabras intercaladas entre una y la siguiente. Si hay una negación
// en el hueco (o justo antes), esa secuencia no cuenta.
//
// Reemplaza al matching de frases contiguas, que fallaba cuando el modelo metía palabras en
// medio: el 09/09 dijo "pedido HA SIDO cancelado" y no calzaba con "pedido cancelado".
// {"pedido","cancelado"} calza "pedido cancelado", "pedido ha sido cancelado" y "tu pedido ya
// quedo cancelado", pero no "pedido no cancelado" ni "quieres cancelar el pedido" (orden).
func AfirmaSecuencia(txt string, secuencias [][]string, holgura int) bool {
	palabras := strings.Fields(Normalizar(txt))
	for _, sec := range secuencias {
		if secuenciaPresente(palabras, sec, holgura) {
			return true
		}
	}
	return false
}

func secuenciaPresente(palabras, sec []string, holgura int) bool {
	if len(sec) == 0 {
		return false
	}
	for inicio := range palabras {
		if calzaDesde(palabras, sec, inicio, holgura) {
			return true
		}
	}
	return false
}

// calzaDesde intenta calzar `sec` empezando en palabras[inicio]. Falla si el salto entre dos
// claves supera `holgura`, o si hay una negación en el hueco o justo antes del inicio.
func calzaDesde(palabras, sec []string, inicio, holgura int) bool {
	if palabras[inicio] != sec[0] {
		return false
	}
	desde := inicio - lookbackNegacion
	if desde < 0 {
		desde = 0
	}
	if contieneNegacion(palabras[desde:inicio]) {
		return false
	}
	pos := inicio + 1
	for _, clave := range sec[1:] {
		encontrada := false
		for salto := 0; salto <= holgura && pos+salto < len(palabras); salto++ {
			if contieneNegacion(palabras[pos : pos+salto]) {
				return false
			}
			if palabras[pos+salto] == clave {
				pos = pos + salto + 1
				encontrada = true
				break
			}
		}
		if !encontrada {
			return false
		}
	}
	return true
}

func contieneNegacion(palabras []string) bool {
	for _, p := range palabras {
		if negaciones[p] {
			return true
		}
	}
	return false
}
