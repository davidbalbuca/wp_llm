package agent

// COLOR EQUIVALENTE PARA UN PEDIDO QUE YA EXISTE (18/10, backend core/georoutes/color_alterno.py).
//
// Dueño: "un cliente pide un cilindro AMARILLO. Le llega a un repartidor amarillo a 1,5 km que no lo
// acepta. El sistema busca hasta 3 km y no hay otro amarillo, pero a 1 km hay uno con BLANCO. Antes
// de ampliar la búsqueda a toda la zona debería ofrecerle el blanco al cliente para que haga el
// cambio de color". Hasta hoy lo hacían los operadores a mano en el chat (18/10, caso Jhoana y
// Román: el operador les ofreció blanco a mano).
//
// El backend decide CUÁNDO (el primer "no hay otro de su color cerca": no aceptó, canceló o no iba),
// una sola vez por pedido, y nos avisa por /internal/order-color-offer. Aquí solo se pregunta y se
// resuelve la respuesta EN CÓDIGO, antes del modelo y también en los chats que tiene una persona
// (como ConfirmarProgramado): el "sí" cambia el pedido en el backend y no puede depender de que el
// modelo lo interprete, ni quedarse sin respuesta porque el chat lo tomó un operador.
//
// Nunca se nombra al repartidor: al cliente se le presenta recién cuando ACEPTA (por_confirmar.go).
// Mientras el cliente decide, el pedido sigue su camino de siempre en el backend.

import (
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"wp-llm-gas/internal/conversation"
)

// OfertaColorPedido es lo que manda el backend en /internal/order-color-offer.
type OfertaColorPedido struct {
	IDPedido       int
	ColorActual    string
	ColorAlterno   string
	IDColorAlterno int
	Minutos        int
}

// vigenciaColorPedido: pasado este rato la oferta ya no se intercepta (el backend la da por vencida
// a los 2 min; aquí se deja más margen para contestarle bien al que responde tarde, con "ese
// repartidor ya no está disponible", en vez de que su "sí" le llegue suelto al modelo).
const vigenciaColorPedido = 30 * time.Minute

// TextoColorDelPedido: corto, con el color que pidió, el que hay y cuándo llega. Sin nombre.
func TextoColorDelPedido(o OfertaColorPedido) string {
	llega := ""
	if o.Minutos > 0 {
		llega = fmt.Sprintf(" a unos %d min", o.Minutos)
	}
	return fmt.Sprintf("No encuentro repartidor con cilindro %s cerca, pero tengo uno con %s%s. ¿Te sirve el %s? "+
		"(mismo precio)", strings.ToUpper(o.ColorActual), strings.ToUpper(o.ColorAlterno), llega,
		strings.ToUpper(o.ColorAlterno))
}

// Botones del menú (máx. 20 caracteres en WhatsApp: si el color es muy largo, el genérico).
func botonSiColorPedido(color string) string {
	if b := "Sí, " + strings.ToUpper(color); utf8.RuneCountInString(b) <= 20 {
		return b
	}
	return botonSiAlterno(color)
}

func botonNoColorPedido(color string) string {
	if b := "No, espero " + strings.ToUpper(color); utf8.RuneCountInString(b) <= 20 {
		return b
	}
	return BotonSeguirEsperando
}

// OfrecerColorDelPedido manda el menú del cambio de color y deja la oferta pendiente. Devuelve false
// si no se le preguntó (ya tiene otro menú de color sin contestar, el pedido ya no es el suyo, o el
// menú no salió).
func (a *Agent) OfrecerColorDelPedido(from string, o OfertaColorPedido) bool {
	if o.IDPedido <= 0 || o.IDColorAlterno <= 0 || o.ColorAlterno == "" {
		return false
	}
	if activo, hay := a.store.GetActivePedido(from); hay && activo > 0 && activo != o.IDPedido {
		log.Printf("[color-pedido] %s: la oferta es del pedido %d pero tiene el %d en curso; no se pregunta",
			from, o.IDPedido, activo)
		return false
	}
	if sw, hay := a.store.GetPendingColorSwap(from); hay && sw.IDPedido != o.IDPedido {
		log.Printf("[color-pedido] %s: tiene otra oferta de color sin contestar; no se le encima la del pedido %d",
			from, o.IDPedido)
		return false
	}
	cuerpo := TextoColorDelPedido(o)
	opciones := []string{botonSiColorPedido(o.ColorAlterno), botonNoColorPedido(o.ColorActual)}
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[color-pedido] %s: el menú del pedido %d no salió (%v)", from, o.IDPedido, err)
		return false
	}
	a.store.SetPendingColorSwap(from, conversation.PendingColorSwap{
		ColorOriginal: o.ColorActual, ColorAlterno: o.ColorAlterno, IDColorAlterno: o.IDColorAlterno,
		IDPedido: o.IDPedido, OfrecidoAt: time.Now().Unix(),
	})
	texto := cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	a.store.LogMessage(from, "system", "📋 "+texto)
	a.store.AppendModel(from, texto)
	log.Printf("[color-pedido] %s: pedido %d sin otro %s cerca; se ofrece %s (~%d min)",
		from, o.IDPedido, o.ColorActual, o.ColorAlterno, o.Minutos)
	return true
}

// ResponderColorDelPedido resuelve el sí / no de la oferta de color de un pedido que ya existe. Solo
// esa: cualquier otra oferta de color la resuelve ResponderCambioColor. Lo llama el webhook antes de
// los demás menús y también con el chat en manos de una persona.
func (a *Agent) ResponderColorDelPedido(from, texto string) (string, bool) {
	sw, hay := a.store.GetPendingColorSwap(from)
	if !hay || sw.IDPedido <= 0 {
		return "", false
	}
	return a.responderColorDelPedido(from, texto, sw)
}

// Lo que cuenta como "no" en ESTE menú. A propósito sin "cancelar" ni "ya no quiero" (que sí están
// en respuestasNegativas): eso es cancelar el pedido, y lo tiene que ver la cancelación.
var noAlColorDelPedido = map[string]bool{
	"no": true, "nop": true, "no gracias": true, "mejor no": true, "negativo": true, "no por ahora": true,
	"espero": true, "lo espero": true, "prefiero esperar": true,
}

func (a *Agent) responderColorDelPedido(from, texto string, sw conversation.PendingColorSwap) (string, bool) {
	if sw.OfrecidoAt > 0 && time.Since(time.Unix(sw.OfrecidoAt, 0)) > vigenciaColorPedido {
		a.store.ClearPendingColorSwap(from)
		return "", false
	}
	respuesta := normalizarRespuesta(texto)
	original := strings.ToUpper(sw.ColorOriginal)
	var msg string
	switch {
	case respuesta == normalizarRespuesta(botonSiColorPedido(sw.ColorAlterno)) ||
		respuesta == normalizarRespuesta(botonSiAlterno(sw.ColorAlterno)) || respuestasAfirmativas[respuesta]:
		a.store.ClearPendingColorSwap(from)
		msg = a.aceptarColorDelPedido(from, sw)

	case respuesta == normalizarRespuesta(botonNoColorPedido(sw.ColorOriginal)) ||
		respuesta == normalizarRespuesta(BotonSeguirEsperando) || noAlColorDelPedido[respuesta]:
		a.store.ClearPendingColorSwap(from)
		log.Printf("[color-pedido] %s no quiso %s para el pedido %d; sigue su %s", from, sw.ColorAlterno,
			sw.IDPedido, sw.ColorOriginal)
		msg = fmt.Sprintf("Perfecto, sigo buscando tu %s 🙏", original)

	default:
		return "", false // una pregunta, un matiz: lo atiende el modelo (o la persona del chat)
	}
	a.store.AppendUser(from, texto)
	a.store.AppendModel(from, msg)
	return msg, true
}

// aceptarColorDelPedido le pide al backend el cambio y arma el mensaje según lo que pasó.
func (a *Agent) aceptarColorDelPedido(from string, sw conversation.PendingColorSwap) string {
	original, alterno := strings.ToUpper(sw.ColorOriginal), strings.ToUpper(sw.ColorAlterno)
	noDisponible := fmt.Sprintf("Ese repartidor ya no está disponible; sigo buscando tu %s 🙏", original)
	account, ok := a.store.GetAccount(from)
	if !ok {
		log.Printf("[color-pedido] %s sin cuenta; no se puede cambiar el pedido %d", from, sw.IDPedido)
		return noDisponible
	}
	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		log.Printf("[color-pedido] %s: login falló (%v); el pedido %d sigue en %s", from, err, sw.IDPedido, original)
		return noDisponible
	}
	res, err := a.gr.PedidoCambioColor(tokens.Access, sw.IDPedido, sw.IDColorAlterno)
	if err != nil {
		log.Printf("[color-pedido] %s: el backend no cambió el pedido %d (%v)", from, sw.IDPedido, err)
		return noDisponible
	}
	log.Printf("[color-pedido] %s: pedido %d %s→%s: %s", from, sw.IDPedido, original, alterno, res.Estado)
	switch res.Estado {
	case "ok":
		// El nuevo todavía no aceptó: se le presenta cuando acepte (order-reassigned con primer_aviso).
		marcarPorConfirmar(a.cfg, from, sw.IDPedido)
		a.recordarColorNuevo(from, sw)
		return fmt.Sprintf("Listo, cambié tu pedido a %s ✅. Estoy confirmando con el repartidor 🚚", alterno)
	case "ya_acepto":
		return fmt.Sprintf("¡Buenas noticias! Tu repartidor de %s ya aceptó y va en camino 🚚", original)
	}
	return noDisponible
}

// recordarColorNuevo deja el color nuevo donde el bot lo vuelve a decir: la espera (si el pedido
// vino de una búsqueda, el aviso de "ya tienes repartidor" lo nombra) y el último pedido ("repetir
// lo mismo").
func (a *Agent) recordarColorNuevo(from string, sw conversation.PendingColorSwap) {
	if w, ok := a.store.GetPendingWait(from); ok && len(w.Items) <= 1 &&
		strings.EqualFold(w.ColorNombre, sw.ColorOriginal) {
		w.IDColor, w.ColorNombre = sw.IDColorAlterno, sw.ColorAlterno
		a.store.SetPendingWait(from, w)
	}
	if lo, ok := a.store.GetLastOrder(from); ok && len(lo.Items) <= 1 && strings.EqualFold(lo.Color, sw.ColorOriginal) {
		lo.Color = sw.ColorAlterno
		a.store.SetLastOrder(from, lo)
	}
}

// OlvidarColorDelPedido borra la oferta pendiente de ESE pedido cuando el pedido terminó (entregado,
// cancelado, cerrado sin repartidor): ya no hay nada que cambiar.
func OlvidarColorDelPedido(store conversation.Store, from string, idpedido int) {
	if sw, hay := store.GetPendingColorSwap(from); hay && sw.IDPedido > 0 && sw.IDPedido == idpedido {
		store.ClearPendingColorSwap(from)
	}
}
