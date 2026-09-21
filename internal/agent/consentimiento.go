// CONSENTIMIENTO DE PROTECCIÓN DE DATOS: nadie da su cédula sin haber dado permiso antes.
//
// Pedido del dueño (18/09): justo antes de pedirle la cédula, el bot tiene que explicarle que
// por políticas de protección de datos la necesitamos, y el cliente acepta o niega CON UN BOTÓN.
// Solo si acepta se le pide la cédula. Si niega, no se continúa.
//
// POR QUÉ SON TRES PIEZAS Y NO UNA. El resto de los flujos del bot se resuelven con un
// interceptor que dispara ANTES del modelo sobre un estado durable. Aquí eso no alcanza: el bot
// no "decide" cuándo pedir la cédula —lo decide el modelo, cuando le parece que toca (behavior.md
// §8: "datos personales, SOLO al final")—. No hay estado sobre el que disparar antes. Así que:
//
//  1. CANDADO (revisarPeticionDeCedula): mira la respuesta ya redactada; si le está pidiendo la
//     cédula a alguien que no ha dado permiso, la REEMPLAZA por el menú de consentimiento.
//     Es la capa de UX: que el cliente nunca vea la petición antes del permiso.
//  2. INTERCEPTOR (ResponderConsentimiento): resuelve los botones en código, sin que el modelo
//     interprete el "sí". Un consentimiento legal no puede depender de que el modelo acierte.
//  3. COMPUERTA (consentimientoNiega): verificar_cliente y registrar_pedido se niegan a
//     ejecutarse si hay una negativa registrada. Decide por ESTADO, no por texto.
//
// La compuerta es la que de verdad garantiza el requisito: si el candado falla por una redacción
// que no previmos, la cédula todavía no entra a ninguna parte. El candado es comodidad; la
// compuerta es la garantía. Nunca al revés.
//
// A QUIÉN SE LE PREGUNTA. Solo a clientes que el bot no conoce, como se pidió: si ya tiene
// perfil guardado, el bot nunca le pide la cédula, así que el menú no aparece nunca.
//
// El caso que NO se puede cubrir, y conviene tenerlo claro: un número nuevo que resulta ser un
// cliente viejo. No hay forma de saber que está registrado antes de tener su cédula, y la cédula
// es justo lo que no podemos pedirle sin permiso. A ese se le pregunta una vez. Es la misma razón
// de fondo de todo este diseño: el teléfono dice con quién hablo, no quién es.
package agent

import (
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/conversation"
)

// Botones del menú de consentimiento. WhatsApp corta los títulos en ~20 caracteres.
const (
	BotonAceptoDatos   = "Sí, acepto"
	BotonNoAceptoDatos = "No acepto"
)

// Enlaces a las políticas publicadas. Ya contemplan este canal ("también puede solicitarse
// mediante nuestro canal automatizado de WhatsApp, al cual aplican estos mismos términos"), así
// que no hay que redactar nada aparte para el bot.
const (
	urlPrivacidad = "https://ubi.ec/privacidad.html"
	urlTerminos   = "https://ubi.ec/terminos.html"
)

func init() {
	// Los botones se registran como respuestas válidas, igual que los de confirmación: si mañana
	// se les cambia el texto, el clasificador no se queda atrás.
	respuestasAfirmativas[normalizarRespuesta(BotonAceptoDatos)] = true
	respuestasNegativas[normalizarRespuesta(BotonNoAceptoDatos)] = true
}

// pideLaCedula detecta que el texto le está pidiendo al cliente su cédula o identificación.
//
// Usa afirmaSecuencia (tolera palabras intercaladas y descarta negaciones) por lo mismo que los
// demás candados: el modelo redacta distinto cada vez. "Para registrar tu pedido necesito tu
// número de cédula" y "¿me compartes tu cédula?" son la misma petición.
func pideLaCedula(texto string) bool {
	// Hablar de la cédula no es pedirla. "Ya tengo tu cédula registrada" menciona las mismas
	// palabras y es justo lo contrario de una petición; taparla reemplazaría un mensaje normal
	// por el menú de consentimiento. Lo cazó el test negativo al primer intento.
	if yaTieneLaCedula(texto) {
		return false
	}
	return afirmaSecuencia(texto, [][]string{
		{"tu", "cedula"}, {"su", "cedula"}, {"la", "cedula"},
		{"numero", "de", "cedula"}, {"cedula", "de", "identidad"},
		{"tu", "identificacion"}, {"su", "identificacion"},
		{"numero", "de", "identificacion"},
		{"me", "das", "tu", "cedula"}, {"necesito", "tu", "cedula"},
		{"indicame", "tu", "cedula"}, {"facilitame", "tu", "cedula"},
		{"regalame", "tu", "cedula"}, {"compartes", "tu", "cedula"},
		{"dictame", "tu", "cedula"}, {"proporcionar", "tu", "cedula"},
	}, 3)
}

// yaTieneLaCedula reconoce las frases en las que el bot DICE que ya tiene la cédula, en vez de
// pedirla. Son las que más se parecen a una petición sin serlo.
func yaTieneLaCedula(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"ya", "tengo", "tu", "cedula"}, {"ya", "tengo", "tu", "identificacion"},
		{"ya", "tengo", "registrada", "tu", "cedula"},
		{"tu", "cedula", "ya", "esta"}, {"cedula", "ya", "registrada"},
		{"con", "tu", "cedula", "registrada"},
	}, 3)
}

// cuerpoConsentimiento es el texto del menú. Se explica POR QUÉ se pide antes de pedir nada: el
// cliente tiene que poder decidir con la información delante, no después.
func cuerpoConsentimiento() string {
	return "¡Perfecto, casi listo! Para poder emitir tu nota de venta necesito tu cédula 📋\n\n" +
		"Por políticas de protección de datos, antes de pedírtela necesito tu autorización " +
		"para tratarla. Aquí puedes revisar cómo cuidamos tu información:\n" +
		"• Privacidad: " + urlPrivacidad + "\n" +
		"• Términos: " + urlTerminos + "\n\n" +
		"¿Aceptas nuestras políticas de protección de datos?"
}

// mensajeSinConsentimiento es lo que recibe quien dice que NO. Se le explica sin culparlo y se
// le deja una vía humana: no poder atenderlo por aquí no es lo mismo que no poder atenderlo.
func (a *Agent) mensajeSinConsentimiento() string {
	msg := "Entiendo, y gracias por decírmelo 🙏 Sin esa autorización no puedo tomar tu pedido " +
		"por aquí, porque para registrarlo necesito tratar tus datos."
	// El teléfono sale de la configuración del negocio que ya se trae en vivo del backend, no de
	// una variable nueva: si el negocio lo cambia en su panel, el bot lo dice bien sin redeploy.
	if tel := a.telefonoDelNegocio(); tel != "" {
		msg += " Si prefieres hacerlo hablando con una persona, llámanos al " + tel + " 📞"
	}
	return msg + " Y si cambias de opinión, escríbeme cuando quieras y lo retomamos 😊"
}

// telefonoDelNegocio devuelve el teléfono de atención publicado ("" si no se conoce).
func (a *Agent) telefonoDelNegocio() string {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return ""
	}
	return strings.TrimSpace(contexto.Business.Telefono)
}

// revisarPeticionDeCedula es el CANDADO: intercepta la respuesta en la que el modelo le pide la
// cédula a un cliente que todavía no autorizó el tratamiento de sus datos, y la reemplaza por el
// menú de consentimiento.
//
// Decide por ESTADO (¿hay consentimiento registrado?) y solo usa el texto para saber QUÉ momento
// del flujo es. Devuelve la respuesta que debe salir; si mandó el menú, marca t.menuSent para que
// el llamador no envíe además el texto (duplicaría la pregunta).
func (a *Agent) revisarPeticionDeCedula(t *turno, from, reply string) string {
	if !pideLaCedula(reply) {
		return reply
	}
	// Ya respondió antes: no se le vuelve a preguntar. Es lo que se pidió explícitamente —
	// "verificando si el cliente no aceptó ya la protección de datos para no volver a preguntar".
	if c, ya := a.store.GetConsentimiento(from); ya {
		if c.Acepta {
			return reply // autorizó: la petición de cédula sale tal cual
		}
		// Se negó antes y el modelo le está pidiendo la cédula de nuevo. No puede salir.
		log.Printf("[consentimiento] %s ya negó el permiso y el modelo volvió a pedirle la cédula; "+
			"se bloquea", from)
		return a.mensajeSinConsentimiento()
	}
	// Ya se le mandó el menú y no ha contestado. Se le RECUERDA con los botones otra vez, en vez
	// de mandarle el menú completo de nuevo: repetir el mismo texto largo es lo que behavior.md
	// §5 prohíbe, y pasó en producción tres veces seguidas.
	//
	// Pero los BOTONES sí se reenvían, y esto es importante: el menú original ya quedó enterrado
	// varios mensajes más arriba en el chat. Pedirle que confirme sin darle nada que tocar es
	// pedirle que busque para atrás, y quien no lo encuentre se queda sin poder seguir.
	if a.store.ConsentimientoPendiente(from) {
		log.Printf("[consentimiento] %s tiene el menú pendiente; se le recuerda con los botones", from)
		recordatorio := "Para seguir necesito que me confirmes si aceptas nuestras políticas de " +
			"protección de datos 🙏 Puedes revisarlas aquí: " + urlPrivacidad
		if err := a.mandarMenu(from, recordatorio, []string{BotonAceptoDatos, BotonNoAceptoDatos}); err != nil {
			log.Printf("[consentimiento] %s: el recordatorio falló (%v); sale como texto", from, err)
			return recordatorio + "\n\nRespóndeme *Sí* o *No*."
		}
		t.menuSent = true
		t.lastMenuText = recordatorio + " [" + BotonAceptoDatos + " / " + BotonNoAceptoDatos + "]"
		return ""
	}

	log.Printf("[consentimiento] %s: el modelo pidió la cédula sin autorización previa; "+
		"se pide el consentimiento primero", from)
	if !a.pedirConsentimiento(from) {
		// El menú no salió (WhatsApp falló). NO se deja pasar la petición de cédula: pedirla sin
		// permiso es justo lo que este candado existe para impedir. Se pregunta por texto.
		return cuerpoConsentimiento() + "\n\nRespóndeme *Sí* o *No*."
	}
	t.menuSent = true
	t.lastMenuText = cuerpoConsentimiento() + " [" + BotonAceptoDatos + " / " + BotonNoAceptoDatos + "]"
	return ""
}

// pedirConsentimiento manda el menú y deja marcada la espera. Devuelve false si el menú no salió.
func (a *Agent) pedirConsentimiento(from string) bool {
	cuerpo := cuerpoConsentimiento()
	opciones := []string{BotonAceptoDatos, BotonNoAceptoDatos}
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[consentimiento] %s: el menú falló (%v); se pregunta por texto", from, err)
		// La espera se marca IGUAL aunque el menú no saliera: el llamador pregunta por texto, y
		// sin esta marca la respuesta del cliente se iría al modelo en vez de resolverse en
		// código.
		a.store.SetConsentimientoPendiente(from)
		return false
	}
	a.store.SetConsentimientoPendiente(from)
	return true
}

// ResponderConsentimiento es el INTERCEPTOR: resuelve la respuesta del cliente al menú de
// consentimiento en código, antes de que el mensaje llegue al modelo.
//
// Va en código y no en el prompt porque de esto depende que un dato personal entre o no entre a
// la base: si el modelo interpretara mal un "no", registraríamos la cédula de alguien que la
// negó. Eso no se puede dejar a una interpretación.
//
// Si el cliente responde algo que no es sí ni no (una pregunta, un reclamo, "¿para qué la
// necesitan?"), devuelve manejado=false y lo atiende el modelo: ahí hay una duda legítima que
// merece respuesta, y forzarle un sí/no sería maltratarlo.
func (a *Agent) ResponderConsentimiento(from, texto string) (string, bool) {
	if !a.store.ConsentimientoPendiente(from) {
		return "", false
	}
	respuesta := normalizarRespuesta(texto)
	if respuesta == "" {
		return "", false
	}
	acepta, niega := a.respuestaAlMenuDeDatos(from, respuesta)

	switch {
	case acepta:
		log.Printf("[consentimiento] %s ACEPTÓ las políticas de datos", from)
		a.store.ClearConsentimientoPendiente(from)
		a.store.SetConsentimiento(from, conversation.Consentimiento{
			Acepta: true, Fecha: time.Now(),
		})
		msg := a.mensajeTrasAceptar(from)
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, msg)
		return msg, true

	case niega:
		log.Printf("[consentimiento] %s NO aceptó las políticas de datos; no se le pide la cédula", from)
		a.store.ClearConsentimientoPendiente(from)
		a.store.SetConsentimiento(from, conversation.Consentimiento{
			Acepta: false, Fecha: time.Now(),
		})
		msg := a.mensajeSinConsentimiento()
		// Se cierra el ciclo: la conversación termina aquí y la próxima vez que escriba arranca de
		// cero, como se pidió. El consentimiento NO se va con la limpieza —vive en su propia
		// tabla— así que no se le vuelve a preguntar lo que ya respondió.
		//
		// El orden importa: cerrar limpia el historial, así que los turnos se escriben DESPUÉS.
		// Al revés, el turno de la negativa se borraría a sí mismo.
		a.cerrarCicloDeConversacion(from, "el cliente no aceptó las políticas de datos")
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, msg)
		return msg, true
	}

	// Una pregunta, una duda, cualquier otra cosa: la atiende el modelo. La espera sigue en pie.
	return "", false
}

// Lo único que cuenta como respuesta cuando la pregunta salió CON BOTONES. Son los títulos que
// devuelve WhatsApp al tocarlos, más las formas en que alguien los escribiría a mano.
var (
	aceptacionesLiterales = map[string]bool{
		"si acepto": true, "acepto": true, "si acepto las politicas": true,
	}
	negativasLiterales = map[string]bool{
		"no acepto": true, "no acepto las politicas": true,
	}
)

// respuestaAlMenuDeDatos decide si este mensaje es un sí, un no, o ninguna de las dos cosas.
//
// LA DIFERENCIA CON EL RESTO DE LOS MENÚS DEL BOT, que es la razón de que esta función exista:
// en los demás, aceptar de más cuesta un viaje en balde y el cliente lo corrige en el momento.
// Aquí lo que se está firmando es el permiso para tratar la cédula de una persona. Un falso
// positivo no se corrige: queda escrito que alguien autorizó algo que nunca autorizó.
//
// LO QUE DISTINGUE UN "SÍ" BUENO DE UNO MALO NO ES CÓMO SE PREGUNTÓ, SINO A QUÉ RESPONDE. Lo
// enseñaron los cuatro consentimientos falsos del 21/09, puestos al lado del único "Si" que sí
// era válido:
//
//	07:04 bot: menú de datos → 07:06 bot: "¿en qué tiempo?" + RE-PREGUNTA las políticas
//	→ 07:06 cliente: "Si"                                            ← VÁLIDO: contesta el menú
//
//	09:06 bot: menú de datos → 09:07 bot: "¡tu pedido quedó registrado!"
//	→ 09:07 cliente: "Ok"                                            ← NO: contesta a otra cosa
//
// En los cuatro casos malos, entre el menú y la palabra el bot preguntó o dijo OTRA cosa, y el
// cliente estaba contestando a eso. En el bueno, lo último que el bot había preguntado seguía
// siendo el consentimiento.
//
// Así que la pregunta correcta es: ¿el menú de datos sigue siendo lo ÚLTIMO que el bot preguntó?
//   - Sí  → vale el sí/no suelto ("si", "no", "acepto"): está contestando a eso y a nada más.
//   - No  → el hilo se movió; solo vale el literal del botón, que es inequívoco venga cuando venga.
//
// El primer intento de este arreglo usaba "¿se preguntó con botones?" y era el criterio
// equivocado: dejaba fuera el "si" a secas de quien tenía los botones delante —mucha gente
// escribe en vez de tocar— y lo dejaba ATRAPADO, porque el recordatorio le pedía confirmar sin
// aceptarle ninguna forma de hacerlo. Un cliente bloqueado para siempre es peor que el bug que
// veníamos a arreglar.
func (a *Agent) respuestaAlMenuDeDatos(from, respuesta string) (acepta, niega bool) {
	// El literal del botón vale SIEMPRE: es inequívoco, diga lo que diga el resto del hilo.
	if aceptacionesLiterales[respuesta] {
		return true, false
	}
	if negativasLiterales[respuesta] {
		return false, true
	}
	// El sí/no suelto solo cuenta si el menú es lo último que se preguntó.
	if !a.menuDeDatosSigueSiendoLaUltimaPregunta(from) {
		return false, false
	}
	return afirmacionesSimples[respuesta], negacionesSimples[respuesta]
}

// menuDeDatosSigueSiendoLaUltimaPregunta mira el último turno del BOT en el historial y dice si
// fue el menú de consentimiento.
//
// Se mira el historial y no una bandera aparte porque el historial es lo que de verdad vio el
// cliente, en el orden en que lo vio. Una bandera habría que acordarse de apagarla en cada sitio
// donde el bot dice algo —y el olvido de uno solo reintroduce el bug.
//
// Sin historial (el bot se reinició, o se limpió la conversación) devuelve FALSO: ante la duda,
// se exige el literal del botón. Equivocarse hacia ese lado hace que se le vuelva a preguntar;
// hacia el otro, graba un consentimiento que nadie dio.
func (a *Agent) menuDeDatosSigueSiendoLaUltimaPregunta(from string) bool {
	historial := a.store.History(from)
	for i := len(historial) - 1; i >= 0; i-- {
		turno := historial[i]
		if turno == nil || turno.Role != "model" {
			continue
		}
		var dijo strings.Builder
		for _, parte := range turno.Parts {
			dijo.WriteString(parte.Text)
		}
		return esElMenuDeDatos(dijo.String())
	}
	return false
}

// esElMenuDeDatos reconoce el texto del menú de consentimiento en el historial. Se busca por lo
// que ese mensaje tiene de propio —la pregunta por las políticas y el enlace de privacidad— y no
// por el texto completo, que cambia según se haya enviado como menú o como respaldo escrito.
func esElMenuDeDatos(texto string) bool {
	if strings.Contains(texto, urlPrivacidad) {
		return true
	}
	return afirmaSecuencia(texto, [][]string{
		{"aceptas", "politicas", "proteccion", "datos"},
		{"aceptas", "nuestras", "politicas"},
		{"confirmes", "aceptas", "politicas"},
	}, 4)
}

// Las formas de decir sí y no CUANDO SE PREGUNTÓ POR TEXTO. Deliberadamente cortas: son las que
// no pueden significar otra cosa. "ok", "listo" y "dale" no están, y no por olvido.
var (
	afirmacionesSimples = map[string]bool{
		"si": true, "sii": true, "siii": true, "sip": true, "sisi": true,
		"si quiero": true, "si por favor": true, "si porfa": true, "si acepto": true,
		"acepto": true, "yes": true, "afirmativo": true,
	}
	negacionesSimples = map[string]bool{
		"no": true, "nop": true, "no gracias": true, "no quiero": true,
		"no acepto": true, "negativo": true, "mejor no": true,
	}
)

// mensajeTrasAceptar responde al "Sí, acepto" MIRANDO en qué punto está el pedido, en vez de
// suponer que la cédula todavía no llegó.
//
// EL BOTÓN SE QUEDA EN LA PANTALLA DEL CLIENTE. Eso es lo que nadie tuvo en cuenta al escribir
// la respuesta quemada. El cliente ve el menú, lo ignora, sigue por otro camino —manda su cédula,
// pregunta el precio, comparte la ubicación— y minutos después toca el botón, que seguía ahí.
// Para entonces el flujo ya avanzó.
//
// El 20/09 a las 08:31 Jessica (593968179884) mandó su cédula a las 08:31:19, el bot le pidió el
// nombre, y a las 08:31:22 tocó "Sí, acepto": el bot le pidió la cédula OTRA VEZ. Ella contestó
// con su nombre —a la pregunta de hacía 12 segundos— y a partir de ahí los dos fueron
// desfasados. Cuatro turnos cruzados seguidos, todos por esta frase.
//
// Pasó en 9 de los 15 consentimientos de producción. El peor: Carlos (21/09 09:07:42), con el
// pedido YA registrado, recibió "¿me compartes tu número de cédula?" — que le hace dudar de si
// su pedido existe.
func (a *Agent) mensajeTrasAceptar(from string) string {
	perfil, hayPerfil := a.store.GetProfile(from)
	tieneCedula := hayPerfil && strings.TrimSpace(perfil.Identificacion) != ""

	if !tieneCedula {
		// El caso normal: aceptó y ahora sí toca pedírsela.
		return "¡Gracias! 🙌 Ahora sí, ¿me compartes tu número de cédula?"
	}
	// Ya la tenía. Lo que corresponde depende de hasta dónde llegó el pedido.
	if a.tienePedidoVivo(from) {
		// Lo que necesita oír es que su pedido sigue en pie, no una petición de datos.
		return "¡Gracias por confirmarlo! 🙌 Tu pedido sigue en marcha, no tienes que hacer nada más 😊"
	}
	return "¡Gracias! 🙌 Ya tengo tus datos, seguimos con tu pedido."
}

// consentimientoNiega es la COMPUERTA: dice si a este cliente NO se le pueden tratar los datos
// personales, sea porque se negó o porque todavía no ha respondido.
//
// LOS TRES ESTADOS Y QUÉ HACE CADA UNO:
//
//	aceptó                      → pasa
//	se negó                     → bloquea (siempre fue así)
//	se le preguntó y no responde → BLOQUEA (esto es lo que faltaba)
//	nunca se le preguntó         → pasa
//
// El tercero es el agujero que producción encontró el 21/09. Antes solo se miraba la negativa
// explícita, así que con el menú enviado y sin responder las herramientas seguían abiertas: a las
// 09:06 Carlos (593986140905) recibió el menú, lo ignoró, escribió su cédula suelta y a las
// 09:07:26 su pedido estaba registrado. El consentimiento se grabó DIECISÉIS SEGUNDOS DESPUÉS.
//
// El silencio no es un sí. Si se le preguntó, hay que esperar la respuesta.
//
// El cuarto caso sigue pasando y es igual de deliberado: al cliente de siempre el bot nunca le
// pide la cédula —ya tiene su perfil—, así que nunca llega al menú y no puede quedar bloqueado
// por algo que no se le preguntó. Es lo que se pidió: "a los que ya están registrados no podemos
// hacer nada, deben continuar igual".
func (a *Agent) consentimientoNiega(from string) bool {
	if c, hay := a.store.GetConsentimiento(from); hay {
		return !c.Acepta
	}
	// Sin respuesta registrada: bloquea solo si se le llegó a preguntar.
	return a.store.ConsentimientoPendiente(from)
}

// instruccionSinConsentimiento es lo que se le dice AL MODELO cuando una herramienta se bloquea.
// No lo ve el cliente: el modelo lo lee y redacta con sus palabras.
//
// Distingue los dos motivos porque llevan a mensajes opuestos. A quien se NEGÓ hay que
// despedirlo con amabilidad; a quien solo no ha contestado hay que RECORDARLE que responda —
// decirle "no aceptaste" a alguien que simplemente no vio el menú es acusarlo de algo que no
// hizo, y encima cierra una venta que todavía estaba viva.
func (a *Agent) instruccionSinConsentimiento(from, queNoSeHizo string) string {
	if _, respondio := a.store.GetConsentimiento(from); !respondio {
		return "El cliente TODAVÍA NO ha respondido al menú de protección de datos: " + queNoSeHizo +
			" y NO se guardó ningún dato suyo. NO le pidas la cédula ni otros datos personales. " +
			"Recuérdale con amabilidad que para continuar necesitas que responda si acepta las " +
			"políticas, con los botones que ya le enviaste. NO le digas que se negó: no lo hizo."
	}
	return "El cliente NO autorizó el tratamiento de sus datos: " + queNoSeHizo + " y NO se guardó " +
		"ningún dato suyo. Explícale con amabilidad que sin esa autorización no puedes tomarle el " +
		"pedido por aquí, y ofrécele el teléfono de atención."
}

// sincronizarConsentimiento registra en el backend, POR CÉDULA, el consentimiento que hasta ahora
// vivía en el bot atado al teléfono. Es el puente entre los dos momentos: se acepta antes de
// tener la cédula, y se consolida cuando llega.
//
// Se llama al obtener la cédula del cliente. Si el backend falla, el consentimiento sigue válido
// en el bot (Sincronizado queda en false) y se reintenta la próxima vez: perder el registro
// remoto no puede invalidar un permiso que el cliente sí dio.
//
// Una NEGATIVA nunca se sincroniza, y no por olvido: quien se niega no da su cédula, así que no
// hay llave con la que registrarla, y guardar los datos de quien negó el permiso para tratarlos
// sería lo contrario de lo que pidió.
func (a *Agent) sincronizarConsentimiento(from, identificacion string) {
	identificacion = strings.TrimSpace(identificacion)
	if identificacion == "" {
		return
	}
	c, hay := a.store.GetConsentimiento(from)
	if !hay || !c.Acepta || c.Sincronizado {
		return
	}
	if err := a.gr.AceptarProteccionDatos(identificacion); err != nil {
		log.Printf("[consentimiento] %s: no se pudo registrar en el backend la aceptación de %s "+
			"(%v); sigue válida en el bot y se reintentará", from, identificacion, err)
		return
	}
	c.Sincronizado = true
	a.store.SetConsentimiento(from, c)
	log.Printf("[consentimiento] %s: aceptación registrada en el backend para la cédula %s ✔",
		from, identificacion)
}
