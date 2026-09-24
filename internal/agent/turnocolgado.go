package agent

import (
	"log"
	"strings"
)

// LA PELOTA NO SE QUEDA DEL LADO DEL BOT.
//
// Este archivo NO es otro candado. Los candados de forzar.go, cobertura.go y compañía atacan
// cada uno una FRASE del modelo: "ya cancelé tu pedido", "no llegamos a tu zona". Nacen de un
// incidente, cazan una lista de expresiones, y el modelo siempre tiene una manera nueva de
// decir lo mismo. Son trece y siguen creciendo.
//
// Aquí se verifica algo distinto: una INVARIANTE sobre la forma del turno, que no depende de
// qué palabras usó el modelo. En WhatsApp un turno solo arranca cuando escribe el cliente. Por
// lo tanto, el bot únicamente puede cerrar un turno de dos maneras legítimas:
//
//  1. HIZO algo — llamó una herramienta (registró, canceló, verificó, mandó un menú).
//  2. Le DEVOLVIÓ el turno al cliente — le preguntó algo, o cerró la conversación.
//
// Si el turno termina sin ninguna de las dos, la conversación queda esperando al bot, y el bot
// no va a volver a hablar. Eso es un turno COLGADO: no hay quien lo despierte.
//
// Le pasó a Doris el 21/09 (593958615651). Dio color, cantidad, ubicación, aceptó las políticas
// y mandó su cédula. El bot contestó "Un momento mientras verifico tu información en el
// sistema..." sin llamar a verificar_cliente. Ella hizo exactamente lo que se le pidió:
// esperar. Quince minutos de silencio hasta que una persona la rescató a mano. Nunca hubo
// pedido.
//
// La promesa en FUTURO es más dañina que la mentira en pasado. Cuando el bot dice "ya lo
// hice" y no lo hizo, el cliente reclama y la conversación sigue viva. Cuando dice "ahora lo
// hago", el cliente se calla a esperar, y el silencio se ve igual que una conversación normal.

// turnoQuedaColgado dice si este turno termina dejando al cliente esperando una respuesta que
// nunca va a llegar.
//
// Se mira la FORMA del turno, no su contenido:
//   - si se llamó alguna herramienta, el bot hizo algo → no está colgado
//   - si el texto le devuelve el turno al cliente (pregunta o cierre) → no está colgado
//   - lo que queda es una afirmación suelta que no hace nada y no pregunta nada
//
// Deliberadamente NO se buscan frases como "un momento" o "enseguida". Esa lista sería el
// candado número catorce y volveríamos al mismo juego: el modelo dice "permíteme un segundito"
// y el parche no lo ve.
func (a *Agent) turnoQuedaColgado(t *turno, from, reply, dijoElModelo string) bool {
	if t.menuSent {
		return false // un menú es una pregunta con botones: la pelota queda del lado del cliente
	}
	if t.huboHerramienta {
		return false // el bot hizo algo de verdad en este turno
	}
	// Si un candado REEMPLAZÓ la respuesta, el texto que sale ya no es una promesa del modelo:
	// es una decisión del código. "Ahora mismo estoy buscando al repartidor más cercano" (FDS-1)
	// está escrito justo para que el cliente espere tranquilo sin hacer nada; pegarle un
	// "¿seguimos?" le pide lo contrario en el mismo mensaje. Y esos flujos tienen su propio
	// temporizador que los despierta, que es lo que le falta al turno del modelo.
	//
	// Se compara contra el texto original en vez de marcar una bandera dentro de cada candado:
	// así un candado nuevo queda cubierto sin que nadie se acuerde de marcarlo.
	if strings.TrimSpace(reply) != strings.TrimSpace(dijoElModelo) {
		return false
	}
	texto := strings.TrimSpace(reply)
	if texto == "" {
		return false // sin texto no hay nada que arreglar; de eso se ocupa el llamador
	}
	if devuelveElTurno(texto) {
		return false
	}
	// Y la condición que hace que esto sea un problema y no una manía: el cliente tiene que
	// estar A MEDIO PEDIR. A quien pregunta un precio y recibe "cuesta $3.50" no le quedó nada
	// pendiente, aunque el bot no haya preguntado de vuelta; agregarle "¿seguimos?" sería el
	// bot insistiendo por insistir. El daño real —el de Doris— es cortar a alguien que estaba
	// comprando.
	return a.clienteEstaAMedioPedir(from)
}

// soloInstruccion marca el retorno de una herramienta que NO hizo nada y únicamente le dijo al
// modelo qué hacer a continuación ("falta la cédula", "el menú necesita al menos 2 opciones",
// "no se pudo verificar al cliente"). Devuelve el mismo texto para poder escribir:
//
//	return soloInstruccion(t, "Falta la cédula del cliente. Pídesela…")
//
// Existe porque la invariante del turno colgado pregunta "¿el bot hizo algo?", y una
// herramienta que solo devolvió una instrucción deja la conversación exactamente igual que si
// el modelo hubiera escrito texto. El caso peligroso es el backend caído: el modelo SÍ llama a
// verificar_cliente, la llamada falla, el modelo escribe "un momento, ya verifico", y sin esta
// marca la invariante se desarmaría y el cliente quedaría colgado igual que Doris — pero ahora
// con la apariencia de estar cubierto.
func soloInstruccion(t *turno, texto string) string {
	t.sinEfecto = true
	return texto
}

// clienteEstaAMedioPedir dice si hay una transacción abierta que depende del bot para avanzar.
//
// Se mira el estado DURABLE del store, igual que hacen los candados de forzar.go, y no el
// historial: el estado es lo que sobrevive a un reinicio y no se presta a interpretaciones.
//
// Un pedido YA REGISTRADO no cuenta: ahí el flujo lo llevan el conductor y los avisos de
// entrega, no la conversación. Lo que cuenta es el tramo en que el cliente está armando el
// pedido y todavía no existe nada en el backend — justo donde se cayó Doris.
func (a *Agent) clienteEstaAMedioPedir(from string) bool {
	if p, hay := a.store.GetPedidoEnCurso(from); hay && !p.Vacio() {
		return true // eligió color y/o cantidad: está armando su pedido
	}
	// Las dos de abajo son el flujo de verificación por OTP, que HOY NO CORRE: el backend crea
	// al cliente ya verificado (ver georoutes/client.go) y nadie llama a SetPendingVerification
	// ni a SetOrderDraft fuera de los tests. Se dejan porque el día que ese flujo se reactive
	// son exactamente el tramo a cubrir, pero no hay que leerlas como cobertura real: en
	// producción, "a medio pedir" es hoy la ficha del pedido y nada más.
	if _, hay := a.store.GetPendingVerification(from); hay {
		return true // está en medio de la verificación por código
	}
	if _, hay := a.store.GetOrderDraft(from); hay {
		return true // hay un pedido en pausa esperando que se complete
	}
	return false
}

// devuelveElTurno dice si el texto deja al CLIENTE a cargo de hablar.
//
// Son dos formas, y ambas se reconocen por la estructura del castellano, no por listas de
// frases hechas:
//
//   - PREGUNTA: en español escrito siempre lleva "?" (y casi siempre "¿"), venga del modelo o
//     de un menú. Es el marcador más fiable que existe y no envejece.
//   - CIERRE: el bot se despide y deja la puerta abierta ("cualquier cosa, aquí estoy").
//     Nadie espera nada, así que la conversación no quedó a medias.
func devuelveElTurno(texto string) bool {
	if strings.ContainsAny(texto, "?¿") {
		return true
	}
	return esUnCierre(texto)
}

// formulasDeCierre son las despedidas que el bot usa. Aquí sí se miran palabras: una despedida
// no tiene ningún marcador gramatical propio que la distinga.
var formulasDeCierre = [][]string{
	{"aqui", "estoy"},
	{"aqui", "estamos"},
	{"cualquier", "cosa"},
	{"que", "estes", "bien"},
	{"buen", "dia"},
	{"hasta", "pronto"},
	{"te", "esperamos"},
	{"gracias", "por", "preferirnos"},
	{"escribeme", "cuando"},
	{"quedo", "atento"},
	{"estamos", "para", "servirte"},
}

// colaLibre es cuántas palabras pueden venir DESPUÉS de la fórmula para que siga contando como
// despedida. Una basta: cubre el emoji final y remates como "te esperamos PRONTO". Con dos ya
// entra una promesa ("te esperamos UN SEGUNDITO").
const colaLibre = 1

// esUnCierre dice si el mensaje TERMINA despidiéndose.
//
// Lo decisivo no es que aparezca la fórmula, sino que no venga nada detrás. "Estoy verificando
// tu cédula, CUALQUIER COSA te aviso por aquí" y "Dame un momento, AQUÍ ESTOY revisando tu
// información" son promesas colgadas de libro, y las dos contienen una despedida —pero el
// mensaje sigue después de ella ("...te aviso", "...revisando"). Una despedida de verdad no
// tiene nada detrás; eso es lo que las separa, y buscar la fórmula dentro del texto (que era el
// primer intento) dejaba pasar las cuatro promesas del 21/09.
func esUnCierre(texto string) bool {
	palabras := strings.Fields(normalizar(colaDelMensaje(texto)))
	for _, formula := range formulasDeCierre {
		if fin, ok := dondeTermina(palabras, formula); ok && len(palabras)-1-fin <= colaLibre {
			return true
		}
	}
	return false
}

// dondeTermina busca la fórmula en las palabras y devuelve el índice de su ÚLTIMA palabra.
// Las palabras de la fórmula tienen que ir seguidas (se tolera una intercalada, para "cualquier
// cosa, aquí estoy"): una despedida es una frase hecha, no una idea que se arma sobre la marcha.
func dondeTermina(palabras, formula []string) (int, bool) {
	for inicio := range palabras {
		if palabras[inicio] != formula[0] {
			continue
		}
		i, calza := inicio, true
		for _, clave := range formula[1:] {
			siguiente, hay := buscarCerca(palabras, clave, i+1)
			if !hay {
				calza = false
				break
			}
			i = siguiente
		}
		if calza {
			return i, true
		}
	}
	return 0, false
}

// buscarCerca halla `clave` a partir de `desde`, sin saltarse más de una palabra.
func buscarCerca(palabras []string, clave string, desde int) (int, bool) {
	hasta := desde + 1 // la siguiente, o una intercalada de por medio
	for i := desde; i <= hasta && i < len(palabras); i++ {
		if palabras[i] == clave {
			return i, true
		}
	}
	return 0, false
}

// colaDelMensaje devuelve la última oración del texto, que es donde vive una despedida real.
// Corta por el último separador fuerte (punto, salto de línea, punto y coma, dos puntos); si
// no hay ninguno, el mensaje entero ES su última oración.
//
// La coma NO separa a propósito: "gracias, aquí estoy" es una sola despedida, y partirla
// dejaría fuera el saludo que la acompaña.
func colaDelMensaje(texto string) string {
	t := strings.TrimRight(strings.TrimSpace(texto), " \t")
	corte := strings.LastIndexAny(t, ".\n;:")
	if corte < 0 {
		return t
	}
	cola := strings.TrimSpace(t[corte+1:])
	if cola == "" {
		// El texto termina en el separador ("...aquí estoy."): la última oración es la de antes.
		anterior := strings.TrimRight(t[:corte], " \t")
		if c := strings.LastIndexAny(anterior, ".\n;:"); c >= 0 {
			return strings.TrimSpace(anterior[c+1:])
		}
		return strings.TrimSpace(anterior)
	}
	return cola
}

// rescatarTurnoColgado devuelve el texto con el que se cierra un turno que iba a quedar
// colgado. No inventa que la acción ocurrió ni promete nada: le devuelve el turno al cliente
// preguntándole, que es la única forma de que la conversación pueda seguir.
//
// El texto del modelo se CONSERVA (suele ser amable y contextual, "Gracias, Doris.") y solo se
// le agrega el cierre que le faltaba. Sustituirlo entero haría que el bot sonara a máquina
// justo en el momento en que el cliente está por comprar.
func (a *Agent) rescatarTurnoColgado(from, reply string) string {
	log.Printf("[colgado] %s: el turno terminaba sin herramienta y sin pregunta; se le devuelve el turno al cliente", from)
	return strings.TrimSpace(reply) + "\n\n" + a.preguntaQueFalta(from)
}

// preguntaQueFalta arma la pregunta con la que se cierra el turno rescatado.
//
// Se nombra EL SIGUIENTE DATO QUE HACE FALTA, y no un "¿te ayudo con algo más?": esa fórmula
// da a entender que lo anterior ya terminó —justo lo que NO pasó— e invita a un "no, gracias"
// que mata el pedido. Los datos ya están a mano porque clienteEstaAMedioPedir acaba de
// mirarlos, así que preguntar lo concreto no cuesta nada y suena a conversación, no a plantilla.
func (a *Agent) preguntaQueFalta(from string) string {
	p, hay := a.store.GetPedidoEnCurso(from)
	if !hay {
		return "¿Seguimos con tu pedido? 😊"
	}
	// El orden es el del pedido: primero qué se lleva, después a dónde. Preguntar la ubicación
	// cuando todavía no se sabe el color adelanta un paso y desordena la conversación.
	switch {
	case p.Color == "":
		return "¿De qué color es tu cilindro? 😊"
	case p.Cantidad < 1:
		return "¿Cuántos cilindros te envío? 😊"
	}
	if _, tieneUbicacion := a.store.GetLocation(from); !tieneUbicacion {
		return "¿Me compartes tu ubicación 📎 para enviártelo?"
	}
	return "¿Seguimos con tu pedido? 😊"
}
