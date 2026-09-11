// Escritura y lectura del slot PedidoEnCurso: la ficha de lo que el cliente va eligiendo.
//
// Antes, el color y la cantidad solo existían en la transcripción del chat. Cuando el código
// necesitaba saberlos —para rescatar un pedido que el modelo confirmó sin registrar— tenía que
// releer 30 mensajes y adivinarlos con heurísticas de texto ("el primer número de 1 o 2 dígitos
// después del color"). Eso funcionaba de milagro y fallaba en silencio: si el cliente cambiaba
// de opinión, o si el historial se truncaba, el rescate registraba un pedido equivocado.
//
// Ahora el dato se guarda EN EL MOMENTO en que llega, y todos lo leen del mismo sitio.
package agent

import (
	"log"
	"regexp"
	"strings"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// marcaHoraria reconoce que el cliente está hablando de una HORA, no de una cantidad:
// "18:30", "6h30", "7 pm", "a las 3", "seis y media", "y cuarto". Anclado a tokens a
// propósito: cualquier heurística de substring convierte "hola" o un punto final en una hora.
var marcaHoraria = regexp.MustCompile(`\d{1,2}\s*[:.]\s*\d{2}` + // 18:30, 18.30
	`|\d{1,2}\s*h\s*\d{0,2}\b` + // 6h30, 6h (la frontera final evita que "8 hola" cuente)
	`|\d{1,2}\s*(am|pm|a\.m|p\.m)\b` + // 7pm, 7 am — la frontera evita "1 AMarillo" = 1 AM
	`|\b(a las|para las|a la|tipo)\s+\d{1,2}` + // a las 3, para las 18
	`|\by (media|cuarto)\b`) // seis y media

// anotarDelMensaje mira EL MENSAJE ACTUAL del cliente (nunca el historial) y guarda en la ficha
// lo que reconozca. Reutiliza los parsers que ya existían para adivinar sobre el chat; la
// diferencia es que ahora se aplican a un solo mensaje, en el instante en que el cliente lo
// escribe, y el resultado se persiste.
//
// Es acumulativo: cada mensaje solo añade o corrige lo suyo. Un "mejor amarillo" pisa el color
// anterior; un mensaje que no aporta nada deja la ficha igual.
func (a *Agent) anotarDelMensaje(from, texto string) {
	if strings.TrimSpace(texto) == "" {
		return
	}
	p, _ := a.store.GetPedidoEnCurso(from)
	antes := p

	// 1. Color(es): se validan contra el catálogo, así que "el azul" solo cuenta si azul existe.
	//    Con VARIOS colores en el mensaje ("blanco y amarillo") cada uno abre su línea: antes la
	//    ficha tenía un solo Color y se quedaba con el último — así se perdió el BLANCO de David
	//    (10/09) y el bot le confirmó dos colores habiendo registrado uno.
	if contexto, ok := a.catalog.Get(); ok && contexto != nil {
		p = anotarColores(p, contexto.Products, texto)
	}

	// 2. Hora: se mira ANTES que la cantidad. "a las 7 pm" y "6h30" llevan números que no son
	//    cilindros: si se leyera la cantidad primero, el cliente terminaría con 7 o 6 tanques.
	//    Se distingue "parece una hora" de "es una hora que podemos atender": un "6h30" fuera
	//    del horario NO se guarda como hora, pero TAMPOCO puede convertirse en cantidad.
	pareceHora, hora := a.horaEnMensaje(texto)
	if hora != "" {
		p.Hora = hora
		p.Flujo = conversation.FlujoProgramacion
	}

	// 3. Cantidad: solo si el mensaje NO hablaba de una hora, ya hay un color elegido, y el
	//    mensaje es CORTO (la respuesta a "¿cuántos?": "2", "2 porfa", "quiero 3"). Una frase
	//    larga con un número dentro no es una cantidad: "van 4 días con este problema" en un
	//    reclamo dejaba una ficha de 4 cilindros lista para registrar, y el prompt le decía al
	//    modelo "FALTA: nada, llama ya a la herramienta" (encontrado en la revisión del 07/09).
	//
	//    Con varias líneas abiertas, "1 de blanco" lleva la cantidad a la línea de ESE color;
	//    un número solo ("2") va a la línea que se está eligiendo ahora.
	if !pareceHora && len(strings.Fields(texto)) <= 4 {
		if n := primerNumero(texto); n >= 1 && n <= 20 {
			if colores := coloresDelMensaje(a, texto); len(colores) == 1 {
				p = ponerCantidad(p, colores[0], n)
			} else if len(colores) == 0 && p.Color != "" {
				p.Cantidad = n
			}
		}
	}

	if p.Flujo == "" {
		p.Flujo = conversation.FlujoInmediato
	}
	// 4. Solo se escribe si algo cambió: evita tocar el store en cada mensaje de charla.
	if p.Color == antes.Color && p.Cantidad == antes.Cantidad && p.Hora == antes.Hora &&
		p.Flujo == antes.Flujo && mismosItems(p.Items, antes.Items) {
		return
	}
	log.Printf("[encurso] %s: color=%q cantidad=%d items=%v hora=%q flujo=%s", from, p.Color, p.Cantidad, p.Items, p.Hora, p.Flujo)
	a.store.SetPedidoEnCurso(from, p)
}

// coloresDelMensaje devuelve los colores del catálogo mencionados en el texto (en su orden de
// aparición, sin repetidos), o nada si el catálogo no está disponible.
func coloresDelMensaje(a *Agent, texto string) []string {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return nil
	}
	return coloresEnTexto(contexto.Products, texto)
}

// marcasReemplazo delatan un CAMBIO de elección ("mejor amarillo", "cambia a azul"): el color
// nuevo pisa la línea en curso, como siempre hizo la ficha de un solo color.
var marcasReemplazo = []string{"mejor", "cambia", "cambio", "cambialo", "cambiar", "sino", "en vez", "ya no"}

// marcasAdicion delatan una SUMA ("también un amarillo", "y otro azul"): la línea en curso, si
// está completa, se cierra y el color nuevo abre otra.
var marcasAdicion = []string{"tambien", "ademas", "otro", "otra", "aparte", "agrega", "agregame", "suma", "y"}

func contieneMarca(normalizado string, marcas []string) bool {
	campos := " " + normalizado + " "
	for _, m := range marcas {
		if strings.Contains(campos, " "+m+" ") {
			return true
		}
	}
	return false
}

// anotarColores aplica al pedido en curso los colores del catálogo que trae el mensaje. Las
// reglas, en orden:
//
//   - VARIOS colores en un mismo mensaje ("blanco y amarillo") son un pedido multicolor: cada
//     color abre su línea. Antes la ficha tenía un solo Color y ganaba el último — así se
//     perdió el BLANCO de David (10/09).
//   - UN color con marca de adición ("también amarillo") y la línea en curso completa: la
//     cierra y abre otra.
//   - UN color en cualquier otro caso pisa la línea en curso (cambio de opinión, o primera
//     elección): el comportamiento de siempre.
//   - Un color que YA tiene línea no se duplica (el cliente repitiéndose, o "1 de blanco"
//     respondiendo cuántos).
//   - En una PREGUNTA ("¿y el amarillo cuánto cuesta?") no se abre ni cierra ninguna línea:
//     preguntar por un color no es pedirlo.
func anotarColores(p conversation.PedidoEnCurso, products []georoutes.Product, texto string) conversation.PedidoEnCurso {
	colores := coloresEnTexto(products, texto)
	if len(colores) == 0 || strings.ContainsAny(texto, "?¿") {
		return p
	}
	normalizado := normalizar(texto)
	reemplaza := contieneMarca(normalizado, marcasReemplazo)
	aditivo := contieneMarca(normalizado, marcasAdicion)

	nuevos := 0 // cuántos colores nuevos lleva ESTE mensaje (para distinguir el multicolor)
	for _, c := range colores {
		if c == p.Color || tieneLinea(p.Items, c) {
			continue
		}
		nuevos++
		switch {
		case p.Color == "":
			p.Color = c
		case nuevos > 1 || (aditivo && !reemplaza && p.Cantidad >= 1):
			// Multicolor en un mensaje, o una suma explícita con la línea en curso cerrada.
			p.Items = append(p.Items, conversation.ItemPedido{Color: p.Color, Cantidad: p.Cantidad})
			p.Color, p.Cantidad = c, 0
		default:
			p.Color = c // cambio de opinión: pisa la línea en curso
		}
	}
	return p
}

// ponerCantidad asigna una cantidad a la línea del color dado, esté en curso o ya cerrada.
// Si el color no tiene línea, no toca nada (anotarColores corre antes y la habría abierto).
func ponerCantidad(p conversation.PedidoEnCurso, color string, n int) conversation.PedidoEnCurso {
	if p.Color == color {
		p.Cantidad = n
		return p
	}
	for i := range p.Items {
		if p.Items[i].Color == color {
			p.Items[i].Cantidad = n
			return p
		}
	}
	return p
}

func tieneLinea(items []conversation.ItemPedido, color string) bool {
	for _, it := range items {
		if it.Color == color {
			return true
		}
	}
	return false
}

func mismosItems(a, b []conversation.ItemPedido) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// anotarDeTool guarda lo que el modelo mandó como argumentos de registrar_pedido o
// programar_entrega. Se llama ANTES de ejecutar la herramienta, a propósito: si la tool falla
// (backend caído, sin cobertura), la ficha conserva lo que el cliente había pedido y el
// siguiente intento no arranca de cero.
func (a *Agent) anotarDeTool(from string, args map[string]any, flujo string) {
	p, _ := a.store.GetPedidoEnCurso(from)
	// Volver a inmediato BORRA la hora: si no, una hora dicha antes (o de una programación
	// cancelada) se queda pegada y el candado del fantasma agendaría un pedido que el cliente
	// quiere AHORA. El flujo tiene que poder ir en las dos direcciones.
	if flujo == conversation.FlujoInmediato {
		p.Hora = ""
	}
	// Los argumentos de la tool son el pedido COMPLETO según el modelo: pisan las líneas de la
	// ficha (no se suman, para no duplicar lo que anotarDelMensaje ya recogió del chat).
	if lineas := lineasDeArgs(args); len(lineas) > 0 {
		ultima := lineas[len(lineas)-1]
		p.Color, p.Cantidad = ultima.Color, 0
		if ultima.Cantidad >= 1 && ultima.Cantidad <= 20 {
			p.Cantidad = ultima.Cantidad
		}
		p.Items = nil
		if len(lineas) > 1 {
			p.Items = append(p.Items, lineas[:len(lineas)-1]...)
		}
	}
	if h := strings.TrimSpace(str(args["hora"])); h != "" {
		p.Hora = h
	}
	p.Flujo = flujo
	a.store.SetPedidoEnCurso(from, p)
}

// horaEnMensaje analiza si el mensaje habla de una hora. Devuelve dos cosas distintas a
// propósito:
//   - pareceHora: el cliente estaba diciendo una hora, sirva o no. Aunque no se pueda usar,
//     ese número NO es una cantidad de cilindros ("6h30" no son 6 tanques).
//   - hora: la hora utilizable (dentro del horario de atención), o "" si no sirve. Que el
//     cliente escriba "a las 3" no lo convierte en programación si a las 3 no se entrega; de
//     explicárselo se encarga programarEntrega, que da el motivo exacto.
func (a *Agent) horaEnMensaje(texto string) (pareceHora bool, hora string) {
	// Un NÚMERO SUELTO no es una hora: "2" responde a "¿cuántos cilindros?", no son las 02:00.
	// Para contar como hora el mensaje tiene que traer una marca horaria REAL, y por eso se
	// exige un patrón anclado y no un substring: buscar la letra "h" hacía que "Hola, quiero 8
	// blanco" se leyera como las 08:00 y convertía un pedido inmediato en una programación
	// (encontrado en la revisión del 07/09, antes de que llegara a producción). Lo mismo el
	// punto de "2 por favor. gracias", que hacía perder la cantidad.
	if !marcaHoraria.MatchString(strings.ToLower(texto)) {
		return false, ""
	}

	h := extraerHora(texto)
	if h == "" {
		return false, ""
	}
	// Desde aquí, el cliente SÍ estaba diciendo una hora (aunque no podamos atenderla).
	ini, fin := parseHoraHHMM(a.cfg.BotHorarioInicio), parseHoraHHMM(a.cfg.BotHorarioFin)
	if ini < 0 || fin < 0 {
		return true, h // sin horario configurado no se filtra nada
	}
	if m := parseHoraHHMM(h); m < ini || m > fin {
		return true, "" // "6h30" con horario 07-19: es una hora, pero no una que sirva
	}
	return true, h
}
