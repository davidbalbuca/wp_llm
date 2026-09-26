// UN TICKET SIN TELÉFONO USABLE NO SE PUEDE ATENDER.
//
// En los 55 tickets del 3-sep al 26-sep hay tres con el teléfono roto:
//
//	"+593"        -> solo el prefijo: no hay a quién llamar
//	"0991803684"  -> formato local (0…) en vez de E.164
//	"0992883555"  -> igual
//
// Los dos últimos son el MISMO cliente que aparece también como 593991803684 y 593992883555, así
// que además de no poder llamarle, sus tickets no se cruzan: a 593991803684 le fallamos 10 veces y
// sus casos quedaron partidos en dos identidades.
//
// Es el mismo problema que openwa ya resolvió en el backend (ver memory/openwa-telefono-normalizado.md,
// donde la misma clave se guardaba y se leía con formatos distintos y el aviso se perdía en
// silencio). Aquí se aplica al crear el ticket, que es el punto donde el dato queda escrito.
package notify

import "strings"

// normalizarTelefonoEC deja el número en E.164 sin '+': 593XXXXXXXXX.
//
// Devuelve "" si el número NO sirve para llamar. Un vacío es honesto —"no hay teléfono"— y se ve
// en el panel; un "+593" parece un dato y no lo es, que es la peor de las dos.
func normalizarTelefonoEC(tel string) string {
	// Fuera todo lo que no sea dígito: espacios, guiones, paréntesis y el '+'.
	var d strings.Builder
	for _, r := range tel {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	n := d.String()

	switch {
	case n == "" || n == "593":
		// El caso "+593": prefijo sin número.
		return ""
	case strings.HasPrefix(n, "593"):
		// Ya viene con país. El resto debe ser un celular de 9 dígitos (9XXXXXXXX).
		resto := n[3:]
		if len(resto) != 9 {
			return ""
		}
		return n
	case strings.HasPrefix(n, "0") && len(n) == 10:
		// Formato local: 0991803684 -> 593991803684.
		return "593" + n[1:]
	case len(n) == 9 && strings.HasPrefix(n, "9"):
		// Sin cero ni país: 991803684 -> 593991803684.
		return "593" + n
	}
	return ""
}
