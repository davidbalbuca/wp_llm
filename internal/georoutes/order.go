package georoutes

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OrderProduct es una línea del pedido: producto + color + cantidad. Los IDs salen del
// catálogo (getProductXCategory). IDColor=0 se envía como null (producto sin color).
type OrderProduct struct {
	IDCategoria int
	IDProducto  int
	IDColor     int
	Cantidad    int
}

// OrderResult es la respuesta de un pedido creado en startOrder.
type OrderResult struct {
	IDPedido          int     `json:"idpedido"`
	ConductorAsignado string  `json:"conductorasignado"`
	TelefonoConductor string  `json:"telefonoconductor"`
	Total             float64 `json:"total"`
}

// WppOrder crea el pedido del BOT con la ubicación compartida por WhatsApp, SIN iddireccion:
// el backend hace upsert de la única dirección "WhatsApp" del cliente (reemplaza sus
// coordenadas) y REUTILIZA el flujo real de pedido. Endpoint exclusivo: POST /wppOrder/.
func (c *Client) WppOrder(jwt string, latitude, longitude float64, idtipopago int, productos []OrderProduct) (*OrderResult, error) {
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

	res, err := c.post("/wppOrder/", map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idtipopago": idtipopago,
		"productos":  items,
	}, jwt)
	if err != nil {
		return nil, err
	}

	var result OrderResult
	if err := json.Unmarshal(res, &result); err != nil {
		return nil, fmt.Errorf("respuesta de pedido no válida del backend: %w", err)
	}
	return &result, nil
}

// WppRegistrarPedidoNoAsignado registra un pedido que NO se pudo asignar (no hubo repartidor):
// el cliente rechazó esperar, o se vencieron los 5 min de espera sin conductor. El backend lo
// guarda en estado "no asignado" para gestión manual. Endpoint: POST /wppRegistrarPedidoNoAsignado/.
// Best-effort desde el bot (no bloquea el flujo del cliente).
func (c *Client) WppRegistrarPedidoNoAsignado(jwt string, latitude, longitude float64, idtipopago int, productos []OrderProduct) error {
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

	_, err := c.post("/wppRegistrarPedidoNoAsignado/", map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idtipopago": idtipopago,
		"productos":  items,
	}, jwt)
	return err
}

// RatingOrder registra la calificación (1-5) y un comentario opcional del cliente sobre el
// conductor de un pedido entregado (POST /georoutes/ratingOrder/), con el JWT del cliente.
func (c *Client) RatingOrder(jwt string, idpedido, calificacion int, observacion string) error {
	_, err := c.post("/ratingOrder/", map[string]any{
		"idpedido":     idpedido,
		"calificacion": calificacion,
		"observacion":  observacion,
	}, jwt)
	return err
}

// CancelOrder cancela el pedido del cliente (POST /cancelOrder/) con su JWT. El backend lo marca
// CANCELADO_CLIENTE, devuelve el stock al conductor y le avisa. Igual que el "Cancelar" de la app.
func (c *Client) CancelOrder(jwt string, idpedido int) error {
	_, err := c.post("/cancelOrder/", map[string]any{"idpedido": idpedido}, jwt)
	return err
}

// SavedDirection es una dirección que el cliente ya tiene guardada en el backend.
type SavedDirection struct {
	ID         int     `json:"id"`
	Alias      string  `json:"alias"`
	Direccion  string  `json:"direccion"`
	Referencia string  `json:"referencia"`
	Principal  bool    `json:"principal"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
}

// GetDirections devuelve las direcciones guardadas del cliente (GET /getDirectionsClient/,
// con el JWT del cliente). Sirve para ofrecérselas y que elija una sin re-compartir ubicación.
func (c *Client) GetDirections(jwt string) ([]SavedDirection, error) {
	env, status, err := c.doGet("/getDirectionsClient/", jwt)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		mensaje := strings.TrimSpace(env.Mensaje)
		if mensaje == "" {
			mensaje = fmt.Sprintf("error del backend (HTTP %d)", status)
		}
		return nil, fmt.Errorf("%s", mensaje)
	}
	var dirs []SavedDirection
	if err := json.Unmarshal(env.Resultado, &dirs); err != nil {
		return nil, fmt.Errorf("respuesta de direcciones no válida del backend: %w", err)
	}
	return dirs, nil
}

