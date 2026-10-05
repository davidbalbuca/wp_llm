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

import (
	"time"

	"wp-llm-gas/internal/conversation"
)

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
// la app, porque por ahí también puede hacer el pedido".
//
// Solo en el aviso de ENTREGA: ya tiene su gas y está contento, que es cuando más probable es que
// la descargue. En el saludo no, porque ahí lo que quiere es pedir y mandarlo a otro lado lo pierde.
//
// Y como mucho una vez cada 30 días: quien pide cada semana la vería en cada entrega y se vuelve
// publicidad.
//
// Lleva un VALOR concreto, no solo "descárgala": en la app ve su consumo de gas y cuándo le toca
// pedir otra vez (pedido del dueño, 04/10). Es lo que el WhatsApp no le da.

// LinkApp es la página de DESCARGA de la app (botones de Google Play y App Store).
const LinkApp = "https://ubi.ec/app.html"

// intervaloInvitacionApp es lo mínimo que pasa entre dos invitaciones al mismo cliente.
const intervaloInvitacionApp = 30 * 24 * time.Hour

// MensajeInvitarApp es la línea que va dentro del aviso de entrega.
func MensajeInvitarApp() string {
	return "📲 ¿Sabías que en nuestra app puedes ver tu consumo de gas y saber cuándo te toca " +
		"pedir otra vez? Descárgala gratis 👉 " + LinkApp
}

// TocaInvitarApp dice si a este cliente le corresponde la invitación en esta entrega.
func TocaInvitarApp(store conversation.Store, phone string, ahora time.Time) bool {
	ultima, hubo := store.UltimaInvitacionApp(phone)
	return !hubo || ahora.Sub(ultima) >= intervaloInvitacionApp
}
