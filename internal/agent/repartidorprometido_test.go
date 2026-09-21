package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// NO SE PROMETE UN REPARTIDOR QUE TODAVÍA SE ESTÁ BUSCANDO.
//
// El fin de semana del 19-21/09, 13 pedidos murieron en la cola sin conductor (contra 11 que sí
// consiguieron uno). Eso es un problema de flota y no se arregla desde el bot. Lo que SÍ es
// nuestro es lo que se le dice al cliente mientras tanto, y ahí el bot mintió doce veces.
//
// EL CASO NANCY (593939943691, 20/09), tal cual pasó:
//
//	11:20:45  bot    → "Tu pedido está registrado 👍 Estamos buscando al repartidor más cercano"
//	11:21:23  Nancy  → "Por favor necesito que me confirme si viene gracias"
//	11:21:25  bot    → "Aquí está la confirmación 🚚 ... Tu repartidor está en camino"   ← MENTIRA
//	11:27:19  sistema→ Ticket #35 — Pedido sin conductor
//
// Seis minutos después de que le dijeran que su repartidor venía, le dijeron que no había
// ninguno. Nancy canceló y rechazó hasta el cupón de compensación que le ofrecieron después.
//
// POR QUÉ EL CANDADO DEL PEDIDO FANTASMA NO LO ATRAPA. Ese candado pregunta "¿el pedido
// EXISTE?", y el de Nancy existía: se registró a las 11:20:45. Lo que faltaba preguntar es otra
// cosa — "¿tiene CONDUCTOR?"—. Un pedido en la cola de espera está vivo y sin repartidor a la
// vez, y en ese hueco cabían las doce promesas.
//
// La diferencia entre "lo estamos buscando" y "está en camino" es la diferencia entre un cliente
// que espera tranquilo y uno que sale a la puerta a mirar.

// clienteEsperandoConductor deja al cliente como Nancy a las 11:21: con el pedido registrado y
// en la cola, sin repartidor asignado.
func clienteEsperandoConductor(store conversation.Store, from string) {
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 2, IDTipoPago: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO",
		Identificacion: "0102307204", Nombres: "Nancy Pacheco",
	})
}

func TestNoSePrometeRepartidorMientrasSeEstaBuscando(t *testing.T) {
	// Lo que el modelo escribió en producción, y variantes de la misma mentira.
	promesas := []string{
		"¡Claro, Nancy! Aquí está la confirmación de tu pedido 🚚\n\n2 cilindros GAS 15KG BLANCO\n" +
			"💰 Valor a pagar: $6.50\n\nTu repartidor está en camino.",
		"El repartidor está en camino hacia ti 🚚",
		"Tu pedido ya está en camino, llega en unos 30 a 45 minutos aproximadamente 😊",
		"Ya tienes repartidor asignado, sale con tu pedido en breve.",
		"Tu gas llega en 30 minutos 🚚",
	}
	for _, promesa := range promesas {
		t.Run(conversation.Recortar(promesa, 40), func(t *testing.T) {
			const from = "593939943691"
			store := conversation.NewMemStore()
			ag := agentDePrueba(nil, store)
			clienteEsperandoConductor(store, from)

			salida := ag.revisarRepartidorPrometido(from, promesa)

			bajo := normalizar(salida)
			if strings.Contains(bajo, "en camino") || strings.Contains(bajo, "asignado") {
				t.Errorf("se le prometió un repartidor que todavía se está buscando: %q", salida)
			}
			// Y lo que sale tiene que decirle la VERDAD: que se está buscando.
			if !strings.Contains(bajo, "buscando") {
				t.Errorf("no se le explica que el repartidor se está buscando: %q", salida)
			}
		})
	}
}

// LA CONTRACARA, imprescindible: con el conductor YA ASIGNADO la frase es cierta y tiene que
// salir tal cual. Sin este test, un mutante que tape SIEMPRE la palabra "en camino" —y que
// dejaría al bot sin poder avisar nunca que el gas va llegando— sobreviviría.
func TestConConductorAsignadoSiSePuedeDecirQueVieneEnCamino(t *testing.T) {
	const from = "593999700001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	// Pedido activo CON conductor: ya no está en la cola de espera.
	store.SetActivePedido(from, 271)

	const promesa = "¡Tu pedido está confirmado! Tu repartidor JUAN PICON está en camino 🚚"
	salida := ag.revisarRepartidorPrometido(from, promesa)

	if salida != promesa {
		t.Errorf("se tapó un aviso VERDADERO de repartidor en camino.\nse esperaba: %q\nsalió:      %q",
			promesa, salida)
	}
}

// Y sin pedido ninguno tampoco se toca nada: de eso se ocupa el candado del pedido fantasma, que
// ya existe. Dos candados pisando el mismo texto se estorban.
func TestSinPedidoEsteCandadoNoSeMete(t *testing.T) {
	const from = "593999700002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	const texto = "Tu repartidor está en camino 🚚"
	if salida := ag.revisarRepartidorPrometido(from, texto); salida != texto {
		t.Errorf("el candado actuó sin pedido en espera: %q", salida)
	}
}

// Un mensaje NORMAL durante la espera no se toca. El candado solo puede morder la promesa
// concreta; si mordiera de más, el bot dejaría de poder conversar mientras busca conductor.
func TestDuranteLaEsperaLosMensajesNormalesPasanIntactos(t *testing.T) {
	const from = "593999700003"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteEsperandoConductor(store, from)

	normales := []string{
		"Estamos buscando al repartidor más cercano, te aviso apenas se asigne 😊",
		"El GAS 15KG cuesta $3.25 por cilindro, e incluye el envío.",
		"Claro, puedes cancelar cuando quieras escribiendo \"cancelar\".",
		"¡De nada! Aquí estoy para lo que necesites 😊",
	}
	for _, texto := range normales {
		if salida := ag.revisarRepartidorPrometido(from, texto); salida != texto {
			t.Errorf("se tocó un mensaje normal.\nentró: %q\nsalió: %q", texto, salida)
		}
	}
}

// Y el candado tiene que estar CABLEADO en el turno, no solo existir. Es el mutante clásico:
// la función correcta que nadie llama.
func TestElCandadoDelRepartidorEstaCableado(t *testing.T) {
	if !archivoContiene(t, "agent.go", "revisarRepartidorPrometido(") {
		t.Error("revisarRepartidorPrometido no se llama desde el turno: el candado existe pero " +
			"no protege nada")
	}
}
