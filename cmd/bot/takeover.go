package main

import (
	"log"

	"wp-llm-gas/internal/conversation"
)

// ESCRIBIRLE AL CLIENTE ES TOMAR LA CONVERSACIÓN.
//
// El panel tiene dos endpoints separados: /internal/chat-control apaga el bot, y
// /internal/send-message manda el mensaje. Estaban desacoplados, así que quien escribía desde
// el panel creía haber tomado la conversación mientras el bot seguía contestando en paralelo.
//
// El 22/09 le costó a Doris (593958615651): una persona del equipo le escribió "el conductor
// se encuentra cerca de tu domicilio, nos compartes tu ubicación", ella contestó "Estoy
// esperando", y el bot —que no sabía nada— le respondió que no había repartidores y le ofreció
// cancelar el pedido. El repartidor estaba en su puerta.
//
// Medido en la base de producción el 23-sep-2026: de 26 teléfonos que habían recibido mensajes
// de una persona, 25 seguían en modo "bot". No era un descuido ocasional de alguien que se
// olvidó de apretar el botón: era lo que pasaba SIEMPRE, porque apretarlo era un paso aparte.
//
// La regla ahora es la del mundo real: si una persona entra a hablar, el bot se calla. No hay
// que acordarse de nada. El control vuelve solo al bot tras HUMAN_TAKEOVER_TIMEOUT_MIN de
// silencio del cliente (ver el webhook), así que un pedido nuevo no queda esperando a nadie.

// loEscribioUnaPersona decide si detrás de este mensaje hay alguien de carne y hueso.
//
// Dos señales, y la segunda es la que funciona HOY sin tocar el backend:
//
//   - `automatico`: la marca explícita. Es la buena, y la que debería usar todo el que llame al
//     endpoint de aquí en adelante.
//   - `max_horas`: hoy SOLO la mandan los avisos automáticos del backend ("tu pedido fue
//     entregado"), porque son los únicos a los que les importa llegar a tiempo. Una persona
//     escribiendo en el panel nunca la manda: escribe y listo.
//
// Se usa `max_horas` como respaldo a propósito, para no modificar `georoutes` —es la app que ya
// está en producción y la regla del proyecto es no tocarla—. Si algún día un mensaje humano
// empezara a mandar `max_horas`, la marca explícita manda y esto sigue siendo correcto.
func loEscribioUnaPersona(automatico bool, maxHoras float64) bool {
	if automatico {
		return false
	}
	return maxHoras <= 0
}

// tomarChatAlEscribir apaga el bot porque una PERSONA acaba de escribirle al cliente.
func tomarChatAlEscribir(store conversation.Store, phone string) {
	if store.GetChatMode(phone) == conversation.ChatModeHuman {
		return // ya estaba tomado; no hay nada que anunciar
	}
	log.Printf("[takeover] %s: escribió una persona del equipo; el bot deja de responder", phone)
	store.SetChatMode(phone, conversation.ChatModeHuman)
}

// avisarSinTomarChat deja el chat como está: el mensaje lo mandó el SISTEMA, no una persona.
//
// Son los avisos automáticos (entrega hecha, conductor asignado). Si estos apagaran el bot, el
// cliente quedaría sin atención justo después de que le entregan el gas —que es cuando suele
// escribir para agradecer, calificar o pedir otro—, y encima sin nadie del otro lado, porque
// no hay ninguna persona detrás de ese mensaje.
func avisarSinTomarChat(store conversation.Store, phone string) {
	_ = store
	_ = phone
}
