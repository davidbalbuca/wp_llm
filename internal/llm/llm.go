// Package llm aísla al agente del proveedor de modelo. Hoy hay dos: Gemini, el de siempre,
// y Anthropic (Claude), que se elige con LLM_PROVIDER para comparar cómo atiende cada uno
// sin tocar el resto del bot ni borrar la configuración del otro.
//
// Los tipos de google.golang.org/genai se usan como VOCABULARIO COMÚN entre el agente y los
// proveedores en vez de inventar unos propios. No es comodidad: el historial persistido, las
// diez herramientas declaradas y todo el bucle de agent.go ya hablan ese idioma, y traducirlo
// entero para probar un segundo modelo significaría reescribir el camino que hoy atiende a
// clientes reales. Cada proveedor traduce hacia y desde su propio formato de puertas adentro.
package llm

import (
	"context"

	"google.golang.org/genai"
)

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
	Generate(ctx context.Context, system string, history []*genai.Content, tools []*genai.Tool) (Response, error)
	// Nombre identifica al proveedor en los logs ("gemini", "anthropic").
	Nombre() string
	// Modelo es el identificador exacto del modelo en uso, para los logs.
	Modelo() string
}
