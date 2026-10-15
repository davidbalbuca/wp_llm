package agent

import "testing"

// Xavi (12/10, ticket #113): ya tenía su pedido en camino, dijo "Ok listo gracias" y el bot
// contestó "Ya te aviso apenas tu repartidor esté llegando". Es lo que el bot SÍ hace (el aviso
// de llegada), pero el candado lo leyó como "le avisé al repartidor" y abrió un caso de soporte.
func TestPromesaAlClienteNoEsAvisoAlEquipo(t *testing.T) {
	for _, texto := range []string{
		"¡Con gusto, Xavi! 🙌 Ya te aviso apenas tu repartidor esté llegando. Que disfrutes tu gasecito 😊🔥",
		"Te aviso cuando tu repartidor esté cerca 😊",
		"Listo, te aviso en cuanto el repartidor salga.",
	} {
		if afirmaAvisoAlEquipo(texto) {
			t.Errorf("abriría un ticket sin motivo: %q", texto)
		}
	}
}

// Lo que SÍ es prometer un aviso que nadie hizo sigue detectándose (caso 593959499118, 12/09).
func TestAvisoInventadoAlRepartidorSeSigueDetectando(t *testing.T) {
	for _, texto := range []string{
		"Listo, ya le avisé al repartidor de tu nueva dirección 👍",
		"Ya le aviso a tu repartidor que vas a salir",
		"Le paso al repartidor tu referencia",
		"Ya avisé al equipo, pronto te contactan",
		"Un asesor se comunicará contigo",
		"El repartidor va a tu nueva dirección",
	} {
		if !afirmaAvisoAlEquipo(texto) {
			t.Errorf("no se detectó una promesa sin respaldo: %q", texto)
		}
	}
}
