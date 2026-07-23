// Package catalog obtiene del backend GEOWARE (georoutes) el catálogo del negocio
// (productos con colores/marcas y precio, y formas de pago) y lo cachea con un TTL.
//
// Así el agente refleja los cambios hechos en el backend (nuevos productos, precios,
// colores o formas de pago) sin reiniciarse y sin duplicar esos datos en su código: la
// única fuente de verdad es la base de datos de `ubi-geoware`.
package catalog

import (
	"log"
	"sync"
	"time"

	"wp-llm-gas/internal/georoutes"
)

// Context es el catálogo vigente que consume el agente para armar su prompt de servicio
// y para mapear el color elegido por el cliente a los IDs que exige el pedido.
type Context struct {
	Business georoutes.Business
	Products []georoutes.Product
	Payments []georoutes.Payment
}

// Client obtiene y cachea el catálogo del backend (vía el cliente georoutes).
type Client struct {
	gr        *georoutes.Client
	ttl       time.Duration
	mu        sync.Mutex
	cached    *Context
	fetchedAt time.Time
}

// NewClient crea un cliente de catálogo que consulta el backend georoutes y cachea ttl.
func NewClient(gr *georoutes.Client, ttl time.Duration) *Client {
	return &Client{gr: gr, ttl: ttl}
}

// Get devuelve el catálogo vigente. Si la caché está fresca, la reutiliza; si venció,
// intenta refrescar y, si falla, conserva el último valor conocido. El segundo valor
// indica si hay catálogo disponible (aunque provenga de la caché).
func (c *Client) Get() (*Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cached != nil && time.Since(c.fetchedAt) < c.ttl {
		return c.cached, true
	}

	fresh, err := c.fetch()
	if err != nil {
		if c.cached != nil {
			log.Printf("[catalog] fallo al refrescar, uso caché previa: %v", err)
			return c.cached, true
		}
		log.Printf("[catalog] sin catálogo disponible: %v", err)
		return nil, false
	}

	c.cached = fresh
	c.fetchedAt = time.Now()
	log.Printf("[catalog] catálogo actualizado: %d producto(s), %d forma(s) de pago",
		len(fresh.Products), len(fresh.Payments))
	return fresh, true
}

func (c *Client) fetch() (*Context, error) {
	products, err := c.gr.GetProducts()
	if err != nil {
		return nil, err
	}
	payments, err := c.gr.GetPayments()
	if err != nil {
		return nil, err
	}
	// Los textos del negocio son secundarios: si fallan, seguimos con el catálogo.
	var business georoutes.Business
	if info, err := c.gr.GetBusinessInfo(); err == nil {
		business = *info
	} else {
		log.Printf("[catalog] sin configuración del negocio: %v", err)
	}
	return &Context{Business: business, Products: products, Payments: payments}, nil
}
