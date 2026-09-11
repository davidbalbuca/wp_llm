// Candado de COBERTURA: el modelo no puede decirle a un cliente que no llegamos a su zona.
//
// El caso (Israel, 11/09): preguntó "¿al barrio La Gloria, por el ex CREA, llegan?". El bot
// respondió "déjame revisar... La Gloria no está en nuestras zonas de cobertura" y el cliente
// se despidió diez minutos después. Las dos frases eran falsas: no revisó nada (no existe
// herramienta para consultar un nombre de lugar) y La Gloria es un barrio de Cuenca, donde sí
// atendemos. El prompt le pedía buscar el lugar en una lista de PARROQUIAS; un barrio nunca
// está en esa lista, así que el error caía siempre del lado caro: rechazar.
//
// La cobertura la decide UNA sola cosa: la geocerca del backend sobre las COORDENADAS que el
// cliente comparte (checkCoverage, en cmd/bot). Este candado impide que el modelo se adelante
// a esa decisión con una negativa basada en un nombre. Misma filosofía que el resto: no se le
// pide al modelo que se acuerde de una regla, se comprueba el resultado.
//
// Solo se vigila la NEGATIVA, no el "sí": afirmar cobertura de más no pierde al cliente y el
// pedido igual se valida por coordenadas antes de registrarse (fueraDeCobertura corta ahí).
package agent

import (
	"log"
	"strings"

	"wp-llm-gas/internal/georoutes"
)

// niegaCobertura detecta que el texto le está diciendo al cliente que NO llegamos a su zona.
// Usa afirmaSecuencia (tolera palabras intercaladas y descarta negaciones) por lo mismo que
// los demás candados: el modelo redacta distinto cada vez.
func niegaCobertura(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"no", "esta", "en", "cobertura"},
		{"no", "esta", "en", "nuestras", "zonas"},
		{"no", "esta", "dentro", "de", "cobertura"},
		{"no", "llegamos"}, {"no", "tenemos", "cobertura"},
		{"no", "hay", "cobertura"}, {"no", "cubrimos"},
		{"fuera", "de", "nuestra", "cobertura"}, {"fuera", "de", "cobertura"},
		{"lamentablemente", "no", "llegamos"},
		{"aun", "no", "llegamos"}, {"todavia", "no", "llegamos"},
	}, 3)
}

// mensajeCoberturaEnPositivo redacta la respuesta correcta: dice dónde SÍ atendemos (con las
// zonas que informa el backend, nunca quemadas) y pide la ubicación para confirmar.
func mensajeCoberturaEnPositivo(zonas []georoutes.ZonaCobertura) string {
	base := "¡Claro que sí! 😊 "
	if texto := zonasEnTexto(zonas); texto != "" {
		base += "Atendemos en " + texto + ". "
	}
	return base + "Para confirmarte si llegamos justo a tu dirección, compárteme tu ubicación " +
		"por WhatsApp 📎 y lo verifico al instante."
}

// zonasEnTexto arma "Azuay (Baños, Bellavista y más)" con lo que informe el backend. Devuelve
// "" si no hay zonas: en ese caso el mensaje sale sin nombrar ninguna, en vez de inventarla.
func zonasEnTexto(zonas []georoutes.ZonaCobertura) string {
	partes := make([]string, 0, len(zonas))
	for _, z := range zonas {
		if strings.TrimSpace(z.Zona) == "" {
			continue
		}
		texto := z.Zona
		if len(z.Parroquias) > 0 {
			ejemplos := z.Parroquias
			if len(ejemplos) > 2 {
				ejemplos = ejemplos[:2]
			}
			texto += " (" + strings.Join(ejemplos, ", ") + " y más)"
		}
		partes = append(partes, texto)
	}
	return strings.Join(partes, ", ")
}

// revisarNegativaDeCobertura reemplaza la respuesta del modelo cuando niega cobertura sin que
// el sistema lo haya verificado por coordenadas en este turno.
//
// La negativa LEGÍTIMA no pasa por aquí: cuando el cliente comparte su ubicación y cae fuera
// de zona, quien le responde es fueraDeCobertura (cmd/bot) y el turno del modelo ni siquiera
// llega a ejecutarse. Así que una negativa en la respuesta del modelo es, por construcción,
// una deducción suya a partir de un nombre — justo lo que no puede hacer.
func (a *Agent) revisarNegativaDeCobertura(from, reply string) string {
	if !niegaCobertura(reply) {
		return reply
	}
	// Excepción: si a ESTE cliente ya se le verificó la ubicación por coordenadas y cayó fuera
	// de zona, su negativa es VERDAD y tiene que poder repetirla ("¿de verdad no llegan?").
	// Taparla lo devolvería al bucle de pedirle el pin una y otra vez para darle la misma
	// respuesta — el caso de Ambato del 08/09. La marca se limpia si comparte otra ubicación.
	if a.store.FueraDeCoberturaVerificado(from) {
		return reply
	}
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		// Sin catálogo no se puede nombrar la zona, pero la negativa igual hay que taparla:
		// es lo que pierde al cliente.
		log.Printf("[cobertura] %s: el modelo negó cobertura sin verificar y no hay catálogo; se responde en positivo", from)
		return mensajeCoberturaEnPositivo(nil)
	}
	log.Printf("[cobertura] %s: el modelo negó cobertura por el NOMBRE de un lugar, sin verificar "+
		"coordenadas; se reemplaza por pedir la ubicación", from)
	return mensajeCoberturaEnPositivo(contexto.Zonas)
}
