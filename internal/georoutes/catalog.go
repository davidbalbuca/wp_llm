package georoutes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// get hace un GET a path y devuelve el "resultado" del envoltorio, o un error con el
// "mensaje" del backend. Los endpoints de catálogo (GET) no requieren JWT en DEBUG.
func (c *Client) get(path string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("respuesta no válida del backend (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		mensaje := strings.TrimSpace(env.Mensaje)
		if mensaje == "" {
			mensaje = fmt.Sprintf("error del backend (HTTP %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", mensaje)
	}
	return env.Resultado, nil
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
