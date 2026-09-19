package georoutes

import (
	"encoding/json"
	"fmt"
)

// Búsqueda de conductor en el BACKEND.
//
// Antes la espera vivía acá: el bot reintentaba el pedido completo cada 30 s durante 5 minutos
// escritos en su código. Eso dejaba al backend sin saber que un cliente estaba esperando (y por
// eso no podía avisarle por push a los conductores desconectados), los minutos no se podían
// cambiar desde el panel, y un reinicio del bot se llevaba la espera.
//
// Ahora el backend registra la búsqueda y el bot solo pregunta el estado. Los tiempos y a quién
// se avisa se configuran en el panel; acá no hay ningún número de minutos.

// Estados que devuelve el backend.
const (
	BusquedaBuscando     = "BUSCANDO"      // los primeros minutos
	BusquedaSinConductor = "SIN_CONDUCTOR" // venció la búsqueda inicial; espera la decisión
	BusquedaEsperando    = "ESPERANDO"     // el cliente aceptó esperar
	BusquedaAsignado     = "ASIGNADO"      // hay conductor: el pedido está hecho
	BusquedaNoAsignado   = "NO_ASIGNADO"   // se agotó; el backend lo dejó en gestión manual
	BusquedaCancelado    = "CANCELADO"     // lo canceló el cliente, o no contestó
)

// BusquedaResult es la respuesta de todas las APIs de búsqueda.
type BusquedaResult struct {
	IDBusqueda int    `json:"idbusqueda"`
	Estado     string `json:"estado"`
	// SegundosRestantes de la etapa actual; null cuando ya terminó. OJO: no significa lo mismo en
	// cada etapa. En SIN_CONDUCTOR son los segundos que tiene el cliente para contestar, NO la
	// espera; para eso está EsperaSegundos.
	SegundosRestantes *int `json:"segundos_restantes"`
	// EsperaSegundos es cuánto duraría la espera si el cliente acepta. Lo decide el backend (se
	// cambia desde el panel) y es el número que se le dice al cliente en la pregunta: el bot no
	// tiene ningún plazo escrito.
	EsperaSegundos int `json:"espera_segundos"`
	// Pedido llega SOLO con estado ASIGNADO, con la misma forma que el pedido normal.
	Pedido  *OrderResult `json:"pedido"`
	Mensaje string       `json:"mensaje"`
}

// Terminada indica que ya no hay nada que esperar.
func (b BusquedaResult) Terminada() bool {
	return b.Estado == BusquedaAsignado || b.Estado == BusquedaNoAsignado || b.Estado == BusquedaCancelado
}

func itemsDeProductos(productos []OrderProduct) []map[string]any {
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

func (c *Client) busqueda(path string, cuerpo map[string]any, jwt string) (*BusquedaResult, error) {
	res, err := c.post(path, cuerpo, jwt)
	if err != nil {
		return nil, err
	}
	var result BusquedaResult
	if err := json.Unmarshal(res, &result); err != nil {
		return nil, fmt.Errorf("respuesta de búsqueda no válida del backend: %w", err)
	}
	return &result, nil
}

// BuscarConductor pide el gas. Si hay conductor devuelve ASIGNADO con el pedido ya hecho; si no,
// BUSCANDO, y el backend sigue intentando. Endpoint: POST /buscarConductor/.
func (c *Client) BuscarConductor(jwt string, latitude, longitude float64, idtipopago int,
	productos []OrderProduct, alias string) (*BusquedaResult, error) {

	cuerpo := map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idtipopago": idtipopago,
		"productos":  itemsDeProductos(productos),
	}
	// Igual que en el pedido: un alias vacío no debe alterar el camino de siempre.
	if alias != "" {
		cuerpo["alias_direccion"] = alias
	}
	return c.busqueda("/buscarConductor/", cuerpo, jwt)
}

// EstadoBusqueda consulta cómo va. Cada consulta hace avanzar la búsqueda en el backend
// (reintenta asignar, avisa a los conductores y cierra las etapas vencidas).
func (c *Client) EstadoBusqueda(jwt string, idbusqueda int) (*BusquedaResult, error) {
	return c.busqueda("/estadoBusquedaConductor/", map[string]any{"idbusqueda": idbusqueda}, jwt)
}

// DecisionBusqueda responde si el cliente quiere esperar. Con esperar=false el backend deja el
// pedido en no asignados (gestión manual), igual que hacía el bot antes por su cuenta.
func (c *Client) DecisionBusqueda(jwt string, idbusqueda int, esperar bool) (*BusquedaResult, error) {
	return c.busqueda("/decisionBusquedaConductor/", map[string]any{
		"idbusqueda": idbusqueda,
		"esperar":    esperar,
	}, jwt)
}

// CancelarBusqueda corta la búsqueda: fue decisión del cliente, así que NO queda en no asignados.
func (c *Client) CancelarBusqueda(jwt string, idbusqueda int) (*BusquedaResult, error) {
	return c.busqueda("/cancelarBusquedaConductor/", map[string]any{"idbusqueda": idbusqueda}, jwt)
}
