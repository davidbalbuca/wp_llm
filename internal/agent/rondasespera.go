package agent

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// NO PERDER AL CLIENTE POR PREGUNTARLE UNA SOLA VEZ.
//
// Carlos (593959545411) escribió el 23/09 a las 17:45, pidió su gas, dio todo, y a las 17:57 el
// bot le dijo "no hay ningún repartidor disponible, intenta más tarde". Doce minutos desde que
// escribió; ocho desde que el pedido quedó registrado. Le preguntamos UNA vez si quería
// esperar, dijo que sí, y cuando esa espera venció lo despedimos.
//
// El cliente estaba dispuesto. Lo perdimos nosotros.
//
// Cómo queda el flujo:
//
//	ronda 0  →  "¿Deseas esperar?"        [Esperar / Programar / Cancelar]
//	ronda 1  →  "¿Seguimos buscando?"     [Esperar / Programar / Cancelar]
//	ronda 2  →  disculpas + reprogramar   [Reprogramar / Cancelar]
//
// Cada ronda dura BOT_ESPERA_RONDA_MIN (15 min por defecto, configurable). Quien acepta las dos
// veces espera hasta 45 minutos antes de que se le ofrezca reprogramar — pero NUNCA se le
// cierra la puerta sin preguntarle, que es lo que pasó con Carlos.
//
// En la última ronda desaparece "Esperar" a propósito: alguien que ya esperó media hora no
// necesita que le ofrezcamos media hora más, necesita una salida digna.

// rondasConEspera es en cuántas rondas se le puede ofrecer seguir esperando. A partir de ahí
// solo quedan reprogramar o cancelar. Dos: la primera pregunta y una insistencia.
const rondasConEspera = 2

// opcionesDeLaRonda dice qué botones se le muestran.
//
// Hasta rondasConEspera puede seguir esperando. Después ya no: se le ofrece reprogramar (que
// entra al flujo de agendado de siempre) o cancelar. Son DOS opciones y no una sola porque un
// menú sin salida no es una opción, es un embudo — y el cliente que quiere irse se va igual,
// solo que molesto.
func opcionesDeLaRonda(ronda int) []string {
	if ronda < rondasConEspera {
		return []string{"Esperar", "Programar", "Cancelar"}
	}
	return []string{"Reprogramar", "Cancelar"}
}

// cuerpoDeLaRonda arma el texto que acompaña a los botones.
//
// Los minutos que se nombran son los que de verdad va a esperar: prometer cinco y tardar quince
// es peor que decir quince desde el principio, porque el cliente cuenta el tiempo.
func cuerpoDeLaRonda(ronda, minutos int) string {
	switch {
	case ronda == 0:
		return fmt.Sprintf("Estamos buscando al chofer ideal para ti 🚚. Nuestro sistema puede "+
			"tardar hasta %s minutos en conectar con el camión más cercano en tu zona. "+
			"¿Deseas esperar?", strconv.Itoa(minutos))
	case ronda < rondasConEspera:
		return fmt.Sprintf("Seguimos sin un repartidor libre en tu zona 😕 Podemos seguir "+
			"buscando %s minutos más; a esta hora suelen ir liberándose. ¿Seguimos buscando?",
			strconv.Itoa(minutos))
	default:
		// Última ronda: se pide disculpas de verdad. Esta persona esperó por algo que no llegó.
		return "Te pedimos muchas disculpas 🙏 Hemos buscado un buen rato y no encontramos un " +
			"repartidor libre para tu zona ahora mismo.\n\nNo queremos dejarte sin tu gas: " +
			"podemos agendarlo para la hora que tú elijas y te lo llevamos apenas haya alguien " +
			"disponible. ¿Lo reprogramamos?"
	}
}

// MensajeDespedidaTrasCancelar es lo que se le dice a quien, después de esperar, elige no
// seguir. Se le agradece la paciencia y se le deja la puerta abierta: es un cliente que quiso
// comprarnos y no pudo por algo nuestro, no alguien que se arrepintió.
func MensajeDespedidaTrasCancelar() string {
	return "Entendido 🙏 Lamentamos no haber podido llevarte tu gas esta vez, y te agradecemos " +
		"la paciencia de haber esperado.\n\nCuando lo necesites escríbeme y lo intentamos de " +
		"nuevo — aquí estoy 😊"
}

// liberarRespuestaDeEspera marca que el cliente YA contestó el menú, para que la siguiente
// ronda pueda volver a preguntarle. La ronda NO se toca: contarla es el trabajo de
// ofrecerEsperaAlCliente, que es quien sabe si el menú llegó a salir.
func (a *Agent) liberarRespuestaDeEspera(from string) {
	w, ok := a.store.GetPendingWait(from)
	if !ok || !w.EsperandoRespuesta {
		return
	}
	w.EsperandoRespuesta = false
	a.store.SetPendingWait(from, w)
}

// duracionDeLaRonda es cuánto dura una ronda de espera (BOT_ESPERA_RONDA_MIN).
//
// Está aparte y no calculado dentro de la goroutine de espera porque ahí no hay forma de
// comprobarlo: el mutante que fijaba la espera en 5 minutos sobrevivía a toda la suite. Y ese
// número es justo el que le costó el pedido a Carlos el 23/09.
//
// El cero se trata como "sin configurar" y cae al default, no a una espera instantánea: un
// .env incompleto no puede convertir la espera en nada.
func duracionDeLaRonda(cfg config.Config) time.Duration {
	if cfg.EsperaRonda > 0 {
		return cfg.EsperaRonda
	}
	return 10 * time.Minute
}

// tocaOtraRonda dice si ya puede ofrecerse la ronda SIGUIENTE. Función pura —recibe los dos
// instantes y la duración— por el mismo motivo que duracionDeLaRonda: así se comprueba sin
// esperar minutos reales.
//
// EL FRENO ES EL TIEMPO, no "que el cliente haya contestado". Con lo segundo —lo único que
// había cuando la búsqueda la lleva el backend— un cliente ATENTO se castigaba a sí mismo:
// Edison (25/09) contestó "Esperar" dos veces y agotó sus TRES rondas en 56 segundos en vez de
// en los 30 minutos que le tocaban. Al contestar se limpiaba EsperandoRespuesta y, 7 segundos
// después (lo que tarda el bot en volver a preguntarle al backend), el estado seguía en
// SIN_CONDUCTOR y se le ofrecía la siguiente. Quien ignoraba el mensaje conservaba sus rondas.
//
// Los dos frenos se SUMAN, no se sustituyen: EsperandoRespuesta sigue evitando que se le repita
// el menú mientras no conteste, y este evita que contestar se lo gaste.
//
// La primera ronda (ultimaRonda cero) no espera: es el instante en que el backend acaba de decir
// que no hay nadie, y ahí hay que preguntarle ya.
func tocaOtraRonda(ultimaRonda, ahora time.Time, duracion time.Duration) bool {
	if ultimaRonda.IsZero() {
		return true
	}
	return !ahora.Before(ultimaRonda.Add(duracion))
}

// ultimaRondaDe convierte el unix guardado en la espera a un instante. El cero significa "aún no
// se le ha ofrecido ninguna ronda" y NO la medianoche de 1970: sin esto, time.Unix(0,0) daría una
// fecha lejanísima y tocaOtraRonda dejaría pasar siempre, que es justo el bug que se arregla.
func ultimaRondaDe(w conversation.PendingWait) time.Time {
	if w.UltimaRondaAt <= 0 {
		return time.Time{}
	}
	return time.Unix(w.UltimaRondaAt, 0)
}

// BORRAR LA ESPERA DEL BOT ES CERRAR LA BÚSQUEDA DEL BACKEND.
//
// Con la búsqueda en el backend, el bot solo guarda el id para poder cerrarla. Había CINCO
// caminos que borraban la espera y solo dos avisaban: cancelar y reprogramar. Los otros tres
// —rechazar el color alterno, cambiar la dirección con pedido en ruta, y cerrar el ciclo de la
// conversación— la borraban en silencio, y el backend seguía buscando repartidor para un pedido
// que el cliente ya no esperaba.
//
// El peor era el cambio de dirección: el bot cancela el pedido y lo re-registra en la ubicación
// nueva, pero la búsqueda anterior quedaba viva. El backend podía asignarle un repartidor a un
// pedido que ya no existe y mandarlo a la dirección vieja.
//
// Se resuelve con UN punto de salida en vez de recordarlo en cada sitio: quien borre la espera
// pasa por aquí, y el día que alguien agregue un camino nuevo no tiene que acordarse de nada.
// El test verifica además que no quede ningún ClearPendingWait suelto en esos archivos.
func (a *Agent) cerrarEsperaYBusqueda(from, motivo string) {
	w, hay := a.store.GetPendingWait(from)
	if hay && w.IDBusqueda > 0 {
		log.Printf("[espera] %s: se cierra la busqueda %d del backend (%s)", from, w.IDBusqueda, motivo)
		a.cerrarBusquedaBackend(from, w.IDBusqueda)
	}
	a.store.ClearPendingWait(from)
}
