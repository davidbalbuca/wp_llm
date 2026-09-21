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

// MensajeGuardarContacto es la invitación que va pegada al agradecimiento final.
//
// Corto a propósito: llega cuando la conversación ya terminó, así que un párrafo largo se lee
// como publicidad y se ignora entero. Dos líneas: qué pedimos y para qué le sirve.
//
// Nombra a UBI porque es lo que el cliente va a escribir en su buscador de contactos la próxima
// vez que se le acabe el gas. "Guárdanos" a secas no le dice cómo encontrarnos.
func MensajeGuardarContacto() string {
	return "📇 Guárdanos como *Ubi* en tus contactos: así nos encuentras rapidito la próxima vez " +
		"que necesites gas y te conectamos con el repartidor más cercano 😉"
}
