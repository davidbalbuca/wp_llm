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
	"strings"

	"wp-llm-gas/internal/conversation"
)

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

	// 1. Color: se valida contra el catálogo, así que "el azul" solo cuenta si azul existe.
	if contexto, ok := a.catalog.Get(); ok && contexto != nil {
		if c := colorEnTexto(contexto.Products, texto); c != "" {
			p.Color = c
		}
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

	// 3. Cantidad: solo si el mensaje NO hablaba de una hora y ya hay un color elegido. Sin la
	//    condición del color, el "2" de "somos 2 en la casa" o el número de una calle se
	//    tomaría como cantidad.
	if !pareceHora && p.Color != "" {
		if n := primerNumero(texto); n >= 1 && n <= 20 {
			p.Cantidad = n
		}
	}

	if p.Flujo == "" {
		p.Flujo = conversation.FlujoInmediato
	}
	// 4. Solo se escribe si algo cambió: evita tocar el store en cada mensaje de charla.
	if p.Color == antes.Color && p.Cantidad == antes.Cantidad && p.Hora == antes.Hora && p.Flujo == antes.Flujo {
		return
	}
	log.Printf("[encurso] %s: color=%q cantidad=%d hora=%q flujo=%s", from, p.Color, p.Cantidad, p.Hora, p.Flujo)
	a.store.SetPedidoEnCurso(from, p)
}

// anotarDeTool guarda lo que el modelo mandó como argumentos de registrar_pedido o
// programar_entrega. Se llama ANTES de ejecutar la herramienta, a propósito: si la tool falla
// (backend caído, sin cobertura), la ficha conserva lo que el cliente había pedido y el
// siguiente intento no arranca de cero.
func (a *Agent) anotarDeTool(from string, args map[string]any, flujo string) {
	p, _ := a.store.GetPedidoEnCurso(from)
	if c := strings.TrimSpace(str(args["color"])); c != "" {
		p.Color = c
	}
	if n := toInt(args["cantidad"]); n >= 1 && n <= 20 {
		p.Cantidad = n
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
	// Un NÚMERO SUELTO no es una hora: "2" es la respuesta a "¿cuántos cilindros?", no las
	// 02:00. Para que cuente como hora, el mensaje tiene que traer alguna marca horaria
	// (":", "h", "pm/am", "las"...) o minutos explícitos. Sin esto, la cantidad del pedido se
	// leía como hora y el pedido inmediato se convertía en programación.
	t := strings.ToLower(strings.TrimSpace(texto))
	tieneMarca := strings.ContainsAny(t, ":.") ||
		strings.Contains(t, "h") || strings.Contains(t, "am") || strings.Contains(t, "pm") ||
		strings.Contains(t, "las ") || strings.Contains(t, "la ") ||
		strings.Contains(t, "media") || strings.Contains(t, "y cuarto")
	if !tieneMarca {
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
