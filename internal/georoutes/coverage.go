package georoutes

import (
	"encoding/json"
	"fmt"
)

// Cobertura consultable y color alterno (specs/cobertura-y-color-alterno.md). Los tres
// endpoints son de solo lectura y sin JWT: información pública, como el catálogo.

// ZonaCobertura es una zona atendida con sus parroquias, tal como la devuelve el backend.
type ZonaCobertura struct {
	Zona       string   `json:"zona"`
	Parroquias []string `json:"parroquias"`
}

type respuestaZonas struct {
	HayCobertura bool            `json:"hay_cobertura"`
	Zonas        []ZonaCobertura `json:"zonas"`
}

// GetCoverageZones trae las zonas con cobertura activa (GET /getCoverageZones/).
func (c *Client) GetCoverageZones() (bool, []ZonaCobertura, error) {
	res, err := c.get("/getCoverageZones/")
	if err != nil {
		return false, nil, err
	}
	var data respuestaZonas
	if err := json.Unmarshal(res, &data); err != nil {
		return false, nil, fmt.Errorf("respuesta de zonas no válida del backend: %w", err)
	}
	return data.HayCobertura, data.Zonas, nil
}

// ResultadoCobertura dice si un punto cae dentro de un sector activo.
type ResultadoCobertura struct {
	Cubierto bool   `json:"cubierto"`
	Sector   string `json:"sector"`
	Zona     string `json:"zona"`
}

// CheckCoverage verifica la ubicación ANTES de seguir con el pedido (POST /checkCoverage/). El
// teléfono viaja para que, fuera de zona, el backend registre la demanda (DemandaFueraDeZona).
func (c *Client) CheckCoverage(latitude, longitude float64, telefono string) (ResultadoCobertura, error) {
	res, err := c.post("/checkCoverage/", map[string]any{
		"latitude":  latitude,
		"longitude": longitude,
		"telefono":  telefono,
	}, "")
	if err != nil {
		return ResultadoCobertura{}, err
	}
	var data ResultadoCobertura
	if err := json.Unmarshal(res, &data); err != nil {
		return ResultadoCobertura{}, fmt.Errorf("respuesta de cobertura no válida del backend: %w", err)
	}
	return data, nil
}

// AlternativaColor es un color equivalente al pedido que SÍ tiene conductor con stock.
type AlternativaColor struct {
	IDColor     int    `json:"idcolor"`
	Color       string `json:"color"`
	Conductores int    `json:"conductores"`
}

type respuestaAlternativas struct {
	Alternativas []AlternativaColor `json:"alternativas"`
}

// CheckColorAlternatives consulta, tras un pedido sin conductor, si algún color equivalente SÍ
// tiene conductor (POST /checkColorAlternatives/). Usa la misma función de las 5 compuertas, así
// que una alternativa devuelta es real: conductor activo, en zona, con producto y stock.
func (c *Client) CheckColorAlternatives(latitude, longitude float64, idproducto, idcolor, cantidad int) ([]AlternativaColor, error) {
	res, err := c.post("/checkColorAlternatives/", map[string]any{
		"latitude":   latitude,
		"longitude":  longitude,
		"idproducto": idproducto,
		"idcolor":    idcolor,
		"cantidad":   cantidad,
	}, "")
	if err != nil {
		return nil, err
	}
	var data respuestaAlternativas
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, fmt.Errorf("respuesta de alternativas no válida del backend: %w", err)
	}
	return data.Alternativas, nil
}
