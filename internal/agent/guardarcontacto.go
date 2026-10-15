// AL CERRAR EL PEDIDO SE LE PIDE AL CLIENTE QUE NOS GUARDE EN SUS CONTACTOS.
//
// Pedido de David (21/09): "al finalizar el pedido acolítenlo para que el man diga algo como
// guárdame en tus contactos favoritos o cosas así — algo como 'recuerda que Ubi te contacta con
// el repartidor de gas más cercano'".
//
// POR QUÉ ESTE MOMENTO Y NO OTRO. El cliente acaba de recibir su gas y de calificar: es el
// instante de más buena voluntad de toda la conversación, y el único en el que pedirle algo no
// interrumpe lo que vino a hacer. Pedírselo mientras espera al repartidor, o a mitad del pedido,
// sería ruido justo cuando está pendiente de otra cosa.
//
// Y ES LO QUE DECIDE SI VUELVE. Un número guardado se busca por nombre en la agenda; uno sin
// guardar se pierde entre los chats, y el cliente que quiso repetir termina llamando por
// teléfono o pidiéndole a otro.
package agent

import "strings"

// MensajeGuardarContacto es la invitación que va pegada al agradecimiento final.
//
// Corto a propósito: llega cuando la conversación ya terminó, así que un párrafo largo se lee
// como publicidad y se ignora entero. Dos líneas: qué pedimos y para qué le sirve.
//
// Nombra a UBIGAS porque es lo que el cliente va a escribir en su buscador de contactos la próxima
// vez que se le acabe el gas. "Guárdanos" a secas no le dice cómo encontrarnos.
func MensajeGuardarContacto() string {
	return "📇 Guárdanos como *UbiGas* en tus contactos: así nos encuentras rapidito la próxima vez " +
		"que necesites gas y te conectamos con el repartidor más cercano 😉"
}

// INVITACIÓN A LA APP (pedido del dueño, 04/10): "comenzar a pedir al cliente que también se baje
// la app, porque por ahí también puede hacer el pedido". Y el 12/10: "tiene que vender la app por
// todo lado, pero con texto no tan cargado".
//
// Por eso va en CADA momento del pedido, siempre en UNA línea con el enlace al final:
//   - el saludo (LineaApp, bienvenida.go),
//   - la confirmación con repartidor y el cambio de repartidor (LineaAppEnCamino): es cuando más
//     le sirve, porque en la app ve el pedido en curso en vivo,
//   - la entrega (MensajeInvitarApp): ya tiene su gas, que la próxima vez la pida desde la app.
// Antes la de la entrega salía como mucho una vez cada 30 días; el dueño la quiere siempre.
//
// Lleva un VALOR concreto, no solo "descárgala": lo que el WhatsApp no le da.

// LinkApp es la página de DESCARGA de la app (botones de Google Play y App Store).
const LinkApp = "https://ubi.ec/app.html"

// MensajeInvitarApp es la línea que va dentro del aviso de entrega.
func MensajeInvitarApp() string {
	return "📲 Tu próximo gas pídelo desde la app: lo ves llegar en vivo y revisas tu consumo 👉 " + LinkApp
}

// LineaAppEnCamino va con la confirmación del pedido y con el cambio de repartidor (dueño, 12/10:
// "mientras esperas tu pedido te invito a descargar la app para dar seguimiento"). Es cierto:
// quien se registra en la app con el MISMO celular ve ahí este pedido en curso (union_clientes
// del backend, probado el 12/10).
func LineaAppEnCamino() string {
	return "📲 Mientras llega, baja nuestra app y sigue tu pedido en vivo 👉 " + LinkApp
}

// ConInvitacionApp anexa LineaAppEnCamino a un aviso de pedido en camino, salvo que el texto ya
// lleve el enlace de la app (no se repite dos veces en el mismo mensaje).
func ConInvitacionApp(reply string) string {
	if strings.Contains(reply, LinkApp) {
		return reply
	}
	return strings.TrimRight(reply, "\n ") + "\n\n" + LineaAppEnCamino()
}
