package agent

// Confirmar la direccion antes de entregar, cuando la ubicacion guardada NO es de ahora.
//
// El caso que esto evita: un cliente pide en la mañana y en la tarde vuelve a pedir, pero desde
// otro lado. Su ubicacion sigue guardada (el bot las conserva 24 h), asi que sin esto el pedido
// saldria a la direccion de la mañana. Es el peor error posible: el gas llega a otra casa.
//
// Por que va aqui y no en el prompt. El texto de la direccion NO lo puede redactar el modelo:
// ya se vio que reescribe datos criticos -a David le ofrecio repetir "2 x GAS 15KG" cuando su
// ultimo pedido era de 23KG-. Si hiciera lo mismo con una direccion, el cliente confirmaria una
// y le llegaria a otra. Asi que el menu lo arma el codigo con el texto exacto que tenemos
// guardado, y la respuesta se resuelve tambien en codigo, sin pasar por el modelo.
//
// Ante cualquier duda se pide la ubicacion de nuevo. Molestar es barato; entregar mal, no.

import (
	"fmt"
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/notify"
	"wp-llm-gas/internal/whatsapp"
)

// Botones de la confirmacion. Como en las entregas agendadas, lo que vuelve es el titulo
// EXACTO, asi que la respuesta no hay que interpretarla.
const (
	BotonMismaDireccion = "Sí, la misma"
	BotonOtraDireccion  = "Otra dirección"
)

// VentanaUbicacionFresca: si la ubicacion llego hace menos de esto, es de la conversacion en
// curso y se usa sin preguntar. Un pedido se arma en minutos, no en horas.
const VentanaUbicacionFresca = 30 * time.Minute

// ubicacionEsDeAhora dice si la ubicacion guardada corresponde a esta conversacion.
func (a *Agent) ubicacionEsDeAhora(from string) bool {
	loc, ok := a.store.GetLocation(from)
	if !ok || loc.UpdatedAt == 0 {
		return false
	}
	return time.Since(time.Unix(loc.UpdatedAt, 0)) < VentanaUbicacionFresca
}

// pedirConfirmacionDireccion deja el pedido en pausa y le manda al cliente el menu con su
// direccion. Devuelve el texto para el MODELO (no para el cliente) y si se hizo cargo.
func (a *Agent) pedirConfirmacionDireccion(t *turno, from, color string, cantidad int) (string, bool) {
	direccion, hay := a.store.GetDireccionTexto(from)
	if !hay || strings.TrimSpace(direccion) == "" {
		// Sin una direccion legible no se puede preguntar nada util: mostrarle coordenadas o el
		// nombre de un barrio entero seria pedirle que confirme algo que no identifica.
		return "", false
	}

	a.store.SetPedidoEsperandoDireccion(from, color, cantidad)
	cuerpo := fmt.Sprintf("¿Te lo enviamos a %s?", direccion)
	if err := whatsapp.SendMenu(a.cfg, from, cuerpo,
		[]string{BotonMismaDireccion, BotonOtraDireccion}); err != nil {
		log.Printf("[direccion] no se pudo mandar la confirmación a %s: %v", from, err)
		a.store.ClearPedidoEsperandoDireccion(from)
		return "", false
	}

	t.menuSent = true
	t.lastMenuText = cuerpo + " [" + BotonMismaDireccion + " / " + BotonOtraDireccion + "]"
	a.store.AppendModel(from, t.lastMenuText)
	log.Printf("[direccion] %s: pedido en pausa esperando que confirme %q", from, direccion)
	return "Se le pidió al cliente que confirme su dirección. ESPERA su respuesta.", true
}

// ConfirmarDireccion resuelve, SIN pasar por el modelo, la respuesta del cliente al menu de
// direccion. Devuelve el mensaje para el cliente y si se hizo cargo del turno.
func (a *Agent) ConfirmarDireccion(from, texto string) (string, bool) {
	color, cantidad, enPausa := a.store.GetPedidoEsperandoDireccion(from)
	if !enPausa {
		return "", false
	}
	// Punto de entrada propio (lo llama cmd/bot, no HandleMessage): su turno nace aquí.
	t := &turno{}
	respuesta := normalizarRespuesta(texto)

	switch respuesta {
	case normalizarRespuesta(BotonMismaDireccion):
		a.store.ClearPedidoEsperandoDireccion(from)
		log.Printf("[direccion] %s confirmó su dirección; se registra el pedido", from)
		a.runTool(t, from, "registrar_pedido", map[string]any{
			"color": color, "cantidad": cantidad, "direccion_confirmada": true,
		})
		return a.mensajeDelPedido(t, from), true

	case normalizarRespuesta(BotonOtraDireccion):
		a.store.ClearPedidoEsperandoDireccion(from)
		// Se BORRA la ubicacion vieja: si no, el proximo intento volveria a proponerla.
		a.store.ClearLocation(from)
		log.Printf("[direccion] %s pidió otra dirección", from)
		return "Perfecto 👍 Compárteme tu ubicación actual por WhatsApp 📎 (botón de adjuntar → " +
			"Ubicación) y con eso te lo enviamos.", true

	default:
		// Cualquier otra cosa NO se interpreta: podria ser "sí pero a la casa de mi mamá".
		return "", false
	}
}

// mensajeDelPedido redacta para el cliente el desenlace del registro. Mismo criterio que la
// confirmación de entregas agendadas: el texto que devuelve la herramienta está escrito para el
// modelo, no para el cliente.
func (a *Agent) mensajeDelPedido(t *turno, from string) string {
	res := t.ultimoPedido
	switch {
	case res.ok:
		mensaje := fmt.Sprintf("¡Listo! 🎉 Tu pedido de %d x %s color %s va en camino.",
			res.Cantidad, res.Producto, res.Color)
		if res.Conductor != "" {
			mensaje += fmt.Sprintf(" Tu repartidor es %s.", res.Conductor)
		}
		return mensaje + " Te aviso apenas esté llegando. 🙌"
	case res.enEspera:
		if w, ok := a.store.GetPendingWait(from); ok {
			a.startWaitForDriver(from, w)
		}
		return "¡Listo! 🎉 Tu pedido quedó registrado. En este momento los repartidores están " +
			"un poco lejos, así que estoy buscando uno para ti 🚚. En menos de 5 minutos te " +
			"confirmo. No tienes que hacer nada."
	default:
		// FUERA DE HORARIO no es un fallo: es una condición normal del negocio. Decirle
		// "inconveniente técnico, ya avisé al equipo" lo asusta y además MIENTE (nadie fue
		// avisado, no hay ticket). Se le dice la verdad y se le ofrece agendar.
		if !a.dentroDeHorario(time.Now().In(zonaEcuador)) {
			return fmt.Sprintf("A esta hora ya no tenemos repartidores en ruta 🙏. Atendemos de %s "+
				"a %s: dime a qué hora te viene bien mañana y te la dejo agendada 😊",
				a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin)
		}
		// Cualquier otro motivo sí es un problema nuestro: se DERIVA de verdad (crea el ticket)
		// antes de prometerle al cliente que el equipo lo va a contactar.
		notify.ReportarFallo(a.cfg, a.store, from, "No se pudo registrar el pedido",
			"El registro falló en un flujo resuelto por código (confirmación de dirección o "+
				"repetir pedido). El cliente quedó esperando; hay que contactarlo.")
		return "Recibí tu confirmación ✅, pero tuve un inconveniente al registrar el pedido. " +
			"Ya avisé a nuestro equipo para que te contacte enseguida."
	}
}
