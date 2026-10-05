package agent

import (
	"strings"
	"testing"
)

// 05/10: la reasignación y la entrega agendada avisan IGUAL que una asignación normal.
func TestMensajeRepartidorCompleto(t *testing.T) {
	m := MensajeRepartidor("Juan de Dios Fajardo", "ABF-2832", 14, "https://ws11.geoware.lat/seguimiento/x/")
	for _, esperado := range []string{"Juan de Dios Fajardo", "ABF-2832", "14 minutos", "/seguimiento/x/"} {
		if !strings.Contains(m, esperado) {
			t.Errorf("falta %q en:\n%s", esperado, m)
		}
	}
}

// Lo que no se tiene no se inventa: sin placa, sin minutos o sin enlace, esa línea no sale.
func TestMensajeRepartidorSinDatos(t *testing.T) {
	m := MensajeRepartidor("Pedro", "", 0, "")
	if strings.Contains(m, "Placa") || strings.Contains(m, "minutos") || strings.Contains(m, "seguimiento") {
		t.Errorf("prometió algo que no hay: %q", m)
	}
	if MensajeReasignado("", "", 0, "") == "" {
		t.Error("sin datos igual hay que avisar la reasignación")
	}
}
