// HORAS SUGERIDAS COMO BOTONES, calculadas en CÓDIGO.
//
// Pedido del dueño (18/09): "revisar todos los flujos y ver qué cosas pueden ir como opciones,
// así le evitamos al cliente que escriba algo y empezamos a sugerir cosas para que solo dé click".
//
// La hora de una entrega agendada era el último punto grande donde el cliente tiene que escribir.
// Y es el peor de todos para escribir: la gente pone "6h30", "18:30 pm", "a las siete", y cada
// formato es una oportunidad de que el bot no entienda. El 05/09 una clienta dijo "6h30" y luego
// "18:30 pm"; el bot le pidió confirmar la hora TRES veces y la programación no se creó.
//
// ═══ POR QUÉ ESTO NO CONTRADICE LA REGLA "NUNCA OFREZCAS HORAS COMO OPCIONES" ═══
//
// Esa regla (behavior.md §9) existe por un incidente real: el 02/09 un cliente compartió su
// ubicación a las 22:49 y el bot le agendó una entrega para las 06:00 que él NUNCA pidió. Como era
// cliente conocido, todos los datos salían del perfil y la hora se la inventó el modelo. Al día
// siguiente le llegó la confirmación de una entrega fantasma.
//
// Lo que esa regla protege es que **EL MODELO no invente una hora**. Y eso sigue igual de
// prohibido: el modelo no elige nada aquí.
//
// Este menú lo arma el CÓDIGO a partir de dos hechos verificables —el horario configurado del
// negocio y el reloj— y la hora que sale de él es la que el cliente TOCÓ. Es exactamente la misma
// diferencia que ya distingue al resto del bot: el modelo no decide la cobertura (lo hace la
// geocerca), no decide la dirección (la arma el código con el texto guardado), y no decide la
// hora. Un botón que el cliente pulsa no es una hora inventada: es una hora elegida.
//
// La opción "Otra hora" se queda SIEMPRE, así que no se pierde nada: quien quiera las 16:45 las
// sigue pudiendo escribir, y el parser tolerante que ya existe la entiende igual.
package agent

import (
	"fmt"
	"time"
)

// BotonOtraHora deja escapar del menú al que quiera una hora que no está sugerida. Sin esta
// salida, el menú sería una jaula: el negocio atiende 12 horas y aquí solo caben unas pocas.
const BotonOtraHora = "Otra hora"

// margenParaPrepararse es lo mínimo que tiene que faltar para una hora sugerida. Sin esto se le
// ofrecería al cliente una entrega "a las 15:00" cuando son las 14:58, que nadie puede cumplir.
const margenParaPrepararse = 45 * time.Minute

// maxHorasSugeridas es cuántas se ofrecen. WhatsApp admite 10 filas en una lista; se dejan 4 más
// "Otra hora" porque una lista larga de horas se lee peor que escribir la hora directamente —el
// menú deja de ser un atajo y se vuelve otra decisión.
const maxHorasSugeridas = 4

// horasSugeridas calcula las próximas horas en punto que el negocio puede cumplir, a partir de
// `ahora` y del horario configurado. Devuelve los títulos de los botones, ya listos para el menú.
//
// Si hoy ya no cabe ninguna (es tarde), sigue con las primeras de MAÑANA: quien escribe a las
// 21:00 quiere que le llegue mañana temprano, y ofrecerle una lista vacía sería inútil.
func (a *Agent) horasSugeridas(ahora time.Time) []string {
	ini, fin := parseHoraHHMM(a.cfg.BotHorarioInicio), parseHoraHHMM(a.cfg.BotHorarioFin)
	if ini < 0 || fin < 0 || ini >= fin {
		return nil // configuración inválida: mejor sin menú que con horas imposibles
	}

	var horas []string
	// Primero lo que queda de HOY, luego MAÑANA si hace falta llenar.
	for _, dia := range []struct {
		etiqueta string
		fecha    time.Time
	}{
		{"hoy", ahora},
		{"mañana", ahora.Add(24 * time.Hour)},
	} {
		for m := ini; m < fin && len(horas) < maxHorasSugeridas; m += 60 {
			momento := time.Date(dia.fecha.Year(), dia.fecha.Month(), dia.fecha.Day(),
				m/60, m%60, 0, 0, zonaEcuador)
			// Solo las que de verdad se pueden cumplir.
			if momento.Sub(ahora) < margenParaPrepararse {
				continue
			}
			horas = append(horas, fmt.Sprintf("%s %02d:%02d", dia.etiqueta, m/60, m%60))
		}
		if len(horas) >= maxHorasSugeridas {
			break
		}
	}
	return horas
}

// OpcionesDeHora arma el menú completo: las horas sugeridas más la salida "Otra hora".
// Devuelve nil si no hay ninguna sugerencia posible (el cliente escribirá la hora, como antes).
func (a *Agent) OpcionesDeHora(ahora time.Time) []string {
	horas := a.horasSugeridas(ahora)
	if len(horas) == 0 {
		return nil
	}
	return append(horas, BotonOtraHora)
}

// horaDeBoton traduce el título de un botón ("mañana 09:00") a la hora "HH:MM" y si es para el día
// siguiente. ok=false si el texto no es uno de estos botones.
//
// Se reutiliza extraerHora para el "HH:MM", así que un cambio de formato en los botones no rompe
// la lectura mientras la hora siga siendo reconocible.
func horaDeBoton(texto string) (hora string, esManana bool, ok bool) {
	normalizado := normalizarRespuesta(texto)
	if normalizado == normalizarRespuesta(BotonOtraHora) {
		return "", false, false
	}
	// "manana" ya viene sin tilde de normalizarRespuesta.
	esManana = len(normalizado) >= 6 && normalizado[:6] == "manana"
	esHoy := len(normalizado) >= 3 && normalizado[:3] == "hoy"
	if !esManana && !esHoy {
		return "", false, false
	}
	if h := extraerHora(texto); h != "" {
		return h, esManana, true
	}
	return "", false, false
}
