// POR QUÉ NO SE PUDO CANCELAR: UN MOTIVO, NO UNA FRASE.
//
// `cancelarPedido` devolvía prosa dirigida al modelo ("No encuentro la cuenta del cliente para
// cancelar el pedido. Discúlpate y dile que en un momento lo revisa el equipo."). El modelo la
// leyó como un diagnóstico de avería y abrió el ticket #56 titulado "Error técnico: no se pudo
// cancelar el pedido porque el sistema no encuentra su cuenta" — para un cliente que NUNCA había
// pedido nada. El operador recibía la orden de confirmar la cancelación de un pedido inexistente.
//
// La raíz: un texto pensado para guiar la redacción del modelo terminó siendo la CAUSA de una
// decisión operativa. Aquí se separan las dos cosas. El código decide con `motivoCancelacion`
// (un valor, comparable, sin ambigüedad) y solo después se redacta.
//
// La distinción que hay que preservar:
//
//	sin cuenta / sin pedido  -> ESTADO NORMAL. El cliente no llegó a pedir, o ya no tiene nada
//	                            vivo. Se le explica con amabilidad. NO es una avería, NO abre
//	                            ticket, NO menciona "error técnico".
//	backend falló            -> AVERÍA DE VERDAD. Hay un pedido real que sigue vivo y no se pudo
//	                            cancelar. Ahí sí: ticket con el pedido_id y aviso al equipo.
//
// Confundirlas cuesta en las dos direcciones: tratar un estado normal como avería llena la cola
// de soporte de trabajo inventado (ver specs/tickets-que-nadie-cierra.md), y tratar una avería
// como estado normal deja al cliente con un conductor en camino que él creía cancelado.
package agent

// motivoCancelacion es el resultado de intentar cancelar, visto por el CÓDIGO.
type motivoCancelacion int

const (
	// cancelacionHecha: el pedido quedó cancelado (o ya lo estaba, que para el cliente es lo mismo).
	cancelacionHecha motivoCancelacion = iota
	// cancelacionSinCuenta: el cliente no tiene cuenta en el backend. Nunca completó un pedido.
	cancelacionSinCuenta
	// cancelacionSinPedido: tiene cuenta, pero no hay ningún pedido vivo — ni anotado por el bot
	// ni en el backend.
	cancelacionSinPedido
	// cancelacionFalloBackend: había un pedido real y el backend no pudo cancelarlo. LA ÚNICA que
	// es una avería.
	cancelacionFalloBackend
)

// esAveria distingue el fallo real del estado normal. Es lo único que decide si se abre ticket.
func (m motivoCancelacion) esAveria() bool { return m == cancelacionFalloBackend }

// nadaQueCancelar: el cliente no tenía nada vivo. No es un fallo de nadie.
func (m motivoCancelacion) nadaQueCancelar() bool {
	return m == cancelacionSinCuenta || m == cancelacionSinPedido
}

// String da el motivo para el LOG, no para el cliente. Corto y estable, para poder buscarlo.
func (m motivoCancelacion) String() string {
	switch m {
	case cancelacionHecha:
		return "hecha"
	case cancelacionSinCuenta:
		return "sin_cuenta"
	case cancelacionSinPedido:
		return "sin_pedido"
	case cancelacionFalloBackend:
		return "backend_fallo"
	}
	return "desconocido"
}

// mensajeAlCliente es lo que DE VERDAD sale por WhatsApp. Se redacta aquí, en código, y no se
// deja a que el modelo interprete una instrucción: ese rodeo es el que produjo el #56.
//
// Ninguno de estos textos menciona un error técnico salvo cuando lo hay, y ninguno afirma haber
// avisado al equipo si no se creó el ticket (ver specs/afirmaciones-respaldadas-por-estado.md).
func (m motivoCancelacion) mensajeAlCliente() string {
	switch m {
	case cancelacionHecha:
		return "Listo, cancelé tu pedido 🙏. Cuando necesites tu gas, aquí estoy para ayudarte 😊"
	case cancelacionSinCuenta, cancelacionSinPedido:
		// La verdad, sin dramatizar: no hay nada en curso. Y la puerta abierta, porque muchos de
		// estos casos son clientes que se fueron a mitad del pedido (el caso Carlos).
		return "No tienes ningún pedido en curso ahora mismo, así que no hay nada que cancelar 😊 " +
			"Si quieres tu gas, dime el color y la cantidad y lo preparo enseguida 🚚"
	case cancelacionFalloBackend:
		// Aquí SÍ se avisa al equipo — y el ticket se crea antes de decirlo.
		return "Disculpa 🙏, tuve un problema al cancelar tu pedido. Ya avisé al equipo para que lo " +
			"cancele enseguida. Lamento la molestia."
	}
	return "No pude completar la cancelación. Dame un momento y lo reviso 🙏"
}
