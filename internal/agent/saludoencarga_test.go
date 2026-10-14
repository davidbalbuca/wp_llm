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
	for _, quiero := range []string{"Soy *UbiGas*", "Tú no te preocupes, yo te busco al repartidor más cercano", "te lo envío en minutos"} {
		if !strings.Contains(saludo, quiero) {
			t.Errorf("el saludo no dice %q: %q", quiero, saludo)
		}
	}
	if strings.Contains(saludo, "recibir") {
		t.Errorf("el saludo ya no dice \"pagas al recibir\" (dueño, 13/10): %q", saludo)
	}
}

// DE NOCHE dice lo mismo (sin "en minutos", que choca con el aviso de cierre), y la app va siempre
// junto a "o también te puedo ayudar por aquí" (dueño, 14/10).
func TestDeNocheElSaludoDiceLoMismoYOfreceAyudarPorAqui(t *testing.T) {
	ag := agenteConHora(22)
	ag.store.SetLastOrder("593900960001", conversation.LastOrder{})
	saludo, hay := ag.SaludoDeBienvenida("593900960001")
	if !hay {
		t.Fatal("no se saludó")
	}
	for _, quiero := range []string{"Soy *UbiGas*.", "Tú no te preocupes, yo te busco al repartidor más cercano",
		"Ahora no estamos atendiendo", "Descarga nuestra app", "O también te puedo ayudar por aquí mismo"} {
		if !strings.Contains(saludo, quiero) {
			t.Errorf("de noche el saludo no dice %q:\n%s", quiero, saludo)
		}
	}
	if strings.Contains(saludo, "en minutos") {
		t.Errorf("de noche no se promete en minutos:\n%s", saludo)
	}
}

func TestDeDiaLaAppVaJuntoAAyudarPorAqui(t *testing.T) {
	ag := agenteConHora(10)
	saludo, _ := ag.SaludoDeBienvenida("593900960002")
	if !strings.Contains(saludo, LinkApp+"\n"+pideloAqui) {
		t.Errorf("la app y el \"por aquí\" van juntos:\n%s", saludo)
	}
}
