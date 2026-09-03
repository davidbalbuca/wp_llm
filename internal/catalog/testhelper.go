package catalog

import "time"

// NewStaticForTest crea un Client con un catálogo fijo, sin backend. Solo para tests de otros
// paquetes que necesitan un catálogo determinista (no hace red).
func NewStaticForTest(ctx *Context) *Client {
	return &Client{cached: ctx, fetchedAt: time.Now(), ttl: time.Hour}
}
