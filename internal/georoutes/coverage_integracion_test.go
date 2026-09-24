package georoutes

import (
	"os"
	"testing"
)

// Integración REAL contra el backend local (no el falso). Se salta salvo que
// INTEGRACION_BACKEND apunte a un backend vivo: la suite normal no depende de servicios.
//
//	INTEGRACION_BACKEND=http://127.0.0.1:8000 go test ./internal/georoutes/ -run Integracion -v
func TestIntegracionCobertura(t *testing.T) {
	base := os.Getenv("INTEGRACION_BACKEND")
	if base == "" {
		t.Skip("sin INTEGRACION_BACKEND; test de integración omitido")
	}
	c := NewClient(base)

	hay, zonas, err := c.GetCoverageZones()
	if err != nil || !hay || len(zonas) == 0 {
		t.Fatalf("GetCoverageZones: hay=%v zonas=%d err=%v", hay, len(zonas), err)
	}
	t.Logf("zonas: %s con %d parroquias", zonas[0].Zona, len(zonas[0].Parroquias))

	dentro, err := c.CheckCoverage(-2.898, -79.002, "593999INTEG")
	if err != nil || !dentro.Cubierto {
		t.Fatalf("CheckCoverage dentro: %+v err=%v", dentro, err)
	}
	t.Logf("dentro: sector=%s zona=%s", dentro.Sector, dentro.Zona)

	fuera, err := c.CheckCoverage(-0.18, -78.47, "593999INTEG")
	if err != nil || fuera.Cubierto {
		t.Fatalf("CheckCoverage fuera: %+v err=%v", fuera, err)
	}

	// Alternativas: sin conductores activos localmente la lista es vacía; lo que se valida
	// es el contrato (sin error, JSON bien formado).
	alts, err := c.CheckColorAlternatives(-2.898, -79.002, 1, 3, 1)
	if err != nil {
		t.Fatalf("CheckColorAlternatives: %v", err)
	}
	t.Logf("alternativas de NARANJA: %v", alts)

	// Equivalencias por nombre, SIN ubicación: es la consulta que el bot necesita cuando el
	// cliente pregunta por el cambio antes de compartir el pin (caso 593980787206, 24/09).
	eq, err := c.GetColorEquivalences()
	if err != nil {
		t.Fatalf("GetColorEquivalences: %v", err)
	}
	t.Logf("equivalencias: %d pares, por_color=%v", len(eq.Pares), eq.PorColor)
	// La simetría es lo que se rompe en silencio: la tabla guarda el par una sola vez, con el id
	// menor primero, así que sin ella la mitad de las consultas daría vacío y el bot diría "no
	// hacemos ese cambio" de un cambio que sí se hace.
	for color, equivalentes := range eq.PorColor {
		for _, otro := range equivalentes {
			vuelta := eq.PorColor[otro]
			var simetrico bool
			for _, v := range vuelta {
				if v == color {
					simetrico = true
				}
			}
			if !simetrico {
				t.Errorf("%s->%s existe pero %s->%s no: la equivalencia no es simétrica",
					color, otro, otro, color)
			}
		}
	}
}
