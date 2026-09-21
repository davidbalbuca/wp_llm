// NO SE LE OFRECE AL CLIENTE UN CAMINO QUE DESPUÉS SE LE NIEGA.
//
// El 20/09 el modelo se inventó este menú, que no existe en ninguna parte del código:
//
//	📋 ¿Cómo prefieres compartirla?
//	• Compartir ubicación actual
//	• Escribir dirección
//
// Jessica (593968179884, 08:30:54) eligió la segunda y el bot le contestó "Entendido, pero
// necesito que me compartas tu ubicación por WhatsApp". Otra clienta (593997141025) vio el mismo
// menú a las 14:59 y no volvió a escribir nunca.
//
// La regla de no aceptar direcciones escritas es correcta y no se toca: mandar el gas a otra
// casa es el error más caro del negocio. Lo que está mal es OFRECERLA. Un menú es una promesa
// —"cualquiera de estas opciones vale"— y romperla deja al cliente sin saber qué hacer; peor que
// no haberle ofrecido nada.
//
// El prompt ya lo prohíbe, pero el prompt es una capa y el modelo no siempre obedece: es la
// misma lección del candado de los menús. Esto es la capa que no depende de él.
package agent

import "log"

// revisarMenuTrampa dice si este menú le ofrece al cliente escribir su dirección, que es algo
// que el bot no puede aceptar.
//
// Mira SOLO las opciones, no el cuerpo. El cuerpo puede mencionar la dirección con toda
// legitimidad ("¿te lo enviamos a Casa, en Av. Solano 123?"); lo que no puede es haber un botón
// que prometa un camino cerrado.
func menuOfreceEscribirDireccion(opciones []string) bool {
	for _, opcion := range opciones {
		if opcionEsEscribirDireccion(opcion) {
			return true
		}
	}
	return false
}

// opcionEsEscribirDireccion reconoce el botón prohibido en sus muchas redacciones.
//
// Se exige el VERBO junto al sustantivo, y por eso no basta con buscar "dirección": "Casa (Av.
// Solano 123)" y "Otra dirección" son opciones legítimas del menú de direcciones guardadas, que
// apuntan a ubicaciones reales que el cliente ya confirmó. Lo prohibido es el botón que invita a
// TECLEAR una dirección nueva.
func opcionEsEscribirDireccion(opcion string) bool {
	return afirmaSecuencia(opcion, [][]string{
		{"escribir", "direccion"}, {"escribir", "mi", "direccion"},
		{"escribir", "la", "direccion"}, {"escribir", "calles"},
		{"escribo", "direccion"}, {"te", "escribo", "direccion"},
		{"dictar", "direccion"}, {"dicto", "direccion"},
		{"indicar", "direccion"}, {"poner", "direccion"},
		{"escribir", "referencia"}, {"escribir", "por", "texto"},
	}, 3)
}

// avisoMenuTrampa es lo que se le devuelve al MODELO cuando su menú se rechaza. No lo ve el
// cliente: le dice qué pasó y qué hacer en su lugar, para que no se quede mudo ni lo reintente.
func avisoMenuTrampa(from string) string {
	log.Printf("[menu-trampa] %s: el modelo ofreció \"escribir dirección\", que no se puede "+
		"aceptar; el menú NO se envió", from)
	return "MENÚ RECHAZADO: una de sus opciones le ofrecía al cliente ESCRIBIR su dirección, y " +
		"eso no se puede aceptar (el pedido necesita la ubicación compartida o una guardada). " +
		"Ofrecerle un camino que luego le niegas lo deja sin saber qué hacer. NO mandes ningún " +
		"menú: pídele la ubicación por WhatsApp con una frase corta y amable."
}
