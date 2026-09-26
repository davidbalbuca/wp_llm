// EL PORTERO: NINGUNA AFIRMACIÓN OPERATIVA SALE SIN ESTADO QUE LA RESPALDE.
//
// Por qué existe, con los números del 26/09: la tabla `tickets` tenía 55 filas y SEIS de ellas
// (#23, #33, #40, #51, #52, #53) decían lo mismo — "el bot prometió al cliente que el equipo lo
// contactaría" sin que nadie lo hubiera avisado. Tres de esas seis son de esta semana.
//
// Hay ONCE candados vigilando lo que el modelo afirma, y sus propios comentarios cuentan la
// historia: se añadieron el 07/09, 09/09, 11/09, 12/09 y 25/09, cada uno DESPUÉS de que un cliente
// concreto pagara el fallo. Uno dice literalmente "el 09/09 falló 'está programada para hoy a
// las'". Cada candado caza una LISTA DE FRASES; el modelo redacta distinto y no salta nadie.
//
// El espacio de redacciones es infinito. El de estados, finito. Por eso el portero invierte la
// pregunta:
//
//	antes:  ¿esta frase está en alguna de mis listas negras?   (y si no, pasa)
//	ahora:  esta frase afirma el hecho H. ¿Tengo el estado que H exige?   (si no, no pasa)
//
// QUÉ NO ES. No sustituye a los once candados: ellos ARREGLAN (registran el pedido que faltaba,
// crean el ticket, mandan el menú) y son mejores en su caso particular porque saben repararlo.
// El portero es la RED FINAL que audita lo que quedó después de todos ellos — el mismo sitio y la
// misma filosofía que `turnoQuedaColgado`, que tampoco mira frases sino la forma del turno.
//
// Si el portero salta, es que un candado no cubrió su caso. El log lo dice para que se arregle el
// candado; mientras, el cliente no se lleva la mentira.
package agent

import "log"

// hechoOperativo es una cosa que, si el bot la afirma, el cliente cuelga creyendo que ya pasó.
// No son temas de conversación: son los cinco puntos donde una mentira cuesta una entrega.
type hechoOperativo int

const (
	hechoPedidoRegistrado hechoOperativo = iota
	hechoEntregaAgendada
	hechoEquipoAvisado
	hechoPedidoCancelado
	hechoRepartidorEnRuta
)

func (h hechoOperativo) String() string {
	switch h {
	case hechoPedidoRegistrado:
		return "pedido registrado"
	case hechoEntregaAgendada:
		return "entrega agendada"
	case hechoEquipoAvisado:
		return "equipo avisado"
	case hechoPedidoCancelado:
		return "pedido cancelado"
	case hechoRepartidorEnRuta:
		return "repartidor en ruta"
	}
	return "?"
}

// afirma dice si el texto afirma este hecho. REUSA los detectores que ya existen —no se duplica
// ni una lista de frases— porque llevan meses afinándose con casos reales; lo que cambia es que
// su veredicto ahora se cruza contra el estado en un solo sitio.
func (h hechoOperativo) afirma(texto string) bool {
	switch h {
	case hechoPedidoRegistrado:
		return afirmaPedidoConfirmado(texto)
	case hechoEntregaAgendada:
		return afirmaProgramado(texto)
	case hechoEquipoAvisado:
		return afirmaAvisoAlEquipo(texto)
	case hechoPedidoCancelado:
		return afirmaCancelado(texto)
	case hechoRepartidorEnRuta:
		return afirmaRepartidorEnRuta(texto)
	}
	return false
}

// respaldado dice si el ESTADO del sistema sostiene el hecho. Es la única autoridad: no importa
// cómo esté redactada la frase ni qué candado pasó antes.
func (a *Agent) respaldado(t *turno, from string, h hechoOperativo) bool {
	switch h {
	case hechoPedidoRegistrado:
		return a.tienePedidoVivo(from)
	case hechoEntregaAgendada:
		return a.store.TieneProgramacionViva(from)
	case hechoEquipoAvisado:
		// El ticket tiene que existir. t.escalado marca el de ESTE turno; un ticket abierto de
		// antes también vale: alguien ya está al tanto de este cliente.
		if t.escalado {
			return true
		}
		return len(a.store.ListTickets("abierto", 200)) > 0 && a.tieneTicketAbierto(from)
	case hechoPedidoCancelado:
		// Cancelado = el pedido YA NO está vivo. Al revés que los demás.
		return !a.tienePedidoVivo(from)
	case hechoRepartidorEnRuta:
		// Un repartidor en ruta exige un pedido vivo; sin él no hay a quién asignar.
		return a.tienePedidoVivo(from)
	}
	return true
}

// tieneTicketAbierto dice si este cliente tiene algún caso abierto.
func (a *Agent) tieneTicketAbierto(from string) bool {
	for _, tk := range a.store.ListTickets("abierto", 200) {
		if tk.Phone == from {
			return true
		}
	}
	return false
}

// hechosSinRespaldo devuelve los hechos que el texto afirma y el estado NO sostiene. Vacío = el
// mensaje puede salir tal cual.
func (a *Agent) hechosSinRespaldo(t *turno, from, reply string) []hechoOperativo {
	var malos []hechoOperativo
	for _, h := range []hechoOperativo{
		hechoPedidoRegistrado, hechoEntregaAgendada, hechoEquipoAvisado,
		hechoPedidoCancelado, hechoRepartidorEnRuta,
	} {
		if h.afirma(reply) && !a.respaldado(t, from, h) {
			malos = append(malos, h)
		}
	}
	return malos
}

// revisarHechosAfirmados es el portero. Va al FINAL del turno, después de todos los candados: lo
// que importa es lo que el cliente va a leer, no lo que el modelo escribió.
//
// Si algo pasó sin respaldo, se sustituye por una frase que NO afirma nada y devuelve el turno al
// cliente. Preferimos parecer torpes a mentir: un "dime otra vez" cuesta un mensaje; un "tu
// entrega está agendada" que es falso cuesta una entrega que nadie hará.
//
// El sustituto se compone AQUÍ, en código, y está construido para no disparar ningún detector —si
// lo hiciera, el portero se activaría con su propio texto, que es el bug del ticket #51: una
// disculpa falsa ("ya avisé al equipo") activó el detector de aviso y abrió un ticket que
// describía el error del primer candado. TestElSustitutoNoSeMuerdeLaCola lo fija.
func (a *Agent) revisarHechosAfirmados(t *turno, from, reply string) string {
	malos := a.hechosSinRespaldo(t, from, reply)
	if len(malos) == 0 {
		return reply
	}
	for _, h := range malos {
		// Que quede en el log: significa que un candado no cubrió su caso y hay que mirarlo.
		log.Printf("[portero] %s: el mensaje afirma %q y el estado NO lo respalda; se sustituye", from, h)
	}
	return "Disculpa, déjame confirmarte bien 😊 ¿Me repites qué necesitas para no equivocarme?"
}
