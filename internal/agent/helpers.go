package agent

import (
	"wp-llm-gas/internal/geo"
	"wp-llm-gas/internal/texto"
)

// Atajos a los paquetes puros (internal/texto, internal/geo) para que el resto del agente los
// use con nombre corto. La lógica vive allá; aquí solo se re-exporta hacia adentro.
var (
	normalizar      = texto.Normalizar
	afirmaSecuencia = texto.AfirmaSecuencia
	mismaUbicacion  = geo.MismaUbicacion
	distanciaMetros = geo.DistanciaMetros
)
