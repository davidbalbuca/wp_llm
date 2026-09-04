package agent

import (
	"log"
	"strconv"
	"strings"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/notify"
)

// Este archivo resuelve EN CÓDIGO dos acciones críticas cuando el modelo se las salta: registrar
// y cancelar un pedido. El modelo (haiku) demostró en producción que a veces ESCRIBE la respuesta
// ("¡tu pedido está confirmado!", "he cancelado tu pedido") SIN llamar a la herramienta. El
// prompt lo prohíbe, pero no basta. La filosofía es la misma que ya usan confirmacion.go y el
// candado de la hora: una decisión que le cuesta dinero al negocio o deja al cliente esperando NO
// puede depender de que el modelo se acuerde de invocar una función.

// forzarRegistroSiHaceFalta se llama cuando el modelo afirmó un pedido confirmado SIN haber
// llamado a registrar_pedido en este turno. En vez de solo tapar la mentira, INTENTA registrar
// el pedido de verdad con los datos que ya tenemos: color y cantidad de lo que el cliente eligió
// en la conversación, más ubicación y perfil (que registrarPedido saca solo). Devuelve el texto
// para el cliente y ok=true si se resolvió aquí (registrado o con un mensaje honesto), de modo
// que el llamador no siga con la respuesta inventada del modelo.
//
// Si NO logra inferir color y cantidad, no inventa nada: devuelve un mensaje que le pide al
// cliente retomar, y avisa al grupo. Mejor pedir un dato de más que registrar un pedido con un
// color equivocado.
func (a *Agent) forzarRegistroSiHaceFalta(from string) (string, bool) {
	color, cantidad, ok := a.inferirPedido(from)
	if !ok {
		// No sabemos qué pedir con certeza: honesto y sin inventar.
		notify.Default.Fallo(from, a.nombreDe(from), "Pedido fantasma — no se pudo autorregistrar",
			"El modelo afirmó un pedido sin llamar registrar_pedido y el código no pudo inferir "+
				"color/cantidad con seguridad. El cliente NO tiene pedido; hay que atenderlo.")
		return "Disculpa 🙏, tuve un problema al registrar tu pedido. ¿Me confirmas qué cilindro " +
			"necesitas (color) y cuántos, y me compartes tu ubicación 📎? Quiero asegurarme de que " +
			"te llegue tu gas.", true
	}

	log.Printf("[forzar] %s: el modelo confirmó sin registrar; se registra en código (%d x %s)", from, cantidad, color)
	// registrarPedido hace TODO el flujo real (cuenta, login, geocerca, startOrder) y devuelve un
	// texto pensado para el MODELO. Pero como aquí ya no hay otra vuelta al modelo, convertimos su
	// resultado en algo para el cliente según lo que pasó (ver a.ultimoPedido, que registrarPedido
	// deja seteado).
	a.registrarPedido(from, map[string]any{"color": color, "cantidad": cantidad})

	// Si registrarPedido envió un MENÚ (p. ej. confirmar la dirección porque la ubicación no es de
	// esta conversación), ese menú YA salió al cliente: no lo pisamos con texto.
	if a.menuSent {
		return "", true
	}

	switch {
	case a.ultimoPedido.ok:
		notify.Default.Fallo(from, a.nombreDe(from), "Pedido rescatado por código",
			"El modelo iba a confirmar sin registrar; el código lo registró de verdad. Pedido #"+
				strconv.Itoa(a.ultimoPedido.IDPedido))
		msg := "¡Listo! 🎉 Tu pedido de " + strconv.Itoa(cantidad) + " " + color + " quedó registrado."
		if a.ultimoPedido.Conductor != "" {
			msg += " Tu repartidor es " + a.ultimoPedido.Conductor + " 🚚."
		}
		return msg + " Cualquier cosa, aquí estoy 😊", true
	case a.ultimoPedido.enEspera:
		// Sin repartidor: registrarPedido ya dejó el PendingWait. Ofrecemos esperar.
		return "¡Anotado tu pedido de " + strconv.Itoa(cantidad) + " " + color + "! 🙌 En este momento " +
			"los repartidores están un poco lejos. ¿Deseas que busque uno para ti? Puede tardar hasta " +
			"5 minutos. Escríbeme \"sí\" para esperar o \"no\" si prefieres que lo dejemos.", true
	default:
		// No se pudo (cobertura, catálogo, etc.): registrarPedido ya avisó/derivó si tocaba.
		return "Disculpa 🙏, no pude completar tu pedido ahora mismo. Ya avisé al equipo para que te " +
			"contacte y lo resuelva. ¿Me confirmas tu ubicación 📎 mientras tanto?", true
	}
}

// inferirPedido reconstruye el color y la cantidad que el cliente eligió, mirando lo que se dijo
// en la conversación reciente. El color se toma del catálogo (para no aceptar cualquier palabra);
// la cantidad, del primer número 1..20 que el cliente haya escrito después de elegir el color.
//
// Es conservador: si no hay un color válido del catálogo o una cantidad clara, devuelve ok=false
// y el llamador pregunta en vez de adivinar.
func (a *Agent) inferirPedido(from string) (color string, cantidad int, ok bool) {
	contexto, disponible := a.catalog.Get()
	if !disponible || contexto == nil {
		return "", 0, false
	}

	msgs := a.store.GetConversation(from, 30)
	// Recorremos del más reciente al más viejo: nos quedamos con la ÚLTIMA elección de color, que
	// es la que vale si el cliente cambió de opinión ("mejor amarillo"). Buscamos el color como
	// PALABRA dentro del mensaje, no por igualdad exacta: el cliente escribe "mejor Amarillo" o
	// "el azul porfa", no siempre el color a secas.
	colorIdx := -1
	for i := len(msgs) - 1; i >= 0 && colorIdx < 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		if c := colorEnTexto(contexto.Products, msgs[i].Content); c != "" {
			color = c
			colorIdx = i
		}
	}
	if colorIdx < 0 {
		return "", 0, false
	}

	// La cantidad: primer número plausible que el cliente escribió DESPUÉS de elegir el color
	// (o en el mismo mensaje). Un cilindro por defecto NO se asume: si no lo dijo, preguntamos.
	for i := colorIdx; i < len(msgs); i++ {
		if msgs[i].Role != "user" {
			continue
		}
		if n := primerNumero(msgs[i].Content); n >= 1 && n <= 20 {
			return color, n, true
		}
	}
	return "", 0, false
}

// primerNumero devuelve el primer entero que aparece en el texto, o -1. Sirve para leer la
// cantidad de un mensaje como "quiero 2" o "3 porfa". Ignora números pegados a otras cifras
// largas (teléfonos, cédulas) exigiendo que sea un token corto.
func primerNumero(s string) int {
	campos := strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	for _, c := range campos {
		if len(c) >= 1 && len(c) <= 2 { // 1..99: una cantidad, no una cédula
			if n, err := strconv.Atoi(c); err == nil {
				return n
			}
		}
	}
	return -1
}

// afirmaCancelado detecta que el texto le está diciendo al cliente que su pedido YA se canceló.
// Igual que con la confirmación: si el modelo lo afirma sin haber llamado a cancelar_pedido, el
// pedido sigue vivo en el backend (pasó el 03/09: el bot dijo "he cancelado tu pedido" y el
// pedido quedó activo hasta que el conductor lo canceló a mano desde su app).
func afirmaCancelado(texto string) bool {
	t := normalizar(texto)
	if t == "" {
		return false
	}
	// "cancelaste/cancelé" en pasado + referencia al pedido. Evita "¿quieres cancelar?" (futuro).
	hechos := []string{
		"he cancelado", "cancele tu pedido", "pedido cancelado", "quedo cancelado",
		"tu pedido fue cancelado", "cancelado con exito", "ya cancele", "acabo de cancelar",
		"lo he cancelado", "cancelamos tu pedido",
	}
	for _, h := range hechos {
		if strings.Contains(t, h) {
			return true
		}
	}
	return false
}

// forzarCancelacionSiHaceFalta se llama cuando el modelo afirmó que canceló SIN haber llamado a
// cancelar_pedido. Si hay un pedido activo, lo cancela de verdad. Devuelve el texto para el
// cliente y ok=true si se resolvió aquí.
func (a *Agent) forzarCancelacionSiHaceFalta(from string) (string, bool) {
	if _, hay := a.store.GetActivePedido(from); !hay {
		// El modelo dijo "cancelado" pero no hay pedido activo: puede ser un pedido ya entregado
		// o una confusión. No hay nada que cancelar en el backend; dejamos pasar su texto.
		return "", false
	}
	log.Printf("[forzar] %s: el modelo dijo cancelado sin llamar cancelar_pedido; se cancela en código", from)
	salida := a.cancelarPedido(from) // texto pensado para el modelo, pero sirve de guía

	// cancelarPedido limpia el active_pedido cuando el pedido queda cancelado (por este intento o
	// porque YA estaba cancelado). Si sigue ahí, la cancelación falló de verdad.
	if _, sigue := a.store.GetActivePedido(from); sigue {
		notify.Default.Fallo(from, a.nombreDe(from), "Cancelación fantasma — no se pudo cancelar",
			"El modelo dijo al cliente que su pedido estaba cancelado, pero el backend NO lo "+
				"canceló. El pedido SIGUE VIVO. Hay que cancelarlo a mano. Detalle: "+conversation.Recortar(salida, 200))
		return "Disculpa 🙏, tuve un problema al cancelar tu pedido. Ya avisé al equipo para que lo " +
			"cancele enseguida. Lamento la molestia.", true
	}

	// El pedido quedó cancelado. Si ya estaba cancelado (el conductor lo canceló antes), NO se
	// avisa al grupo: no es un rescate ni un fallo, solo el cliente pidiendo cancelar algo que ya
	// no estaba vivo. Antes esto disparaba una alerta roja de falso positivo.
	if !yaEstabaCancelado(salida) {
		notify.Default.Fallo(from, a.nombreDe(from), "Cancelación rescatada por código",
			"El modelo iba a decir cancelado sin cancelar; el código canceló el pedido de verdad.")
	}
	return "Listo, cancelé tu pedido 🙏. Cuando necesites tu gas, aquí estoy para ayudarte 😊", true
}

// colorEnTexto devuelve el nombre de catálogo del color mencionado en el texto (como palabra
// suelta), o "" si ninguno aparece. Usa normalizar para ignorar tildes/mayúsculas, y compara
// contra las PALABRAS del mensaje para no matchear "azulejo" con "azul".
func colorEnTexto(products []georoutes.Product, texto string) string {
	palabras := map[string]bool{}
	for _, p := range strings.Fields(normalizar(texto)) {
		palabras[p] = true
	}
	for _, prod := range products {
		for _, col := range prod.Colores {
			if palabras[normalizar(col.Nombre)] {
				return col.Nombre
			}
		}
	}
	return ""
}
