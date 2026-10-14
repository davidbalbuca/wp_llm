package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// EL SALUDO DICE QUE ÉL SE ENCARGA (dueño, 13/10): "tú no te preocupes, yo te busco al repartidor
// más cercano y te lo envío en minutos". Sin "pagas al recibir", y de noche sin prometer buscar.

func agenteConHora(hora int) *Agent {
	ag := agentDePrueba(nil, conversation.NewMemStore())
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "20:30", BotDiasLaborables: "1-7"}
	ag.reloj = func() time.Time { return time.Date(2026, 10, 13, hora, 0, 0, 0, zonaEcuador) }
	return ag
}

func TestElSaludoDiceQueElSeEncarga(t *testing.T) {
	saludo := textoBienvenidaA("Ana", time.Date(2026, 10, 13, 10, 0, 0, 0, zonaEcuador))
	for _, quiero := range []string{"tú no te preocupes", "yo te busco el repartidor más cercano", "te lo envío en minutos"} {
		if !strings.Contains(saludo, quiero) {
			t.Errorf("el saludo no dice %q: %q", quiero, saludo)
		}
	}
	if strings.Contains(saludo, "recibir") {
		t.Errorf("el saludo ya no dice \"pagas al recibir\" (dueño, 13/10): %q", saludo)
	}
}

func TestDeNocheElSaludoNoPrometeBuscarRepartidor(t *testing.T) {
	ag := agenteConHora(22)
	saludo := ag.textoBienvenidaSegunHorario("Ana", ag.ahora())
	if strings.Contains(saludo, "más cercano") || strings.Contains(saludo, "en minutos") {
		t.Errorf("de noche el saludo no promete buscar ni llevar en minutos: %q", saludo)
	}
	if !strings.Contains(saludo, marcaDePresentacion) {
		t.Errorf("sin la marca, saludounico.go no reconoce la presentación: %q", saludo)
	}
}
