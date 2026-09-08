// Chequeo de cobertura al recibir la ubicación (specs/cobertura-y-color-alterno.md, Fase B).
//
// La decisión del dueño (08-sep): validar la zona ANTES de seguir con el pedido. Antes se le
// pedían al cliente todos los datos y recién el registro final fallaba con "no hay
// conductores"; ahora se le dice de una, con amabilidad, y su demanda queda registrada en el
// backend (DemandaFueraDeZona) para decidir dónde expandirse.
package main

import (
	"fmt"
	"log"
	"strings"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/notify"
)

// fueraDeCobertura verifica el punto contra el backend. Devuelve true SOLO cuando el backend
// confirmó que está fuera de zona (y entonces ya respondió al cliente y avisó al grupo: el
// turno termina, el modelo ni se entera).
//
// REGLA DURA: si la consulta falla (timeout, 500), devuelve false y el flujo SIGUE como
// siempre. Un fallo nuestro no puede dejar sin pedir a un cliente que sí tiene cobertura.
// El chequeo corre SOLO cuando llega una ubicación nueva, nunca sobre la guardada.
func fueraDeCobertura(cfg config.Config, store conversation.Store, gr *georoutes.Client, from string, lat, lng float64) bool {
	res, err := gr.CheckCoverage(lat, lng, from)
	if err != nil {
		log.Printf("[cobertura] no se pudo verificar la zona de %s (%v); el flujo sigue", from, err)
		return false
	}
	if res.Cubierto {
		return false
	}

	// Fuera de zona confirmado. La demanda YA quedó grabada en el backend (checkCoverage la
	// registra con el teléfono); aquí se le responde al cliente y se avisa al grupo.
	log.Printf("[cobertura] %s está FUERA de cobertura (%f, %f)", from, lat, lng)

	msg := "Uy 😔 revisé tu ubicación y por ahora no llegamos a esa zona."
	if zonas := zonasParaTexto(store, gr); zonas != "" {
		msg += " Atendemos en " + zonas + "."
	}
	msg += " ¡Ojalá pronto podamos llegar hasta allá! 🙏"
	_ = replyClient(cfg, store, from, msg)

	// El equipo ve la demanda al instante (además de la tabla): dónde y quién.
	notify.Default.Fallo(from, conversation.NombreDe(store, from), "Cliente FUERA de cobertura",
		fmt.Sprintf("Pidió gas desde una zona sin cobertura. Ubicación: https://maps.google.com/?q=%f,%f — "+
			"quedó en DemandaFueraDeZona para el mapa de expansión. No es un error del bot.", lat, lng))
	store.LogMessage(from, "system", fmt.Sprintf("📍 fuera de cobertura: %f, %f", lat, lng))
	return true
}

// zonasParaTexto arma "AZUAY (Baños, Bellavista y más)" con lo que el backend informe.
// Best-effort: si no hay zonas disponibles, devuelve "" y el mensaje sale sin esa parte.
func zonasParaTexto(store conversation.Store, gr *georoutes.Client) string {
	hay, zonas, err := gr.GetCoverageZones()
	if err != nil || !hay || len(zonas) == 0 {
		return ""
	}
	partes := make([]string, 0, len(zonas))
	for _, z := range zonas {
		ej := z.Parroquias
		if len(ej) > 2 {
			ej = ej[:2]
		}
		if len(ej) > 0 {
			partes = append(partes, fmt.Sprintf("%s (%s y más)", z.Zona, strings.Join(ej, ", ")))
		} else {
			partes = append(partes, z.Zona)
		}
	}
	return strings.Join(partes, "; ")
}
