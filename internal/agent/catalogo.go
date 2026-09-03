// Helpers puros del agente: mapear el color elegido a IDs del catálogo, armar la sección
// "INFORMACIÓN DEL SERVICIO" del prompt, y utilidades de parseo (hora, números, cobertura).
// Sin estado ni efectos: funciones de apoyo que agent.go y el flujo de pedido usan.
package agent

import (
	"fmt"
	"math"
	"strings"
	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/georoutes"
)

// coberturaMarkers son fragmentos (en minúsculas) de los mensajes que el backend devuelve
// cuando no hay conductor asignable por zona/cercanía. Se comparan contra el error para
// distinguir "sin cobertura" (negocio) de un fallo técnico real.
var coberturaMarkers = []string{
	"no existen conductores",
	"no hay conductores",
	"fuera de la zona",
	"fuera de zona",
	"fuera de cobertura",
	"sin cobertura",
	"no se encontraron conductores",
	"no existen conductores en el sector",
}

// esFalloDeCobertura indica si el mensaje de error del backend corresponde a "no hay
// repartidores en la zona / fuera de cobertura" en lugar de un error técnico.
func esFalloDeCobertura(mensaje string) bool {
	m := strings.ToLower(mensaje)
	for _, marker := range coberturaMarkers {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}

// parseHoraHHMM convierte "HH:MM" a minutos del día (-1 si es inválida).
func parseHoraHHMM(s string) int {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

// findProductByColor busca en el catálogo el producto cuyo listado de colores/marcas
// contiene el color pedido (comparación sin distinguir mayúsculas/espacios).
func findProductByColor(products []georoutes.Product, colorNombre string) (georoutes.Product, georoutes.Color, bool) {
	objetivo := strings.ToLower(strings.TrimSpace(colorNombre))
	for _, producto := range products {
		for _, color := range producto.Colores {
			if strings.ToLower(strings.TrimSpace(color.Nombre)) == objetivo {
				return producto, color, true
			}
		}
	}
	return georoutes.Product{}, georoutes.Color{}, false
}

// availableColors devuelve la lista de colores/marcas disponibles, para re-preguntar.
func availableColors(products []georoutes.Product) string {
	var nombres []string
	visto := map[string]bool{}
	for _, producto := range products {
		for _, color := range producto.Colores {
			if !visto[color.Nombre] {
				visto[color.Nombre] = true
				nombres = append(nombres, color.Nombre)
			}
		}
	}
	if len(nombres) == 0 {
		return "(sin colores configurados)"
	}
	return strings.Join(nombres, ", ")
}

// defaultPaymentID elige la forma de pago por defecto: "efectivo" si existe, o la primera.
func defaultPaymentID(payments []georoutes.Payment) (int, bool) {
	for _, pago := range payments {
		if strings.Contains(strings.ToLower(pago.Nombre), "efectivo") {
			return pago.ID, true
		}
	}
	if len(payments) > 0 {
		return payments[0].ID, true
	}
	return 0, false
}

// str convierte de forma segura un valor de args (any) a string.
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toInt convierte un valor de args a entero. Los números JSON llegan como float64.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return int(math.Trunc(n))
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// renderServiceInfo arma la sección "INFORMACIÓN DEL SERVICIO" con el catálogo dinámico
// del backend (productos con colores/marcas y precio, y formas de pago). Si no hay
// catálogo disponible, devuelve una nota para que el agente derive en lugar de inventar.
func renderServiceInfo(contexto *catalog.Context, disponible bool) string {
	if !disponible || contexto == nil {
		return "La información del servicio (productos, precios) no está disponible en este momento. " +
			"Si el cliente pregunta por estos datos y no los tienes con certeza, discúlpate y deriva al dueño."
	}

	var texto strings.Builder
	negocio := contexto.Business
	if negocio.Nombre != "" {
		fmt.Fprintf(&texto, "Negocio: %s\n", negocio.Nombre)
	}
	if negocio.Telefono != "" {
		fmt.Fprintf(&texto, "Teléfono/WhatsApp: %s\n", negocio.Telefono)
	}
	if negocio.Horario != "" {
		fmt.Fprintf(&texto, "Horario de atención: %s\n", negocio.Horario)
	}
	if negocio.TiemposEntrega != "" {
		fmt.Fprintf(&texto, "Tiempos de entrega: %s\n", negocio.TiemposEntrega)
	}
	if negocio.Nombre != "" || negocio.Telefono != "" || negocio.Horario != "" {
		texto.WriteString("\n")
	}

	if len(contexto.Products) > 0 {
		texto.WriteString("Productos disponibles (con colores/marcas y precio):\n")
		for _, producto := range contexto.Products {
			// PRECIO TOTAL por unidad (unitario + envío + instalación + servicio): es lo que
			// el cliente paga de verdad. NUNCA cotizar solo el unitario.
			fmt.Fprintf(&texto, "- %s: $%.2f por cilindro (incluye envío, instalación y servicio)",
				producto.Nombre, producto.PrecioTotal())
			if len(producto.Colores) > 0 {
				var nombres []string
				for _, color := range producto.Colores {
					nombres = append(nombres, color.Nombre)
				}
				fmt.Fprintf(&texto, " | colores/marcas: %s", strings.Join(nombres, ", "))
			}
			texto.WriteString("\n")
		}
	}

	if negocio.FormasPago != "" {
		fmt.Fprintf(&texto, "\nFormas de pago: %s\n", negocio.FormasPago)
	}
	if negocio.Seguridad != "" {
		fmt.Fprintf(&texto, "Seguridad (fuga de gas): %s\n", negocio.Seguridad)
	}
	if negocio.Adicional != "" {
		fmt.Fprintf(&texto, "\n%s\n", negocio.Adicional)
	}

	texto.WriteString("\nPara concretar un pedido necesitas del cliente: cédula, nombre completo, " +
		"el color/marca deseado, la cantidad y su ubicación de WhatsApp (📎 → Ubicación). " +
		"NUNCA pidas correo electrónico (no se necesita).\n")
	return texto.String()
}
