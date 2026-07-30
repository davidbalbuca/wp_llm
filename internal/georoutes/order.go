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

// NearbyDir indica si el cliente ya tiene una dirección guardada cercana a una ubicación.
type NearbyDir struct {
	Existe      bool `json:"existe"`
	IDDireccion *int `json:"iddireccion"`
}

// NearbyDirection consulta si el cliente (JWT) ya tiene una dirección guardada CERCANA a la
// ubicación dada (GET /georoutes/nearby-direction/). El backend decide la cercanía por sector
// + distancia geodésica (tolerancia DISTANCIA_DIRECCIONES). Sirve para reutilizar la dirección
// en vez de crear una nueva por cada pedido, igual que la app. Usa el JWT del cliente (no el
// token de servicio), por eso va por doGet con bearer explícito.
func (c *Client) NearbyDirection(jwt string, latitude, longitude float64) (*NearbyDir, error) {
	path := fmt.Sprintf("/nearby-direction/?latitude=%f&longitude=%f", latitude, longitude)
	env, status, err := c.doGet(path, jwt)
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
	var nearby NearbyDir
	if err := json.Unmarshal(env.Resultado, &nearby); err != nil {
		return nil, fmt.Errorf("respuesta nearby-direction no válida del backend: %w", err)
	}
	return &nearby, nil
}
