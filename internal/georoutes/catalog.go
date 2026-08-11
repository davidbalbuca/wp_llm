package georoutes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// get hace un GET a path y devuelve el "resultado" del envoltorio, o un error con el
// "mensaje" del backend. Adjunta el JWT de la cuenta de servicio cuando está configurada
// (necesario en prod con DEBUG=False). Si el backend responde 401 con un token cacheado,
// re-autentica una vez y reintenta (el JWT pudo haber vencido).
func (c *Client) get(path string) (json.RawMessage, error) {
	token, err := c.serviceToken(false)
	if err != nil {
		return nil, err
	}

	res, status, derr := c.doGet(path, token)
	// 401 con token: el JWT probablemente venció. Reintentamos con un login forzado ANTES de
	// rendirnos, AUNQUE el cuerpo del 401 no sea JSON (derr != nil por el decode fallido). Antes
	// el `if err != nil { return }` salía primero y este reintento NUNCA se alcanzaba: el bot se
	// quedaba pegado en 401 para siempre y el catálogo jamás se refrescaba hasta reiniciar el
	// contenedor (precios/descripciones cambiados en el backend no se reflejaban).
	if status == http.StatusUnauthorized && token != "" {
		token, err = c.serviceToken(true)
		if err != nil {
			return nil, err
		}
		res, status, derr = c.doGet(path, token)
	}
	if derr != nil {
		return nil, derr
	}

	if status < 200 || status >= 300 {
		mensaje := strings.TrimSpace(res.Mensaje)
		if mensaje == "" {
			mensaje = fmt.Sprintf("error del backend (HTTP %d)", status)
		}
		return nil, fmt.Errorf("%s", mensaje)
	}
	return res.Resultado, nil
}

// doGet ejecuta un GET con un bearer opcional y devuelve el envoltorio y el status HTTP.
func (c *Client) doGet(path, bearer string) (envelope, int, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return envelope{}, 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return envelope{}, 0, err
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return envelope{}, resp.StatusCode, fmt.Errorf("respuesta no válida del backend (HTTP %d)", resp.StatusCode)
	}
	return env, resp.StatusCode, nil
}

// Color es un color/marca de cilindro (p. ej. Duragas=amarillo, Austrogas=blanco).
type Color struct {
	ID     int    `json:"id"`
	Nombre string `json:"nombre"`
}

// Product es un producto del catálogo con su precio y los colores/marcas disponibles.
type Product struct {
	IDCategoria    int     `json:"idcategoria"`
	IDProducto     int     `json:"idproducto"`
	Nombre         string  `json:"nombre"`
	PrecioUnitario float64 `json:"precio_unitario"`
	Colores        []Color `json:"colores"`
}

// Payment es una forma de pago (TipoPago) del backend.
type Payment struct {
	ID     int    `json:"id"`
	Nombre string `json:"nombre"`
}

// GetProducts devuelve el catálogo de productos con colores y precio (getProductXCategory).
func (c *Client) GetProducts() ([]Product, error) {
	res, err := c.get("/getProductXCategory/")
	if err != nil {
		return nil, err
	}
	var products []Product
	if err := json.Unmarshal(res, &products); err != nil {
		return nil, fmt.Errorf("catálogo no válido del backend: %w", err)
	}
	return products, nil
}

// Business son los textos editables del negocio (modelo ConfiguracionNegocio del backend).
type Business struct {
	Nombre         string `json:"nombre"`
	Telefono       string `json:"telefono"`
	Horario        string `json:"horario"`
	FormasPago     string `json:"formas_pago"`
	TiemposEntrega string `json:"tiempos_entrega"`
	Seguridad      string `json:"seguridad"`
	Adicional      string `json:"adicional"`
}

// GetBusinessInfo devuelve la configuración del negocio (businessInfo).
func (c *Client) GetBusinessInfo() (*Business, error) {
	res, err := c.get("/businessInfo/")
	if err != nil {
		return nil, err
	}
	var business Business
	if err := json.Unmarshal(res, &business); err != nil {
		return nil, fmt.Errorf("configuración del negocio no válida: %w", err)
	}
	return &business, nil
}

// GetPayments devuelve las formas de pago disponibles (getPayments).
func (c *Client) GetPayments() ([]Payment, error) {
	res, err := c.get("/getPayments/")
	if err != nil {
		return nil, err
	}
	var payments []Payment
	if err := json.Unmarshal(res, &payments); err != nil {
		return nil, fmt.Errorf("formas de pago no válidas del backend: %w", err)
	}
	return payments, nil
}
