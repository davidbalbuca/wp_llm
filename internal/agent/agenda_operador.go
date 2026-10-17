package agent

import (
	"fmt"
	"strings"
	"time"

	"wp-llm-gas/internal/conversation"
)

// ENTREGA AGENDADA A MANO POR UN OPERADOR, desde el chat del panel (16/10).
//
// El bot ya agenda solo cuando el cliente se lo pide (programar_entrega). Esto es para cuando lo
// arregla una persona: en el chat el cliente dice "tráemelo a las 4" y el operador lo deja
// agendado sin depender de que el modelo lo entienda. Se guarda IGUAL que lo que agenda el bot
// (un ScheduledOrder), así que a la hora el mismo reloj le escribe al cliente "¿La enviamos
// ahora?" y, si dice que sí, se crea el pedido y se busca repartidor (confirmarYRegistrar).
//
// No se le escribe nada al cliente al agendar: el operador ya está hablando con él en ese chat.

// AgendaDeOperador es lo que manda el panel.
type AgendaDeOperador struct {
	Phone string
	Items []conversation.PendingWaitItem
	// IDTipoPago: la forma de pago del formulario; en cero, la de por defecto.
	IDTipoPago int
	// FechaHora: "AAAA-MM-DD HH:MM", hora de Ecuador.
	FechaHora      string
	Operador       string
	Identificacion string
	Nombres        string
}

// ResultadoAgendaOperador es lo que ve el operador en el panel.
type ResultadoAgendaOperador struct {
	OK     bool   `json:"ok"`
	Motivo string `json:"motivo,omitempty"`
	ID     int64  `json:"id,omitempty"`
	Cuando string `json:"cuando,omitempty"`
}

func noSePudoAgendar(motivo string) ResultadoAgendaOperador {
	return ResultadoAgendaOperador{OK: false, Motivo: motivo}
}

// margenParaAgendar: menos que esto es un pedido para ahora, no una entrega agendada.
const margenParaAgendar = 15 * time.Minute

// AgendarPedidoDeOperador valida y deja la entrega agendada. Si algo no corresponde devuelve el
// motivo y no guarda nada.
func (a *Agent) AgendarPedidoDeOperador(p AgendaDeOperador) ResultadoAgendaOperador {
	from := strings.TrimSpace(p.Phone)
	if from == "" {
		return noSePudoAgendar("falta el teléfono del cliente")
	}
	var items []conversation.PendingWaitItem
	for _, it := range p.Items {
		if it.Cantidad > 0 {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return noSePudoAgendar("falta el producto y la cantidad")
	}
	if len(items) > 1 {
		// Una entrega agendada guarda un solo color (como las que agenda el bot).
		return noSePudoAgendar("por ahora se agenda un solo color por entrega: deja una sola línea")
	}
	it := items[0]

	cuando, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(p.FechaHora), zonaEcuador)
	if err != nil {
		return noSePudoAgendar("falta la fecha y la hora de la entrega")
	}
	ahora := a.ahora().In(zonaEcuador)
	if cuando.Before(ahora.Add(margenParaAgendar)) {
		return noSePudoAgendar("esa hora ya pasó o está muy cerca: para ahora usa \"Crear pedido\"")
	}

	// El horario de ESE día (el del panel, con el .env de respaldo).
	f, _ := a.horarioDelDia(cuando)
	if !f.abierto {
		return noSePudoAgendar("ese día no hay servicio")
	}
	if f.desde >= 0 && f.hasta >= 0 {
		m := cuando.Hour()*60 + cuando.Minute()
		if m < f.desde || m > f.hasta {
			return noSePudoAgendar(fmt.Sprintf("ese día atendemos de %s a %s", minutosAHora(f.desde), minutosAHora(f.hasta)))
		}
	}

	// A la hora, el bot le escribe para confirmar: WhatsApp solo lo deja dentro de las 24 h de su
	// último mensaje (el reloj de los programados usa el mismo margen de 5 min).
	ultimo, hay := a.store.LastClientMessageAt(from)
	if !hay {
		return noSePudoAgendar("no hay mensajes de este cliente: el bot no podrá escribirle para confirmar")
	}
	limite := time.Unix(ultimo, 0).Add(24*time.Hour - 5*time.Minute).In(zonaEcuador)
	if cuando.After(limite) {
		return noSePudoAgendar(fmt.Sprintf("a esa hora WhatsApp ya no deja escribirle (pasan 24 h desde su "+
			"último mensaje): agenda hasta las %s o confírmalo por teléfono", limite.Format("02/01 15:04")))
	}

	// Uno por cliente: dos agendados le mandarían dos confirmaciones.
	for _, estado := range []string{conversation.SchedulePendiente, conversation.ScheduleConfirmando} {
		for _, s := range a.store.ListScheduled(estado, 500) {
			if s.Phone == from {
				return noSePudoAgendar(fmt.Sprintf("ya tiene una entrega agendada (#%d para el %s)", s.ID,
					time.Unix(s.HoraPropuesta, 0).In(zonaEcuador).Format("02/01 a las 15:04")))
			}
		}
	}

	loc, hayUbicacion := a.store.GetLocation(from)
	if !hayUbicacion {
		return noSePudoAgendar("no tenemos la ubicación del cliente en esta conversación")
	}
	if _, motivo := a.cuentaParaOperador(from, p.Identificacion, p.Nombres); motivo != "" {
		return noSePudoAgendar(motivo)
	}
	perfil, _ := a.store.GetProfile(from)

	idtipopago := p.IDTipoPago
	if idtipopago <= 0 && a.catalog != nil {
		if ctx, ok := a.catalog.Get(); ok && ctx != nil {
			idtipopago, _ = defaultPaymentID(ctx.Payments)
		}
	}
	if idtipopago <= 0 {
		return noSePudoAgendar("falta la forma de pago")
	}

	id := a.store.CreateScheduled(conversation.ScheduledOrder{
		Phone:          from,
		Identificacion: perfil.Identificacion,
		Nombres:        perfil.Nombres,
		IDCategoria:    it.IDCategoria,
		IDProducto:     it.IDProducto,
		IDColor:        it.IDColor,
		Cantidad:       it.Cantidad,
		IDTipoPago:     idtipopago,
		ProductoNombre: it.ProductoNombre,
		ColorNombre:    it.ColorNombre,
		Latitude:       loc.Latitude,
		Longitude:      loc.Longitude,
		HoraPropuesta:  cuando.Unix(),
	})
	if id <= 0 {
		return noSePudoAgendar("no se pudo guardar la entrega agendada; intenta de nuevo")
	}
	texto := cuando.Format("02/01 a las 15:04")
	quien := strings.TrimSpace(p.Operador)
	if quien == "" {
		quien = "el operador"
	}
	a.store.LogMessage(from, "system", fmt.Sprintf("📅 Entrega programada #%d por %s: %d x %s %s para el %s",
		id, quien, it.Cantidad, it.ProductoNombre, it.ColorNombre, texto))
	return ResultadoAgendaOperador{OK: true, ID: id, Cuando: texto}
}
