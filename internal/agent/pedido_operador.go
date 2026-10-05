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
	// Identificacion y Nombres: los escribe el operador en el formulario. Los dos son opcionales:
	// sin cédula el cliente se registra por su WhatsApp, y sin nombre se usa el que dio en el
	// chat o el de su perfil si sirve. El nombre escrito corrige el del perfil ("@sd2").
	Identificacion string
	Nombres        string
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

	// Datos del cliente. La cédula es opcional desde el 04/10: el cliente se registra por su
	// número de WhatsApp. El nombre es el que escribió el operador en el formulario y, si no
	// escribió nada, el que dio el cliente en el chat o el de su WhatsApp si sirve (05/10).
	// Si dejó el nombre que venía prellenado, no se re-valida: el de un registro con cédula se
	// usa tal cual aunque no pase el filtro de nombres (así lo hace NombreUsable).
	usable := conversation.NombreUsable(a.store, from)
	escrito := ""
	if dado := strings.TrimSpace(p.Nombres); dado != "" && dado != usable {
		if escrito = conversation.NombreSiSirve(dado); escrito == "" {
			return noSePudo("el nombre escrito no parece un nombre (sin números ni símbolos): corrígelo")
		}
	}
	nombres := escrito
	if nombres == "" {
		nombres = usable
	}
	account, hayCuenta := a.store.GetAccount(from)
	if !hayCuenta {
		// Sin cuenta se crea por el MISMO camino que el pedido del bot (get-or-create en el
		// backend: si ya existe, no lo duplica).
		identificacion := strings.TrimSpace(p.Identificacion)
		if nombres == "" {
			return noSePudo("falta el nombre del cliente: escríbelo en el formulario (su WhatsApp no muestra un nombre)")
		}
		nueva, err := a.gr.WppGetOrCreateClient(identificacion, nombres, from)
		if err != nil {
			log.Printf("[pedido-operador] %s no se pudo crear la cuenta: %v", from, err)
			return noSePudo("no se pudo registrar al cliente: " + err.Error())
		}
		account = conversation.Account{Username: nueva.Username, Password: nueva.Password}
		a.store.SetAccount(from, account)
		perfil, _ := a.store.GetProfile(from)
		perfil.Identificacion, perfil.Nombres = identificacion, nombres
		a.store.SetProfile(from, perfil)
	} else if escrito != "" && escrito != usable {
		// Ya tenía cuenta y el operador corrigió el nombre (el de su perfil era "@sd2" o similar).
		// Es un extra: si falla, el pedido sigue con la cuenta de siempre. El backend solo lo
		// cambia en clientes sin cédula; el de un cliente registrado con cédula no se toca.
		if nueva, err := a.gr.WppActualizarNombre(escrito, from); err == nil && nueva != nil {
			account = conversation.Account{Username: nueva.Username, Password: nueva.Password}
			a.store.SetAccount(from, account)
			perfil, _ := a.store.GetProfile(from)
			if strings.TrimSpace(perfil.Identificacion) == "" {
				perfil.Nombres = escrito
				a.store.SetProfile(from, perfil)
			}
		} else if err != nil {
			log.Printf("[pedido-operador] %s no se pudo actualizar el nombre: %v", from, err)
		}
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
		// El backend dice el motivo cuando no son los conductores (pedido en curso, nada
		// pendiente): se le muestra tal cual al operador en vez del texto genérico.
		if motivo := strings.TrimSpace(disp.Bloqueo); motivo != "" {
			return noSePudo(motivo)
		}
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
