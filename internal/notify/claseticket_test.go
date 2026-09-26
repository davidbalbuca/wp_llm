// Los casos de este archivo son los MOTIVOS REALES de los 55 tickets que había en producción el
// 26/09 (3-sep a 26-sep), no categorías inventadas. 53 estaban abiertos y nadie cerraba ninguno
// porque no se podía distinguir lo urgente de lo que ni era un problema.
package notify

import "testing"

func TestClasificarLosMotivosRealesDeProduccion(t *testing.T) {
	casos := []struct {
		motivo string
		quiero ClaseTicket
	}{
		// COBERTURA: 21 de los 55. No es un bug — nadie puede "resolver" que no había repartidor.
		{"Pedido sin conductor", ClaseCobertura},
		{"Pedido sin conductor disponible", ClaseCobertura},

		// BUG: falla el código. Varios ya están arreglados y sus tickets seguían abiertos.
		{"Error técnico del agente", ClaseBug},
		{"Error técnico al registrar pedido: no se puede asignar geocerca", ClaseBug},
		{"Fallo al registrar un pedido", ClaseBug},
		{"Error técnico al registrar el pedido", ClaseBug},
		{"Pedido activo huérfano", ClaseBug},

		// CLIENTE: personas esperando. Los urgentes, y estaban enterrados entre los otros 45.
		{"El bot prometió al cliente que el equipo lo contactaría", ClaseCliente},
		{"Pedido explícito del cliente de hablar con una persona real", ClaseCliente},
		{"Cliente solicita cambiar forma de pago a transferencia", ClaseCliente},
		{"El repartidor no llegó a la entrega programada", ClaseCliente},
		{"Pedido programado para las 18:30 nunca llegó y el cliente no fue avisado", ClaseCliente},
		{"El cliente reporta que el pedido no fue asignado a repartidor", ClaseCliente},
		{"El cliente insiste en pedir gas color negro", ClaseCliente},
		{"El cliente solicita información sobre cobertura en el centro", ClaseCliente},
		{"Problema grave no resuelto con herramientas: el repartidor no llega", ClaseCliente},
		{"No se pudo cancelar el pedido", ClaseCliente},
	}
	for _, c := range casos {
		if got := ClaseDeTicket(c.motivo); got != c.quiero {
			t.Errorf("%q -> %s, esperado %s", c.motivo, got, c.quiero)
		}
	}
}

// "cobertura en el centro" lleva la palabra cobertura pero es una PREGUNTA de un cliente, no falta
// de repartidor. Si la clasificación mirara subcadenas sueltas, este caso iría a la cola muda y el
// cliente se quedaría sin respuesta.
func TestLaPalabraCoberturaNoBastaParaEnterrarUnCaso(t *testing.T) {
	if got := ClaseDeTicket("El cliente solicita información sobre cobertura en el centro"); got != ClaseCliente {
		t.Errorf("una pregunta sobre cobertura es un cliente esperando, no un dato: %s", got)
	}
}

// Ante la duda, una persona. Equivocarse hacia "que lo mire alguien" cuesta una revisión;
// equivocarse hacia "es cobertura" deja a alguien esperando para siempre.
func TestUnMotivoDesconocidoVaALaColaHumana(t *testing.T) {
	for _, m := range []string{"", "algo raro pasó", "motivo que nadie previó"} {
		if got := ClaseDeTicket(m); got != ClaseCliente {
			t.Errorf("%q -> %s; ante la duda debe atenderlo una persona", m, got)
		}
	}
}

func TestSoloLaClaseClienteEsperaAUnaPersona(t *testing.T) {
	if !ClaseCliente.EsperaAUnaPersona() {
		t.Error("ClaseCliente debe esperar a una persona")
	}
	if ClaseCobertura.EsperaAUnaPersona() || ClaseBug.EsperaAUnaPersona() {
		t.Error("cobertura y bug NO van a la cola humana: por eso se ahogaban los ~8 urgentes")
	}
}

// LOS TRES TELÉFONOS ROTOS DE PRODUCCIÓN.
//
// "+593" no se puede llamar; "0991803684" y "0992883555" son los MISMOS clientes que aparecen
// como 593991803684 y 593992883555, así que sus casos quedaron partidos en dos identidades: a
// 593991803684 le fallamos 10 veces repartidas entre ambas.
func TestNormalizarLosTelefonosRotosDeProduccion(t *testing.T) {
	casos := []struct{ in, quiero string }{
		{"+593", ""},                         // solo el prefijo: no hay a quién llamar
		{"0991803684", "593991803684"},       // formato local -> E.164
		{"0992883555", "593992883555"},       // idem
		{"593991803684", "593991803684"},     // ya correcto: no se toca
		{"991803684", "593991803684"},        // sin cero ni país
		{"+593 99 180 3684", "593991803684"}, // con espacios y '+'
		{"(0)99-180-3684", "593991803684"},   // con separadores
		{"", ""},                             // vacío
		{"593", ""},                          // país sin número
		{"5939918", ""},                      // demasiado corto para ser celular
		{"59399180368412345", ""},            // demasiado largo
	}
	for _, c := range casos {
		if got := normalizarTelefonoEC(c.in); got != c.quiero {
			t.Errorf("normalizarTelefonoEC(%q) = %q, esperado %q", c.in, got, c.quiero)
		}
	}
}

// Los dos formatos del MISMO cliente tienen que colapsar en una sola clave, o sus tickets siguen
// contándose como de dos personas distintas.
func TestLosDosFormatosDelMismoClienteColapsan(t *testing.T) {
	if normalizarTelefonoEC("0991803684") != normalizarTelefonoEC("593991803684") {
		t.Error("el mismo cliente sigue partido en dos identidades")
	}
}
