// UN TICKET LLEVA DATOS DEL ESTADO, NO DE LA IMAGINACIÓN DEL MODELO.
//
// `escalar_al_dueno` toma `motivo` y `resumen` tal como los escribe el modelo y los guarda en el
// ticket. Eso convierte la redacción de un LLM en la orden de trabajo de una persona.
//
// El 26/09 salió caro. Ticket #56: "Carlos pidió cancelar su pedido de 5 cilindros amarillos…
// necesita que alguien revise y confirme la cancelación". En la base del bot, Carlos no tenía
// cuenta, ni pedido, ni entrega agendada: los 5 cilindros los compuso el modelo de una
// conversación en la que el cliente nunca llegó a decir una cantidad. Un operador quedó encargado
// de confirmar la cancelación de un pedido inexistente.
//
// El arreglo NO es prohibirle al modelo que resuma —su resumen es útil, describe la queja mejor
// que ningún campo—. Es SEPARAR las dos cosas en el ticket:
//
//	lo que dijo el modelo   -> se conserva, marcado como relato del cliente
//	lo que dice el estado   -> se añade SIEMPRE, y es lo único sobre lo que se actúa
//
// Así el operador ve "el cliente dice X" junto a "el sistema tiene Y", y cuando no coinciden la
// discrepancia salta a la vista en vez de esconderse. Un dato que no está en la base aparece como
// "no registrado", nunca rellenado.
package agent

import (
	"fmt"
	"strings"
)

// estadoParaTicket compone, LEYENDO EL STORE, lo que de verdad sabemos del cliente. Es el
// contrapeso del resumen del modelo.
//
// Nunca falla ni inventa: lo que no está, se dice que no está. Esa es la diferencia entre una
// orden de trabajo y una suposición.
func (a *Agent) estadoParaTicket(from string) string {
	var b strings.Builder
	b.WriteString("— ESTADO REAL EN EL SISTEMA (leído de la base, no del chat) —\n")
	b.WriteString("Teléfono: " + from + "\n")

	if cuenta, ok := a.store.GetAccount(from); ok && cuenta.Username != "" {
		b.WriteString("Cuenta: " + cuenta.Username + "\n")
	} else {
		b.WriteString("Cuenta: NO tiene (nunca completó un pedido)\n")
	}

	if id, ok := a.store.GetActivePedido(from); ok && id > 0 {
		b.WriteString(fmt.Sprintf("Pedido en curso: #%d\n", id))
	} else {
		b.WriteString("Pedido en curso: NINGUNO\n")
	}

	if p, ok := a.store.GetPedidoEnCurso(from); ok {
		// La ficha de lo que se está armando. Puede estar a medias, y eso es información:
		// cantidad=0 significa que el cliente todavía no dijo cuántos.
		color := p.Color
		if color == "" {
			color = "no registrado"
		}
		cantidad := "no registrada"
		if p.Cantidad > 0 {
			cantidad = fmt.Sprintf("%d", p.Cantidad)
		}
		hora := p.Hora
		if hora == "" {
			hora = "no registrada"
		}
		b.WriteString(fmt.Sprintf("Ficha a medias: color=%s · cantidad=%s · hora=%s\n", color, cantidad, hora))
	} else {
		b.WriteString("Ficha a medias: ninguna\n")
	}

	if a.tieneEntregaAgendada(from) {
		b.WriteString("Entrega agendada: SÍ (ver pedidos programados)\n")
	} else {
		b.WriteString("Entrega agendada: NO\n")
	}

	if perfil, ok := a.store.GetProfile(from); ok {
		if perfil.Identificacion != "" {
			b.WriteString("Cédula: " + perfil.Identificacion + "\n")
		}
		if perfil.Nombres != "" {
			b.WriteString("Nombre: " + perfil.Nombres + "\n")
		}
	}
	return b.String()
}

// resumenConEstado junta el relato del modelo con el estado verificable. El orden importa: el
// estado va al FINAL, que es lo último que lee quien atiende el ticket.
//
// El relato se etiqueta como lo que es —lo que el cliente contó, filtrado por el modelo— para que
// nadie lo confunda con un hecho del sistema. Es exactamente el error del #56.
func (a *Agent) resumenConEstado(from, resumenDelModelo string) string {
	relato := strings.TrimSpace(resumenDelModelo)
	if relato == "" {
		relato = "(el modelo no dejó resumen)"
	}
	return "— LO QUE CUENTA EL CLIENTE (relato del chat, SIN verificar) —\n" + relato + "\n\n" +
		a.estadoParaTicket(from)
}
