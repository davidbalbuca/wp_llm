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
// Se vigilan LAS DOS CARAS. Al principio solo la negativa, con esta justificación: "afirmar
// cobertura de más no pierde al cliente y el pedido igual se valida por coordenadas antes de
// registrarse". Era falso. El 15/09 QA preguntó por nombre, sin mandar ubicación, y el bot
// confirmó cobertura en Paute, Sígsig, Gualaceo y Santa Isabel — cuatro cantones de Azuay donde
// no se opera. La geocerca efectivamente no despacha a nadie fuera de zona, pero para entonces
// el cliente ya eligió color, dio su cédula y compartió su ubicación: el rechazo llega al final,
// que es donde más cuesta. Un "no" honesto al principio es más barato que un "sí" que se cae.
package agent

import (
	"fmt"
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

// afirmaCobertura detecta que el texto le está PROMETIENDO al cliente que sí llegamos a su
// zona. Hermano de niegaCobertura, para la otra cara.
//
// Las secuencias son deliberadamente estrechas: tienen que nombrar la cobertura, no un "sí"
// cualquiera. "Sí, tenemos gas blanco" o "sí, te lo agendo" no son promesas de cobertura, y
// taparlas rompería la conversación normal. afirmaSecuencia ya descarta las negaciones, así que
// "no está cubierto" no cae aquí.
func afirmaCobertura(texto string) bool {
	// Ofrecer verificar no es prometer ("¿quieres que revise si llegamos?").
	if esOfrecimiento(normalizar(texto)) {
		return false
	}
	// Pedir la ubicación YA es la respuesta correcta: si el texto la pide, no está prometiendo
	// nada aunque diga "llegamos" ("para confirmarte si llegamos justo a tu dirección,
	// compárteme tu ubicación"). Sin esta salida el candado se comía su propio reemplazo.
	if pideLaUbicacion(texto) {
		return false
	}
	return afirmaSecuencia(texto, [][]string{
		{"esta", "cubierto"}, {"esta", "cubierta"},
		{"si", "llegamos"}, {"si", "atendemos"},
		{"esta", "dentro", "de", "nuestra", "cobertura"},
		{"esta", "dentro", "de", "cobertura"},
		{"esta", "en", "nuestra", "cobertura"},
		{"si", "tenemos", "cobertura"}, {"tenemos", "cobertura", "en"},
		{"si", "cubrimos"}, {"cubrimos", "esa", "zona"},
		{"si", "hay", "cobertura"},
	}, 3)
}

// pideLaUbicacion dice si el texto le está pidiendo al cliente que comparta su ubicación. Es la
// señal de que el modelo NO está decidiendo la cobertura: la está mandando a verificar.
func pideLaUbicacion(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"comparteme", "tu", "ubicacion"}, {"compartirme", "tu", "ubicacion"},
		{"comparte", "tu", "ubicacion"}, {"me", "compartes", "tu", "ubicacion"},
		{"envíame", "tu", "ubicacion"}, {"enviame", "tu", "ubicacion"},
		{"mandame", "tu", "ubicacion"}, {"necesito", "tu", "ubicacion"},
	}, 3)
}

// revisarPedidoDeUbicacionRedundante corrige al modelo cuando pide una ubicación que el cliente
// ACABA de compartir.
//
// Caso del 15/09 (H-02): tres pines en el mismo minuto y al segundo le contestó "compárteme tu
// ubicación". QA lo leyó como un pin perdido por mensajes simultáneos; el webhook ya serializa
// por teléfono (lockCliente), así que el pin sí se guardó — lo que falló fue la redacción. Con
// varios mensajes seguidos el modelo arrastra el turno anterior y vuelve a pedir lo que ya tiene.
//
// Pedirle dos veces lo mismo al cliente lo hace dudar de si el bot lo está escuchando, y pasa
// justo cuando está a punto de pedir. Solo se corrige con ubicación FRESCA: si es de otra
// conversación, volver a pedirla es lo correcto (ver la guardia de dirección en pedido.go).
func (a *Agent) revisarPedidoDeUbicacionRedundante(from, reply string) string {
	if !pideLaUbicacion(reply) {
		return reply
	}
	if !a.ubicacionEsDeAhora(from) {
		return reply
	}
	log.Printf("[ubicacion] %s: el modelo pidió una ubicación que ya tenía de esta conversación; "+
		"se reemplaza para no hacerle repetir el pin", from)
	return "¡Ya tengo tu ubicación, gracias! 😊 Cuéntame qué cilindro necesitas y cuántos, y te " +
		"lo despacho enseguida."
}

// confirmaElSector dice si el texto ya le está confirmando al cliente que llegamos a su zona.
// Se usa para NO duplicar la confirmación: si el modelo hizo lo que el prompt le pidió, el
// candado no tiene nada que añadir.
func confirmaElSector(texto, sector string) bool {
	norm := normalizar(texto)
	// El nombre del sector basta como señal: el prompt le pide nombrarlo, y no hay otra razón
	// para que aparezca en la respuesta.
	if sector != "" && strings.Contains(norm, normalizar(sector)) {
		return true
	}
	return afirmaSecuencia(texto, [][]string{
		{"si", "llegamos", "tu", "zona"}, {"si", "llegamos", "alla"},
		{"llegamos", "tu", "sector"}, {"si", "atendemos", "tu", "zona"},
		{"si", "tenemos", "cobertura", "tu", "zona"}, {"estas", "dentro", "nuestra", "cobertura"},
		{"buenas", "noticias", "si", "llegamos"},
	}, 3)
}

// revisarCoberturaConfirmada garantiza que el cliente se ENTERE de que sí llegamos a su zona.
//
// El caso (593939235151, 24/09): preguntó "en qué parte de cuenca da su servicio", el bot le
// pidió la ubicación para confirmárselo, el cliente la mandó... y el bot pasó directo al menú de
// colores. Nunca le dijo que sí. Compartir la ubicación es una PREGUNTA y se quedó sin responder;
// el cliente siguió el pedido sin saber si le iba a llegar.
//
// El prompt ya le pide nombrar el sector (ver COBERTURA CONFIRMADA en prompt.go), pero pedirle
// algo al modelo no es garantía —es la lección de los otros diez candados—, así que si no lo
// dijo, lo antepone el código con el nombre que devolvió la geocerca.
//
// Se hace UNA sola vez: la marca se consume al usarla, para no repetirle lo mismo en cada turno.
func (a *Agent) revisarCoberturaConfirmada(from, reply string) string {
	if strings.TrimSpace(reply) == "" {
		// Turno sin texto: o no hay nada que decir, o acabó en menú y la confirmación ya viajó
		// en su cuerpo (ver mandarMenu). En ningún caso hay que consumir la marca aquí.
		return reply
	}
	return a.conCoberturaConfirmada(from, reply)
}

// conCoberturaConfirmada antepone la confirmación de zona al mensaje que va a salir, sea el texto
// de la respuesta o el cuerpo de un menú. Consume la marca: la confirmación es de UN turno, el
// que siguió a la ubicación, y repetirla en cada mensaje sonaría a disco rayado.
func (a *Agent) conCoberturaConfirmada(from, mensaje string) string {
	sector := a.store.SectorCubierto(from)
	if sector == "" {
		return mensaje
	}
	a.store.LimpiarSectorCubierto(from)

	if confirmaElSector(mensaje, sector) {
		return mensaje // el modelo ya lo dijo, con su propio tono
	}
	log.Printf("[cobertura] %s: el modelo no confirmó la cobertura del sector %q; se antepone en código",
		from, sector)
	confirmacion := fmt.Sprintf("¡Buenas noticias! Sí llegamos a tu zona (%s) 🎉", sector)
	if strings.TrimSpace(mensaje) == "" {
		return confirmacion
	}
	return confirmacion + "\n\n" + mensaje
}

// revisarCoberturaAfirmada reemplaza la respuesta del modelo cuando PROMETE cobertura sin que
// el sistema la haya verificado por coordenadas.
//
// La afirmación legítima sí pasa: si el cliente compartió su ubicación en esta conversación, la
// geocerca ya decidió (fueraDeCobertura habría cortado el turno antes de llegar al modelo), así
// que un "sí llegamos" en ese momento es verdad y debe poder decirse.
func (a *Agent) revisarCoberturaAfirmada(from, reply string) string {
	if !afirmaCobertura(reply) {
		return reply
	}
	// Ubicación de esta conversación = geocerca ya consultada y superada. El "sí" es cierto.
	if a.ubicacionEsDeAhora(from) {
		return reply
	}
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		log.Printf("[cobertura] %s: el modelo prometió cobertura sin verificar y no hay catálogo; se pide la ubicación", from)
		return mensajeCoberturaEnPositivo(nil)
	}
	log.Printf("[cobertura] %s: el modelo prometió cobertura por el NOMBRE de un lugar, sin verificar "+
		"coordenadas; se reemplaza por pedir la ubicación", from)
	return mensajeCoberturaEnPositivo(contexto.Zonas)
}

// mensajeCoberturaEnPositivo redacta la respuesta correcta: dice dónde SÍ atendemos (con las
// zonas que informa el backend, nunca quemadas) y pide la ubicación para confirmar.
func mensajeCoberturaEnPositivo(zonas []georoutes.ZonaCobertura) string {
	base := "¡Claro que sí! 😊 "
	if texto := ZonasEnTexto(zonas); texto != "" {
		// "urbanas y rurales" es la frase que faltaba: la geocerca cubre el cantón completo, y
		// sin decirlo el cliente de una parroquia rural asume que solo se atiende el centro.
		base += "Atendemos en las parroquias urbanas y rurales de " + texto + ". "
	}
	return base + "Para confirmarte si llegamos justo a tu dirección, compárteme tu ubicación " +
		"por WhatsApp 📎 y lo verifico al instante."
}

// ejemplosPorZona es cuántas parroquias se nombran por zona EN EL MENSAJE AL CLIENTE. Seis, no
// dos: con dos, y ordenadas como las manda el backend (alfabéticamente), SIEMPRE salían las
// mismas —"BANOS, BELLAVISTA y más"— y un cliente de Sinincay o de Turi no se veía en la lista
// aunque su parroquia esté cubierta. Tampoco se recitan las 33: es un chat, no un catastro.
const ejemplosPorZona = 6

// ejemplosParaElModelo es el mismo dato para el PROMPT, y es más corto a propósito: el modelo lee
// una lista larga como un catálogo cerrado y deduce "no está => no hay cobertura" (caso La Gloria,
// 11/09). El cliente la lee para reconocer la suya. Dos lectores, dos tamaños; suben por separado.
// Ver renderCobertura en catalogo.go.
const ejemplosParaElModelo = 3

// ZonasEnTexto arma "CUENCA (BANOS, EL BATAN, LLACAO... y más)" con lo que informe el backend.
// Devuelve "" si no hay zonas: en ese caso el mensaje sale sin nombrar ninguna, en vez de
// inventarla. Exportada porque cmd/bot arma el mismo texto para el rechazo por coordenadas y
// tenerlo dos veces significaba mejorar uno y olvidar el otro.
func ZonasEnTexto(zonas []georoutes.ZonaCobertura) string {
	partes := make([]string, 0, len(zonas))
	for _, z := range zonas {
		if strings.TrimSpace(z.Zona) == "" {
			continue
		}
		texto := z.Zona
		if ejemplos := parroquiasDeMuestra(z.Parroquias, ejemplosPorZona); len(ejemplos) > 0 {
			texto += " (" + strings.Join(ejemplos, ", ")
			if len(z.Parroquias) > len(ejemplos) {
				texto += " y más"
			}
			texto += ")"
		}
		partes = append(partes, texto)
	}
	return strings.Join(partes, ", ")
}

// parroquiasDeMuestra elige n parroquias REPARTIDAS por toda la lista, no las n primeras.
//
// El backend las devuelve ordenadas alfabéticamente, así que cortar por el principio deja
// siempre el mismo puñado del arranque del alfabeto y esconde el resto. Tomando una cada k se
// recorre la lista entera y aparecen nombres de todo el rango —urbanas y rurales mezcladas—, que
// es lo que hace que el cliente reconozca la suya. Es determinista: la misma lista da siempre
// los mismos ejemplos, así que el mensaje no cambia entre turnos.
func parroquiasDeMuestra(parroquias []string, n int) []string {
	limpias := make([]string, 0, len(parroquias))
	for _, p := range parroquias {
		if p = strings.TrimSpace(p); p != "" {
			limpias = append(limpias, p)
		}
	}
	if n <= 0 || len(limpias) == 0 {
		return nil
	}
	if len(limpias) <= n {
		return limpias
	}
	muestra := make([]string, 0, n)
	// Paso en punto fijo (x1000) para repartir sin acumular error de redondeo: con 33 y n=6
	// salen los índices 0, 5, 11, 16, 22, 27 en vez de 0..5.
	paso := len(limpias) * 1000 / n
	for i := 0; i < n; i++ {
		idx := i * paso / 1000
		if idx >= len(limpias) {
			idx = len(limpias) - 1
		}
		muestra = append(muestra, limpias[idx])
	}
	return muestra
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
