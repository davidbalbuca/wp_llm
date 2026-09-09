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
	Placa             string  `json:"placa"`
	FormaPago         string  `json:"formapago"`
	Total             float64 `json:"total"`
	// SeguimientoToken es el token firmado del enlace público de seguimiento en vivo. Lo genera
	// el backend SOLO para pedidos del bot (wpp_order). Vacío si el backend aún no lo envía
	// (compat hacia atrás): en ese caso el bot simplemente no muestra el enlace.
	SeguimientoToken string `json:"seguimiento_token"`
}

// WppOrder crea el pedido del BOT con la ubicación compartida por WhatsApp, SIN iddireccion:
// el backend hace upsert de la única dirección "WhatsApp" del cliente (reemplaza sus
// coordenadas) y REUTILIZA el flujo real de pedido. Endpoint exclusivo: POST /wppOrder/.
func (c *Client) WppOrder(jwt string, latitude, longitude float64, idtipopago int, productos []OrderProduct) (*OrderResult, error) {
	return c.WppOrderEnDireccion(jwt, latitude, longitude, idtipopago, productos, "")
}

// WppOrderEnDireccion es WppOrder apuntando a una dirección GUARDADA del cliente por su alias
// ("Casa"). Con alias vacío se comporta igual que WppOrder (dirección interna de WhatsApp, que
// se reemplaza en cada pedido); con alias el backend usa esa dirección sin sobrescribirla.
func (c *Client) WppOrderEnDireccion(jwt string, latitude, longitude float64, idtipopago int,
	productos []OrderProduct, alias string) (*OrderResult, error) {

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

	cuerpo := map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idtipopago": idtipopago,
		"productos":  items,
	}
	// Solo se manda si lo hay: un alias vacío no debe alterar el camino de siempre.
	if alias != "" {
		cuerpo["alias_direccion"] = alias
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
	// observacion SIEMPRE se envía aunque el serializer la marque required=False: el backend la
	// lee con datos['observacion'] (acceso directo), así que sin ella lanza KeyError y el bot cree
	// que falló. Pasó el 05/09 con David (loop de reasignaciones sin poder cancelar). Fix de raíz:
	// datos.get('observacion','') en el backend, deuda técnica de David.
	_, err := c.post("/cancelOrder/", map[string]any{
		"idpedido":    idpedido,
		"observacion": "Cancelado por el cliente vía WhatsApp",
	}, jwt)
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

// CreateDirection guarda una ubicación del cliente con su alias ("Casa") en el MISMO sitio que
// la app móvil (POST /createDirectionClient/).
//
// principal va SIEMPRE en false a propósito: con principal=true el backend APAGA la principal
// que el cliente tenga en la app móvil, y el bot no debe reconfigurarle la app.
func (c *Client) CreateDirection(jwt, alias, direccion string, latitude, longitude float64) error {
	_, err := c.post("/createDirectionClient/", map[string]any{
		"direccion":  direccion,
		"alias":      alias,
		"principal":  false,
		"referencia": "",
		"latitude":   latitude,
		"longitude":  longitude,
	}, jwt)
	return err
}
