// Package llm aísla al agente del proveedor de modelo: Gemini (default) y Anthropic (Claude),
// elegibles con LLM_PROVIDER sin tocar el resto del bot.
//
// Los tipos de google.golang.org/genai se usan como VOCABULARIO COMÚN entre agente y proveedores:
// el historial persistido, las herramientas y todo agent.go ya hablan ese idioma, y reescribirlo
// para un segundo modelo tocaría el camino que atiende clientes reales. Cada proveedor traduce a
// su propio formato de puertas adentro.
package llm

import (
	"context"

	"google.golang.org/genai"
)

// System es el prompt de sistema partido en fijo + volátil. La división existe solo por el
// cacheo de Anthropic (cobra 0.1x el texto ya visto, pero solo si el principio del prompt es
// idéntico byte a byte; antes la hora iba pegada al bloque y lo invalidaba cada minuto). El
// modelo recibe EXACTAMENTE el mismo texto: Estatico + Volatil en ese orden.
type System struct {
	// Estatico es la parte fija: las reglas del bot y la informacion del servicio.
	Estatico string
	// Volatil es lo que cambia en cada mensaje: datos del cliente, hora, pedido en curso.
	Volatil string
}

// Completo devuelve el prompt entero, tal como se armaba antes de partirlo.
func (s System) Completo() string { return s.Estatico + s.Volatil }

// Response es un turno del modelo: o trae texto para el cliente, o trae llamadas a
// herramientas que el agente debe ejecutar.
type Response struct {
	// Text es la respuesta en texto (vacía si el modelo prefirió llamar herramientas).
	Text string
	// Calls son las herramientas que el modelo quiere ejecutar, en orden.
	Calls []*genai.FunctionCall
	// Content es ese mismo turno en formato genai, para reinyectarlo al historial antes de
	// mandarle los resultados. Es nil cuando el modelo no devolvió nada utilizable.
	Content *genai.Content
}

// Provider es lo único que el agente necesita de un modelo.
type Provider interface {
	// Generate manda el prompt de sistema, el historial y las herramientas, y devuelve el
	// turno del modelo.
	Generate(ctx context.Context, system System, history []*genai.Content, tools []*genai.Tool) (Response, error)
	// Nombre identifica al proveedor en los logs ("gemini", "anthropic").
	Nombre() string
	// Modelo es el identificador exacto del modelo en uso, para los logs.
	Modelo() string
}
