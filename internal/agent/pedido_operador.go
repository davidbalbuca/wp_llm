package agent

import (
	"fmt"
	"log"
	"strings"
	"time"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// PEDIDO CREADO A MANO POR UN OPERADOR, desde el chat del panel.
//
// El caso real: el cliente pidió por WhatsApp, esperó, no hubo repartidor y su pedido quedó sin
// atender. Alguien del equipo lo llama, el cliente sigue queriendo el gas, y hasta ahora eso se
// resolvía por fuera del sistema (sin pedido, sin stock descontado, sin seguimiento).
//
// Lo crea el BOT y no el panel a propósito: así el bot sabe que ese pedido existe, deja de
// ofrecerle esperar o cancelar, y sigue el seguimiento con el cliente como en cualquier pedido.
//
// EL ORDEN IMPORTA, y es el que pidió David:
//  1. la ventana de tiempo (WhatsApp solo deja escribirle libremente al cliente dentro de las
//     24 h de su último mensaje);
//  2. la disponibilidad (¿hay repartidor AHORA?);
//  3. crear el pedido;
//  4. y SOLO si quedó con repartidor, hablarle al cliente.
//
// Si algo falla, el cliente NO recibe ningún mensaje y el motivo se le devuelve al panel. Es lo
// que evita que el bot prometa un pedido que no existe.

// PedidoDeOperador es lo que manda el panel.
type PedidoDeOperador struct {
	Phone string
	Items []conversation.PendingWaitItem
	// IDTipoPago es la forma de pago elegida en el formulario.
	IDTipoPago int
	// IDConductor en cero = que lo tome el más cercano, como cualquier pedido.
	IDConductor int
	// Operador es quién lo creó; queda guardado en el pedido.
	Operador string
	// MaxHoras es la ventana que el panel ya validó. En cero se usa la que diga el backend.
	MaxHoras float64
}

// ResultadoPedidoOperador es lo que ve el operador en el panel.
type ResultadoPedidoOperador struct {
	OK        bool   `json:"ok"`
	Motivo    string `json:"motivo,omitempty"`
	IDPedido  int    `json:"idpedido,omitempty"`
	Conductor string `json:"conductor,omitempty"`
}

func noSePudo(motivo string) ResultadoPedidoOperador {
	return ResultadoPedidoOperador{OK: false, Motivo: motivo}
}

// CrearPedidoDeOperador es el camino completo. Nunca le escribe al cliente si el pedido no quedó
// con repartidor.
func (a *Agent) CrearPedidoDeOperador(p PedidoDeOperador) ResultadoPedidoOperador {
	from := strings.TrimSpace(p.Phone)
	if from == "" || len(p.Items) == 0 {
		return noSePudo("faltan el teléfono o los productos")
	}

	// Datos del cliente. Un cliente sin cuenta (el bot solo guarda el perfil cuando un pedido
	// sale bien) no se puede pedir: no hay a nombre de quién. Decisión de David: en esos casos
	// el operador NO completa los datos personales desde el panel.
	account, hayCuenta := a.store.GetAccount(from)
	if !hayCuenta {
		return noSePudo("este cliente no tiene datos completos: no se le puede crear el pedido")
	}
	loc, hayUbicacion := a.store.GetLocation(from)
	if !hayUbicacion {
		return noSePudo("no tenemos la ubicación del cliente en esta conversación")
	}

	// ── 1) la ventana de tiempo ───────────────────────────────────────────────
	horas := p.MaxHoras
	if horas <= 0 {
		horas = 24 // se corrige abajo con la que diga el backend, que es la del panel
	}
	if motivo := a.fueraDeLaVentana(from, horas); motivo != "" {
		return noSePudo(motivo)
	}

	tokens, err := a.gr.Login(account.Username, account.Password)
	if err != nil {
		log.Printf("[pedido-operador] %s no se pudo entrar como el cliente: %v", from, err)
		return noSePudo("no se pudo validar la cuenta del cliente")
	}

	productos := orderProductsDeItems(p.Items)

	// ── 2) la disponibilidad ──────────────────────────────────────────────────
	disp, err := a.gr.DisponibilidadPedidoOperador(tokens.Access, loc.Latitude, loc.Longitude, productos)
	if err != nil {
		log.Printf("[pedido-operador] %s falló la disponibilidad: %v", from, err)
		return noSePudo("no se pudo comprobar si hay repartidor; intenta de nuevo")
	}
	// La ventana verdadera es la del panel: si el panel no la mandó, se revisa ahora con ella.
	if p.MaxHoras <= 0 && disp.VentanaHoras > 0 {
		if motivo := a.fueraDeLaVentana(from, float64(disp.VentanaHoras)); motivo != "" {
			return noSePudo(motivo)
		}
	}
	if !disp.Disponible {
		return noSePudo("no hay repartidor disponible con ese color en la zona")
	}
	// Si el operador eligió uno, tiene que seguir estando entre los que pueden.
	if p.IDConductor > 0 && !estaEntreLosDisponibles(p.IDConductor, disp.Conductores) {
		return noSePudo("ese repartidor ya no está disponible; elige otro")
	}

	// ── 3) crear el pedido ────────────────────────────────────────────────────
	// Si no hay quien cumpla, el backend revienta y NO deja nada creado.
	res, err := a.gr.WppOrderDeOperador(tokens.Access, loc.Latitude, loc.Longitude, p.IDTipoPago,
		productos, p.IDConductor, p.Operador)
	if err != nil {
		log.Printf("[pedido-operador] %s no se pudo crear: %v", from, err)
		return noSePudo("no se pudo asignar, intenta de nuevo")
	}
	if res == nil || res.ConductorAsignado == "" {
		// Sin repartidor no se le dice nada al cliente, aunque el backend haya respondido.
		log.Printf("[pedido-operador] %s el pedido volvió sin repartidor; no se avisa al cliente", from)
		return noSePudo("no se pudo asignar, intenta de nuevo")
	}

	// ── 4) recién ahora se le habla al cliente ────────────────────────────────
	// El bot se queda con el chat: es él quien va a contar el seguimiento.
	a.store.SetChatMode(from, conversation.ChatModeBot)
	a.store.ClearPendingWait(from) // la espera vieja ya no corre: este pedido la reemplaza

	perfil, _ := a.store.GetProfile(from)
	w := conversation.PendingWait{
		IDTipoPago:     p.IDTipoPago,
		Items:          p.Items,
		Identificacion: perfil.Identificacion,
		Nombres:        perfil.Nombres,
	}
	if len(p.Items) == 1 {
		// El mensaje de siempre lee los campos sueltos cuando el pedido es de un solo color.
		it := p.Items[0]
		w.IDCategoria, w.IDProducto, w.IDColor, w.Cantidad = it.IDCategoria, it.IDProducto, it.IDColor, it.Cantidad
		w.ProductoNombre, w.ColorNombre = it.ProductoNombre, it.ColorNombre
	}
	// El MISMO aviso que recibe cualquier cliente cuando se le asigna repartidor (pedido
	// confirmado, qué lleva, quién va y el enlace de seguimiento). A propósito no se inventa un
	// texto nuevo: el cliente no tiene por qué notar que este pedido lo creó una persona.
	a.avisarRepartidorAsignado(from, w, res)

	log.Printf("[pedido-operador] pedido %d creado por %s para %s (repartidor %s)",
		res.IDPedido, p.Operador, from, res.ConductorAsignado)
	return ResultadoPedidoOperador{OK: true, IDPedido: res.IDPedido, Conductor: res.ConductorAsignado}
}

// fueraDeLaVentana devuelve el motivo si ya no se le puede escribir al cliente, o "" si sí.
func (a *Agent) fueraDeLaVentana(from string, horas float64) string {
	ultimo, hay := a.store.LastClientMessageAt(from)
	if !hay {
		return "no hay mensajes de este cliente: no se le puede escribir"
	}
	pasadas := time.Since(time.Unix(ultimo, 0)).Hours()
	if pasadas > horas {
		return fmt.Sprintf("el cliente escribió hace %.0f h y el límite es %.0f h: WhatsApp ya no "+
			"permite escribirle, hay que confirmarlo por teléfono", pasadas, horas)
	}
	return ""
}

func estaEntreLosDisponibles(id int, lista []georoutes.ConductorDisponible) bool {
	for _, c := range lista {
		if c.IDConductor == id {
			return true
		}
	}
	return false
}

// orderProductsDeItems convierte las líneas del formulario en las del backend.
func orderProductsDeItems(items []conversation.PendingWaitItem) []georoutes.OrderProduct {
	salida := make([]georoutes.OrderProduct, 0, len(items))
	for _, it := range items {
		salida = append(salida, georoutes.OrderProduct{
			IDCategoria: it.IDCategoria,
			IDProducto:  it.IDProducto,
			IDColor:     it.IDColor,
			Cantidad:    it.Cantidad,
		})
	}
	return salida
}
