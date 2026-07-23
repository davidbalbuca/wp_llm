package georoutes

import (
	"encoding/json"
	"fmt"
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

// StartOrder crea el pedido en el flujo real (POST /georoutes/startOrder/) usando el JWT
// del cliente. El backend resuelve geocerca, asigna el conductor por color/stock/cercanía
// y genera la ruta. Devuelve el conductor asignado.
func (c *Client) StartOrder(jwt string, iddireccion, idtipopago int, productos []OrderProduct) (*OrderResult, error) {
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

	res, err := c.post("/startOrder/", map[string]any{
		"iddireccion": iddireccion,
		"idtipopago":  idtipopago,
		"productos":   items,
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
