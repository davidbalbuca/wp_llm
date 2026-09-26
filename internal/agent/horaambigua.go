// UNA HORA AMBIGUA SE PREGUNTA, NO SE ADIVINA.
//
// Decisión del dueño (25/09), tras el caso 593986074612: el cliente pidió su gas "a las 7 de la
// noche" y quedó agendado a las 07:00 —doce horas antes— porque el ajuste de 12h solo miraba
// "pm"/"am" escritos. Eso ya está resuelto (ver franjaDelDia en programado.go).
//
// Pero queda el caso sin marcador: "a las 7", a secas. Ahí el bot NO tiene forma de saber si son
// las 07:00 o las 19:00, y con el horario del negocio (07:00–19:00) LAS DOS SON VÁLIDAS: no se
// puede desempatar por horario. Adivinar cuesta una entrega a la hora equivocada; preguntar
// cuesta un toque.
//
// Se resuelve en CÓDIGO y no por el modelo, como el resto de los menús cerrados: la respuesta es
// un conjunto de dos opciones y el modelo no elige ninguna. Es la misma distinción de
// horasmenu.go: un botón que el cliente pulsa no es una hora inventada, es una hora elegida.
package agent

import (
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/conversation"
)

// horaAmbigua dice si el texto trae una hora de 1 a 11 SIN decir en qué mitad del día, y
// devuelve ese número. Con marcador ("7 de la noche", "7pm") no hay ambigüedad; con una hora de
// 12 en adelante ("a las 15") tampoco, porque ya viene en formato de 24 horas.
func horaAmbigua(texto string) (hora int, ambigua bool) {
	t := strings.ToLower(texto)
	// PRIMERO: ¿el cliente está hablando de una HORA? Un número suelto NO es una hora, es casi
	// siempre la respuesta a otro menú. El 26/09 este candado leyó el "1" del menú de CANTIDAD
	// ([1 2 3 4]) como "la 1", preguntó mañana/noche dos veces —el cliente insistió "1
	// cilindro!"— y acabó agendando a las 01:00, fuera del horario (caso 593964011403). Se
	// reutiliza marcaHoraria, que existe justo para esta distinción: exige "a las 1", "1 pm",
	// "1:00"… no el "1" a secas.
	if !marcaHoraria.MatchString(t) {
		return 0, false
	}
	if tarde, manana := franjaDelDia(t); tarde || manana {
		return 0, false
	}
	hhmm := extraerHora(texto)
	if hhmm == "" {
		return 0, false
	}
	h := parseHoraHHMM(hhmm) / 60
	if h < 1 || h > 11 {
		return 0, false
	}
	// "18:30" no es ambiguo aunque empiece por 1: si dijo los minutos, dijo la hora exacta.
	if strings.Contains(t, ":") {
		return 0, false
	}
	return h, true
}

// opcionesDeHoraAmbigua son los dos botones. Llevan la hora en el texto para que el cliente vea
// lo que elige y para que la respuesta se pueda leer sin estado extra.
func opcionesDeHoraAmbigua(h int) []string {
	return []string{
		fmt.Sprintf("%d de la mañana", h),
		fmt.Sprintf("%d de la noche", h),
	}
}

// preguntarPorLaHoraAmbigua manda el menú de dos opciones. Devuelve true si salió: entonces el
// turno ya está resuelto y el llamador no debe mandar texto además.
func (a *Agent) preguntarPorLaHoraAmbigua(t *turno, from string, h int) bool {
	cuerpo := fmt.Sprintf("Para no equivocarme 😊 ¿te refieres a las %d de la mañana o a las %d "+
		"de la noche?", h, h)
	opciones := opcionesDeHoraAmbigua(h)
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[hora-ambigua] %s: el menú falló (%v); lo atiende el modelo", from, err)
		return false
	}
	log.Printf("[hora-ambigua] %s dijo \"%d\" sin decir mañana o noche; se le pregunta", from, h)
	t.menuSent = true
	t.lastMenuText = cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	a.store.AppendModel(from, t.lastMenuText)
	return true
}

// ResponderHoraAmbigua resuelve el toque en uno de los dos botones: anota la hora elegida en la
// ficha y deja que el flujo de programación siga su curso.
//
// Devuelve ("", false) si el texto no es una de las dos opciones, para que lo atienda el modelo:
// el cliente puede contestar otra cosa ("mejor a las 4") y eso es conversación, no un botón.
func (a *Agent) ResponderHoraAmbigua(from, texto string) (string, bool) {
	p, hay := a.store.GetPedidoEnCurso(from)
	if !hay {
		return "", false
	}
	resp := normalizarRespuesta(texto)
	for h := 1; h <= 11; h++ {
		opciones := opcionesDeHoraAmbigua(h)
		switch resp {
		case normalizarRespuesta(opciones[0]): // mañana
			return a.anotarHoraElegida(from, p, h, "mañana")
		case normalizarRespuesta(opciones[1]): // noche
			return a.anotarHoraElegida(from, p, h+12, "noche")
		}
	}
	return "", false
}

// anotarHoraElegida guarda la hora que el cliente TOCÓ y confirma. El flujo de programación
// sigue desde la ficha, igual que si la hubiera escrito sin ambigüedad.
func (a *Agent) anotarHoraElegida(from string, p conversation.PedidoEnCurso, h int, franja string) (string, bool) {
	hora := fmt.Sprintf("%02d:00", h)
	p.Hora = hora
	p.Flujo = conversation.FlujoProgramacion
	a.store.SetPedidoEnCurso(from, p)
	log.Printf("[hora-ambigua] %s eligió las %s (%s)", from, hora, franja)

	respuesta := fmt.Sprintf("¡Perfecto! Anotado para las %s 📅", hora)
	a.store.AppendUser(from, texto12h(h, franja))
	a.store.AppendModel(from, respuesta)
	return respuesta, true
}

// texto12h reconstruye lo que el cliente eligió, para el historial del modelo.
func texto12h(h int, franja string) string {
	if h > 12 {
		h -= 12
	}
	return fmt.Sprintf("%d de la %s", h, franja)
}
