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
	// Si ya se le mandó el menú y no ha contestado, no se le manda otro: repetir el mismo menú es
	// exactamente lo que behavior.md §5 prohíbe, y pasó en producción tres veces seguidas.
	if a.store.ConsentimientoPendiente(from) {
		log.Printf("[consentimiento] %s tiene el menú pendiente; no se repite", from)
		return "Para seguir necesito que me confirmes si aceptas nuestras políticas de " +
			"protección de datos 🙏 Puedes revisarlas aquí: " + urlPrivacidad
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

	switch {
	case respuestasAfirmativas[respuesta]:
		log.Printf("[consentimiento] %s ACEPTÓ las políticas de datos", from)
		a.store.ClearConsentimientoPendiente(from)
		a.store.SetConsentimiento(from, conversation.Consentimiento{
			Acepta: true, Fecha: time.Now(),
		})
		// Se sincroniza con el backend en cuanto haya cédula: aquí todavía no la hay.
		msg := "¡Gracias! 🙌 Ahora sí, ¿me compartes tu número de cédula?"
		a.store.AppendUser(from, texto)
		a.store.AppendModel(from, msg)
		return msg, true

	case respuestasNegativas[respuesta]:
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

// consentimientoNiega es la COMPUERTA: dice si este cliente tiene una negativa registrada, en
// cuyo caso ninguna herramienta puede tratar sus datos personales.
//
// Solo bloquea con una negativa EXPLÍCITA, no con la ausencia de respuesta. Es deliberado: los
// clientes que ya estaban registrados de antes tienen que seguir pidiendo igual, como se pidió
// ("a los que ya están registrados no podemos hacer nada, deben continuar igual"). A ellos el bot
// nunca les pide la cédula —ya tiene su perfil—, así que nunca llegan al menú.
func (a *Agent) consentimientoNiega(from string) bool {
	c, hay := a.store.GetConsentimiento(from)
	return hay && !c.Acepta
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
