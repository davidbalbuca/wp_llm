// LA PRESENTACIÓN VIAJA DENTRO DEL PRIMER MENSAJE, NO COMO UN WHATSAPP APARTE.
//
// Pedido del dueño (02/10, caso 593963518172): el cliente escribió "Gas" y recibió DOS mensajes
// seguidos —la presentación y, seis segundos después, el menú de colores—. El saludo ya salía una
// sola vez (el candado de saludounicomenu.go hizo su trabajo), pero eran dos notificaciones para
// una sola respuesta. "la idea es q envies solo uno con todo eso".
//
// Tiene razón y además es mejor producto: en WhatsApp dos mensajes del mismo remitente en seis
// segundos se leen como un bot atropellado, y el primero se salta porque el segundo es el que
// trae los botones.
//
// POR QUÉ AQUÍ Y NO EN EL WEBHOOK. Antes la presentación se enviaba en cmd/bot/main.go, antes de
// llamar al modelo, porque allí es donde se sabe si al cliente le toca (depende de LastActivity,
// que se pisa unas líneas después). Pero el webhook NO sabe qué va a salir después: puede ser un
// menú interactivo, un texto, o nada. Unirlos allí era imposible.
//
// Así que el webhook ya no la envía: la DEJA PENDIENTE en el store, y el primer mensaje que salga
// del turno se la lleva delante. Es el mismo patrón que `conCoberturaConfirmada` (cobertura.go),
// que ya resolvía este problema para la confirmación de zona, y por el mismo motivo: hay dos
// caminos de salida —el cuerpo de un menú y el texto de la respuesta— y la marca se consume en el
// que ocurra primero.
//
// LA RED DE SEGURIDAD IMPORTA. Hay caminos del webhook que terminan sin que el modelo hable
// (fuera de cobertura, media no soportada, control humano). Si la bienvenida se quedara pendiente
// y nadie la entregara, el cliente nuevo no recibiría NADA: peor que dos mensajes. Por eso el
// webhook la entrega al final si sigue pendiente (ver entregarBienvenidaPendiente en main.go).
package agent

import (
	"log"
	"strings"
)

// DejarBienvenidaPendiente guarda la presentación para que salga DENTRO del primer mensaje del
// turno. La llama el webhook en vez de enviarla por su cuenta.
//
// REGISTRA EL TURNO AQUÍ MISMO, y esto no es un detalle: el candado del doble saludo
// (saludounico.go) decide mirando si el ÚLTIMO turno del modelo es la presentación. Cuando la
// enviaba `avisarCliente` ese registro salía gratis; al dejarla pendiente hay que hacerlo a mano o
// `yaSePresentoElCodigo` devuelve false, el candado no actúa y vuelve el doble saludo que se
// arregló hoy mismo.
//
// Se registra ANTES de que salga —al contrario que avisarCliente, que envía y luego anota— porque
// los candados corren durante el turno y necesitan el dato ya puesto. El riesgo que eso abre (que
// el modelo crea que saludó y el envío falle) es menor: la presentación sale sí o sí, dentro del
// primer mensaje o por la red de seguridad del webhook.
func (a *Agent) DejarBienvenidaPendiente(from, texto string) {
	if strings.TrimSpace(texto) == "" {
		return
	}
	a.store.SetBienvenidaPendiente(from, texto)
	a.store.LogMessage(from, "system", texto)
	a.store.AppendModel(from, texto)
}

// HayBienvenidaPendiente dice si queda una presentación sin entregar. El webhook lo consulta al
// final del turno para mandarla suelta si ningún mensaje se la llevó (ver el comentario de
// arriba: un cliente nuevo no puede quedarse sin saludo).
func (a *Agent) HayBienvenidaPendiente(from string) (string, bool) {
	texto := a.store.BienvenidaPendiente(from)
	return texto, strings.TrimSpace(texto) != ""
}

// ConsumirBienvenidaPendiente la marca como entregada.
func (a *Agent) ConsumirBienvenidaPendiente(from string) {
	a.store.LimpiarBienvenidaPendiente(from)
}

// conBienvenida antepone la presentación al mensaje que va a salir, sea el cuerpo de un menú o el
// texto de la respuesta, y consume la marca. Si no hay nada pendiente devuelve el mensaje tal cual.
//
// El separador es una línea en blanco: en WhatsApp deja la presentación como un párrafo propio y
// la pregunta debajo, que es como se lee bien en un solo globo.
func (a *Agent) conBienvenida(from, mensaje string) string {
	bienvenida := strings.TrimSpace(a.store.BienvenidaPendiente(from))
	if bienvenida == "" {
		return mensaje
	}
	a.store.LimpiarBienvenidaPendiente(from)
	if strings.TrimSpace(mensaje) == "" {
		return bienvenida
	}
	log.Printf("[bienvenida] %s: la presentación viaja dentro del primer mensaje del turno", from)
	return bienvenida + "\n\n" + mensaje
}
