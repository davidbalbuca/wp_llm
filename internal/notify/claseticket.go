// TRES COLAS, NO UN CAJÓN.
//
// El 26/09 la tabla `tickets` tenía 55 filas: 53 abiertas, 2 cerradas, en 23 días. Nadie cerraba
// nada porque no se podía distinguir lo urgente de lo que no era ni un problema:
//
//	21 de 55  "Pedido sin conductor"   -> no es un bug: es falta de cobertura. Nadie puede
//	                                      "resolver" que no había repartidor. Como ticket no sirve;
//	                                      como MÉTRICA por zona y hora, sí (contratar, redistribuir).
//	~12       errores de código        -> varios YA arreglados, pero sus tickets siguen abiertos,
//	                                      así que el panel no distingue lo vivo de lo muerto.
//	 ~8       cliente esperando        -> AQUÍ hay personas a las que se les prometió una llamada.
//	                                      Son los urgentes y estaban enterrados entre los otros 45.
//
// Mezclarlos tiene un coste medible: a 593991803684 le fallamos 10 veces y a 593979444899 cinco,
// y nadie lo vio porque el volumen de "sin conductor" ahogaba la señal.
//
// La clase se decide AQUÍ y no en cada sitio que reporta, porque todos los tickets pasan por
// ReportarFallo: un camino nuevo hereda la clasificación sin que nadie se acuerde de pedirla.
package notify

import "strings"

// ClaseTicket dice a QUIÉN le toca y con qué urgencia.
type ClaseTicket string

const (
	// ClaseCliente: hay una persona esperando respuesta. LA ÚNICA que urge.
	ClaseCliente ClaseTicket = "cliente"
	// ClaseBug: falla el código. Va al equipo técnico; se cierra con el arreglo.
	ClaseBug ClaseTicket = "bug"
	// ClaseCobertura: no había repartidor. NO va a la cola humana; alimenta la métrica de demanda
	// no atendida.
	ClaseCobertura ClaseTicket = "cobertura"
)

// clasificar deduce la clase del motivo. Se apoya en los motivos REALES de los 55 tickets del
// 3-sep al 26-sep, no en categorías inventadas.
//
// Ante la duda, ClaseCliente: equivocarse hacia "que lo mire una persona" cuesta una revisión de
// más; equivocarse hacia "es cobertura" deja a alguien esperando para siempre.
func clasificar(motivo string) ClaseTicket {
	m := strings.ToLower(motivo)

	// Falta de cobertura: 21 de los 55. El motivo lo dice literal.
	if strings.Contains(m, "sin conductor") || strings.Contains(m, "sin repartidor") {
		return ClaseCobertura
	}

	// Fallos del código: timeouts, geocercas, pedidos huérfanos. "Error técnico del agente" es el
	// timeout del turno; "no se puede asignar geocerca" es una ubicación fuera de los sectores.
	for _, marca := range []string{
		"error tecnico", "error técnico", // "…del agente", "…al registrar el pedido"
		"deadline", "timeout", "geocerca", "huerfano", "huérfano",
		"fallo al registrar", "error al registrar",
	} {
		if strings.Contains(m, marca) {
			return ClaseBug
		}
	}

	// Todo lo demás es una persona esperando: "el bot prometió que el equipo lo contactaría",
	// "quiere hablar con una persona", "cambiar forma de pago", "el repartidor no llega".
	return ClaseCliente
}

// EsperaAUnaPersona dice si esta clase necesita que alguien conteste. Lo usa el panel para
// separar la cola humana de lo que solo son datos.
func (c ClaseTicket) EsperaAUnaPersona() bool { return c == ClaseCliente }

// ClaseDeTicket es la vista pública: dado un motivo, su clase. El panel la usa para separar las
// colas SIN tocar el esquema de la base.
//
// Se deriva del motivo en vez de guardarse en una columna nueva a propósito: así los 53 tickets
// que YA existen quedan clasificados desde el primer día, sin migración y sin un backfill que
// habría que adivinar. Si algún día la clase necesita corregirse a mano por caso, entonces sí
// tocará columna; hoy sería complejidad sin uso.
func ClaseDeTicket(motivo string) ClaseTicket { return clasificar(motivo) }
