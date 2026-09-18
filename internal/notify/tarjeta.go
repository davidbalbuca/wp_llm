// TARJETA DE ESTADO POR CLIENTE: un solo mensaje en Telegram que avanza.
//
// Pedido del dueño (18/09): "ver si podemos integrar estados por cliente; ahora tenemos cuando un
// cliente inicia la conversación, al menos poner si ya solicitó gas o se registró el pedido y
// cuándo le entregué, 3 al menos adicionales a los que ya existen, y si hay algún error".
//
// POR QUÉ UN MENSAJE QUE SE EDITA Y NO CUATRO MENSAJES NUEVOS. Cuatro avisos por cliente, por
// todos los clientes del día, vuelven el grupo ilegible. Y un grupo que nadie lee es PEOR que no
// tener avisos: da falsa sensación de vigilancia —alguien cree que el sistema está mirado cuando
// en realidad nadie lo mira—. Telegram permite editar un mensaje ya enviado, así que cuesta lo
// mismo dejar una línea de tiempo que se entiende de un vistazo:
//
//	🟢 María Pérez  +593...
//	✅ Escribió → ✅ Pidió gas → ✅ Registrado #942 → 🚚 Entregado
//	📦 2 x GAS 15KG (BLANCO)
//
// REGLAS QUE ESTO RESPETA (las mismas del resto del paquete):
//   - Es un OBSERVADOR: ningún fallo de Telegram puede cortar la conversación del cliente. Todo va
//     en goroutine protegida y no devuelve error.
//   - Nunca RETROCEDE (TarjetaEstado.Avanza): los avisos llegan de goroutines distintas —el
//     webhook, el reintento de repartidor a los 5 min, el aviso de entrega del backend— y pueden
//     desordenarse. Una tarjeta que vuelve de "Entregado" a "Pidió gas" hace que el grupo
//     desconfíe de todas las demás.
//   - Silenciosa salvo lo que hay que atender: el avance normal de un pedido no tiene que vibrar
//     el teléfono de nadie. Los errores siguen yendo por Fallo(), con sonido y a su hilo.
package notify

import (
	"fmt"
	"html"
	"strings"
	"sync"

	"wp-llm-gas/internal/conversation"
)

// cerrojosTarjeta serializa los avisos DE UN MISMO cliente, no de todos.
//
// Hace falta porque la secuencia leer-tarjeta → crear-mensaje → guardar-id no es atómica: dos
// avisos simultáneos del mismo cliente (el webhook y el reintento de repartidor, por ejemplo)
// leerían los dos MessageID = 0 y el grupo vería DOS tarjetas del mismo pedido.
//
// Es un cerrojo POR TELÉFONO y no el mutex del Notifier a propósito: ese ya lo usan los hilos
// fijos, y tomarlo aquí —alrededor de una llamada HTTP a Telegram— pondría en fila los avisos de
// todos los clientes detrás del más lento.
var cerrojosTarjeta sync.Map // phone -> *sync.Mutex

// bloquearTarjeta toma el cerrojo de ese cliente y devuelve cómo soltarlo.
func (n *Notifier) bloquearTarjeta(phone string) func() {
	valor, _ := cerrojosTarjeta.LoadOrStore(phone, &sync.Mutex{})
	mu := valor.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// etapa describe cómo se pinta cada paso en la tarjeta.
type etapa struct {
	orden  int
	icono  string
	nombre string
}

// etapasEnOrden son los pasos del ciclo, tal como se muestran. "Cancelado" no está aquí: es un
// final alternativo que reemplaza la línea entera (ver lineaDeEtapas).
// El icono de "Entregado" es ✅ y no 🚚: es el final del ciclo, y un camión se lee como "va en
// camino". Los demás describen el paso que ocurrió.
var etapasEnOrden = []etapa{
	{conversation.EtapaEscribio, "💬", "Escribió"},
	{conversation.EtapaPidioGas, "🛒", "Pidió gas"},
	{conversation.EtapaRegistrado, "📝", "Registrado"},
	{conversation.EtapaEntregado, "✅", "Entregado"},
}

// EstadoCliente mueve la tarjeta del cliente a una etapa nueva. Si no hay tarjeta, la crea; si la
// etapa no avanza respecto a la actual, no hace nada.
//
// `detalle` es lo que se sabe del pedido ("2 x GAS 15KG (BLANCO)") y se conserva entre etapas:
// pasar "" no borra lo que ya había. `pedidoID` igual (0 = no cambia).
func (n *Notifier) EstadoCliente(phone, nombre string, nuevaEtapa int, detalle string, pedidoID int) {
	if n == nil {
		return
	}
	go n.protegido("EstadoCliente", func() {
		// El hilo se resuelve ANTES del cerrojo: crearlo es una llamada de red, y retener el
		// cerrojo durante un HTTP bloquearía los avisos de este cliente sin necesidad.
		hilo := n.hiloDe(phone, nombre)

		desbloquear := n.bloquearTarjeta(phone)
		defer desbloquear()

		t, hay := n.store.GetTarjetaEstado(phone)
		if hay && !t.Avanza(nuevaEtapa) {
			// No es un error: dos avisos de la misma etapa (o uno que llega tarde) son normales.
			return
		}
		t.Etapa = nuevaEtapa
		if d := strings.TrimSpace(detalle); d != "" {
			t.Detalle = d
		}
		if pedidoID > 0 {
			t.PedidoID = pedidoID
		}
		if t.ThreadID == 0 {
			t.ThreadID = hilo
		}
		n.pintarTarjeta(phone, nombre, t)
	})
}

// ErrorCliente deja constancia en la tarjeta de que algo falló con este cliente.
//
// NO reemplaza la etapa: un pedido puede estar registrado Y haber tenido un problema que alguien
// debe mirar. Tampoco reemplaza a Fallo(), que sigue avisando con sonido en el hilo de errores —
// esto solo hace que el problema se vea en la línea de tiempo del cliente, donde se entiende en su
// contexto.
func (n *Notifier) ErrorCliente(phone, nombre, motivo string) {
	if n == nil || strings.TrimSpace(motivo) == "" {
		return
	}
	go n.protegido("ErrorCliente", func() {
		hilo := n.hiloDe(phone, nombre)

		desbloquear := n.bloquearTarjeta(phone)
		defer desbloquear()

		t, hay := n.store.GetTarjetaEstado(phone)
		if !hay {
			// Sin tarjeta previa se abre una: el cliente escribió, aunque no hayamos registrado
			// esa etapa (puede haber fallado justo al principio).
			t.Etapa = conversation.EtapaEscribio
		}
		t.Error = motivo
		if t.ThreadID == 0 {
			t.ThreadID = hilo
		}
		n.pintarTarjeta(phone, nombre, t)
	})
}

// CerrarTarjeta marca el final del ciclo y deja de editar ese mensaje: la próxima conversación del
// cliente abre una tarjeta nueva, en vez de reescribir la historia del pedido anterior.
//
// `cancelado` distingue los dos finales: entregado (lo normal) o cancelado por el cliente.
func (n *Notifier) CerrarTarjeta(phone, nombre string, cancelado bool) {
	if n == nil {
		return
	}
	go n.protegido("CerrarTarjeta", func() {
		desbloquear := n.bloquearTarjeta(phone)
		defer desbloquear()

		t, hay := n.store.GetTarjetaEstado(phone)
		if !hay {
			return
		}
		etapaFinal := conversation.EtapaEntregado
		if cancelado {
			etapaFinal = conversation.EtapaCancelado
		}
		if t.Avanza(etapaFinal) {
			t.Etapa = etapaFinal
			n.pintarTarjeta(phone, nombre, t)
		}
		// Se olvida la tarjeta DESPUÉS de pintar el final: el mensaje queda en el grupo con su
		// estado último, y el próximo pedido empieza uno nuevo.
		n.store.ClearTarjetaEstado(phone)
	})
}

// pintarTarjeta crea o edita el mensaje en Telegram y guarda el resultado. Se llama SIEMPRE con el
// cerrojo del cliente tomado (bloquearTarjeta): sin eso, dos avisos simultáneos del mismo cliente
// leerían los dos MessageID = 0 y crearían DOS tarjetas para el mismo pedido.
func (n *Notifier) pintarTarjeta(phone, nombre string, t conversation.TarjetaEstado) {
	texto := n.textoTarjeta(phone, nombre, t)

	if t.MessageID == 0 {
		id, err := n.enviarConID(t.ThreadID, texto)
		if err != nil {
			// Se guarda igual el avance: el estado del cliente no se pierde porque Telegram
			// falle, y el próximo aviso reintenta la creación.
			n.store.SetTarjetaEstado(phone, t)
			return
		}
		t.MessageID = id
		n.store.SetTarjetaEstado(phone, t)
		return
	}
	// Editar: si el mensaje ya no existe (lo borraron del grupo) se empieza uno nuevo en el
	// siguiente aviso, en vez de reintentar para siempre contra un id muerto.
	if err := n.editar(t.MessageID, texto); err != nil {
		t.MessageID = 0
	}
	n.store.SetTarjetaEstado(phone, t)
}

// textoTarjeta arma el contenido. Todo lo que viene del cliente va escapado: un nombre de WhatsApp
// con "<" rompería el HTML del mensaje.
func (n *Notifier) textoTarjeta(phone, nombre string, t conversation.TarjetaEstado) string {
	quien := strings.TrimSpace(nombre)
	if quien == "" {
		quien = "cliente nuevo"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>\n<code>+%s</code>\n\n%s",
		html.EscapeString(quien), html.EscapeString(phone), lineaDeEtapas(t))
	if d := strings.TrimSpace(t.Detalle); d != "" {
		fmt.Fprintf(&b, "\n📦 %s", html.EscapeString(conversation.Recortar(d, 200)))
	}
	if t.PedidoID > 0 {
		fmt.Fprintf(&b, "\n🧾 Pedido #%d", t.PedidoID)
	}
	if e := strings.TrimSpace(t.Error); e != "" {
		// El error va AL FINAL y marcado: es lo que alguien tiene que mirar.
		fmt.Fprintf(&b, "\n\n🔴 <b>%s</b>\n<i>(detalle en Errores del sistema)</i>",
			html.EscapeString(conversation.Recortar(e, 200)))
	}
	return b.String()
}

// lineaDeEtapas pinta el avance: lo alcanzado con ✅ y lo pendiente en gris.
func lineaDeEtapas(t conversation.TarjetaEstado) string {
	if t.Etapa == conversation.EtapaCancelado {
		// Un pedido cancelado no es un paso más de la fila: es otro final. Mostrar "Entregado" en
		// gris al lado sugeriría que todavía puede llegar.
		return "❌ <b>Cancelado por el cliente</b>"
	}
	// SOLO SE MUESTRA LO QUE YA PASÓ.
	//
	// Antes se pintaba la fila entera y lo pendiente iba en cursiva. Sobre el papel se distingue;
	// en Telegram no: la cursiva casi no se nota, y en la NOTIFICACIÓN del móvil el formato se
	// pierde del todo. El 18/09 el dueño vio "Escribió → Pidió gas → Registrado → Entregado" en un
	// cliente que solo había escrito, y entendió —con razón— que el pedido estaba entregado.
	//
	// Un aviso que se lee mal es un aviso que miente. Ahora la fila crece según avanza el pedido:
	// lo que se ve, ocurrió.
	partes := make([]string, 0, len(etapasEnOrden))
	for _, e := range etapasEnOrden {
		if e.orden > t.Etapa {
			break
		}
		partes = append(partes, e.icono+" "+e.nombre)
	}
	if len(partes) == 0 {
		return ""
	}
	return strings.Join(partes, " → ")
}
