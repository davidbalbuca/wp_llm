package whatsapp

import (
	"net/url"
	"regexp"
	"strings"
)

// CUANDO EL CLIENTE COMPARTE UN LUGAR, NO UN PIN.
//
// El link corto de Maps se resuelve a una URL final que puede traer tres cosas distintas:
//
//  1. COORDENADAS          → .../@-2.89,-79.00  o  ?q=-2.89,-79.00
//  2. PLUS CODE            → ?q=8FQ5RC9R%2B7X                          (pluscode.go, 21/09)
//  3. el NOMBRE DEL LUGAR  → ?q=Gapal,+Cuenca,+Ecuador&ftid=0x91cd...  ← esto
//
// El tercero sale cuando el cliente no manda su pin sino que elige un LUGAR del buscador de
// Maps. Le pasó a dos clientes reales:
//
//	22-sep 23:01  593961868344  → ?q=Gapal,+Cuenca,+Ecuador
//	08-sep 22:14  593995041865  → ?q=Centro+De+Salud+Valle,+Subcentro+de+Salud+de+El+Valle,+Cuenca
//
// El HTML de esa página tampoco trae coordenadas —Maps las carga por JavaScript—, así que leer
// el cuerpo no sirve. Lo único aprovechable es el nombre, y con el nombre se puede geocodificar.
//
// ESTE ARCHIVO SOLO EXTRAE EL NOMBRE. No inventa coordenadas: quien lo use tendrá que
// geocodificarlo contra un servicio real, y si no puede, pedir el pin como hasta ahora. Devolver
// una coordenada aproximada sería peor que no resolver — el repartidor saldría hacia un lugar
// equivocado y nadie se enteraría hasta que llegue.

// paramQDeMaps saca el valor de ?q= de una URL de Maps.
var paramQDeMaps = regexp.MustCompile(`[?&]q=([^&]+)`)

// pareceCoordenada reconoce "−2.89,−79.00": eso lo resuelve ParseCoordsFromText, no esto.
var pareceCoordenada = regexp.MustCompile(`^\s*-?\d{1,3}\.\d+\s*,\s*-?\d{1,3}\.\d+\s*$`)

// parecePlusCode reconoce un Open Location Code ("8FQ5RC9R+7X", "RC9R+7X Cuenca"): lo resuelve
// pluscode.go. Se mira si el TEXTO EMPIEZA por el código, que es como los pone Google.
var parecePlusCode = regexp.MustCompile(`^[23456789CFGHJMPQRVWX]{2,8}\+[23456789CFGHJMPQRVWX]{2,3}\b`)

// NombreDeLugarEnURL devuelve el nombre del sitio que el cliente compartió, si la URL final de
// Maps trae uno en vez de coordenadas.
//
// Devuelve ok=false cuando el ?q= es una coordenada o un Plus Code: esos tienen su propio
// camino, y tratarlos como "nombre de lugar" haría que el geocodificador busque un sitio
// llamado "-2.89,-79.00" y devuelva cualquier cosa.
func NombreDeLugarEnURL(u string) (string, bool) {
	m := paramQDeMaps.FindStringSubmatch(u)
	if len(m) < 2 {
		return "", false
	}
	// El valor viene como parámetro de URL: "+" son espacios y hay secuencias %XX.
	crudo, err := url.QueryUnescape(m[1])
	if err != nil {
		// Si no se puede decodificar, al menos los "+" sí son espacios.
		crudo = strings.ReplaceAll(m[1], "+", " ")
	}
	nombre := strings.TrimSpace(crudo)
	if nombre == "" {
		return "", false
	}
	if pareceCoordenada.MatchString(nombre) || parecePlusCode.MatchString(nombre) {
		return "", false
	}
	// Un nombre de lugar tiene letras. Sin esto, un ?q= con solo números o símbolos pasaría.
	if !tieneLetra(nombre) {
		return "", false
	}
	return nombre, true
}

func tieneLetra(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127 {
			return true
		}
	}
	return false
}
