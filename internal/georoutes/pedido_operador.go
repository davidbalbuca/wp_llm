package georoutes

import (
	"encoding/json"
	"fmt"
)

// PEDIDO CREADO A MANO POR UN OPERADOR, desde el chat del panel.
//
// El caso: el cliente pidió por WhatsApp, esperó, no hubo repartidor y su pedido quedó sin
// atender. Alguien del equipo lo llama por teléfono, el cliente sigue queriendo el gas, y hasta
// ahora eso se gestionaba por fuera del sistema. El pedido lo crea el BOT (no el panel) para que
// sepa que existe y pueda seguir el seguimiento con el cliente.
//
// Son los MISMOS endpoints del pedido normal: el pedido nace igual que cualquier otro (respeta
// cobertura, stock y asignación). Lo único que se agrega es a quién asignárselo, si el operador
// eligió uno, y quién lo creó.

// ConductorDisponible es un repartidor que PODRÍA llevarse el pedido ahora mismo.
type ConductorDisponible struct {
	IDConductor int    `json:"idconductor"`
	Nombres     string `json:"nombres"`
	Sector      string `json:"sector"`
}

// Disponibilidad responde si el pedido se puede crear en este momento.
type Disponibilidad struct {
	Disponible   bool                  `json:"disponible"`
	Conductores  []ConductorDisponible `json:"conductores"`
	VentanaHoras int                   `json:"ventana_horas"`
}

// DisponibilidadPedidoOperador pregunta al backend si AHORA hay repartidor para esa ubicación y
// esos productos. El panel ya preguntó antes de mostrar el botón; el bot vuelve a preguntar
// porque en ese minuto el repartidor pudo haberse puesto fuera de servicio.
//
// La ventana de horas la decide el PANEL y viene en la respuesta: el bot no tiene ningún plazo
// escrito, igual que con los minutos de la espera.
func (c *Client) DisponibilidadPedidoOperador(jwt string, latitude, longitude float64,
	productos []OrderProduct) (*Disponibilidad, error) {

	cuerpo := map[string]any{
		"latitude":  latitude,
		"longitude": longitude,
		"productos": itemsDePedido(productos),
	}
	res, err := c.post("/disponibilidadPedidoOperador/", cuerpo, jwt)
	if err != nil {
		return nil, err
	}
	var salida Disponibilidad
	if err := json.Unmarshal(res, &salida); err != nil {
		return nil, fmt.Errorf("respuesta de disponibilidad no válida del backend: %w", err)
	}
	return &salida, nil
}

// WppOrderDeOperador crea el pedido con la ubicación guardada del cliente, marcándolo como
// creado por un operador. Con idconductor va para ese repartidor; con cero, el backend lo asigna
// al más cercano, como cualquier pedido.
//
// Si no hay repartidor que cumpla, el backend REVIENTA y no deja nada creado (el pedido se crea
// dentro de una transacción). Eso es justo lo que se quiere: el bot no puede decirle al cliente
// que su pedido va en camino si no hay quien lo lleve.
func (c *Client) WppOrderDeOperador(jwt string, latitude, longitude float64, idtipopago int,
	productos []OrderProduct, idconductor int, operador string) (*OrderResult, error) {

	cuerpo := map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idtipopago": idtipopago,
		"productos":  itemsDePedido(productos),
		"operador":   operador,
	}
	if idconductor > 0 {
		cuerpo["idconductor"] = idconductor
	}
	res, err := c.post("/wppOrder/", cuerpo, jwt)
	if err != nil {
		return nil, err
	}
	var result OrderResult
	if err := json.Unmarshal(res, &result); err != nil {
		return nil, fmt.Errorf("respuesta de pedido no válida del backend: %w", err)
	}
	return &result, nil
}

// itemsDePedido arma las líneas como las espera el backend (idcolor nulo cuando el producto no
// tiene color). Es la misma forma que usan WppOrder y el registro de no asignados.
func itemsDePedido(productos []OrderProduct) []map[string]any {
	items := make([]map[string]any, 0, len(productos))
	for _, p := range productos {
		item := map[string]any{
			"idcategoria": p.IDCategoria,
			"idproducto":  p.IDProducto,
			"cantidad":    p.Cantidad,
			"idcolor":     nil,
		}
		if p.IDColor > 0 {
			item["idcolor"] = p.IDColor
		}
		items = append(items, item)
	}
	return items
}
