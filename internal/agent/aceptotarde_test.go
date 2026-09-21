package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// EL "SÍ, ACEPTO" QUE LLEGA TARDE.
//
// Lo que parecía un problema de turnos cruzados (mensajes que se pisan) resultó ser otra cosa al
// mirar los segundos en el log: los mensajes están bien ordenados. Lo que pasa es que **el botón
// del menú se queda en la pantalla del cliente** y lo toca cuando ya avanzó por otro camino.
//
// EL CASO JESSICA (593968179884, 20/09), con los segundos:
//
//	08:31:01  bot     → menú de consentimiento
//	08:31:19  Jessica → "1600670614"        (su cédula, ignorando el menú)
//	08:31:21  bot     → "¡Listo! Ahora, ¿cuál es tu nombre completo?"
//	08:31:22  Jessica → "Sí, acepto"        (ahora sí toca el botón, que seguía ahí)
//	08:31:22  bot     → "¡Gracias! Ahora sí, ¿me compartes tu número de cédula?"   ← YA LA TIENE
//	08:31:33  Jessica → "Jessica Ocaña"     (contestando a la pregunta del NOMBRE, de hace 12 s)
//	08:31:35  bot     → "Uy, disculpa, no me llegó tu ubicación"
//
// Cuatro turnos cruzados seguidos, y el origen es uno solo: la respuesta al consentimiento está
// QUEMADA y da por hecho que la cédula todavía no llegó. Cuando ya está, el bot la vuelve a
// pedir, el cliente contesta a la pregunta anterior, y a partir de ahí los dos van desfasados.
//
// PASÓ EN 9 DE LOS 15 CONSENTIMIENTOS de producción. El peor es Carlos (593986140905, 21/09
// 09:07:42): su pedido YA estaba registrado y el bot le pidió la cédula otra vez.
//
// La respuesta tiene que mirar EN QUÉ PUNTO está el pedido, no suponerlo.

// clienteQueYaDioLaCedula deja el estado como el de Jessica a las 08:31:22.
func clienteQueYaDioLaCedula(store conversation.Store, from string) {
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, cuerpoConsentimiento())
	store.SetProfile(from, conversation.Profile{Identificacion: "1600670614"})
}

// A QUIEN YA DIO LA CÉDULA NO SE LE VUELVE A PEDIR.
func TestSiAceptaTardeYaNoSeLePideLaCedulaOtraVez(t *testing.T) {
	const from = "593968179884"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	clienteQueYaDioLaCedula(store, from)

	msg, manejado := ag.ResponderConsentimiento(from, "Sí, acepto")

	if !manejado {
		t.Fatal("no se resolvió el botón de consentimiento")
	}
	if pideLaCedula(msg) {
		t.Errorf("se le vuelve a pedir la cédula a quien ya la dio: %q", msg)
	}
	// Y el consentimiento queda registrado igual: eso no se pierde.
	if c, hay := store.GetConsentimiento(from); !hay || !c.Acepta {
		t.Error("no se registró la aceptación")
	}
}

// EL CASO CARLOS: con el pedido YA REGISTRADO, pedirle la cédula es todavía peor — le hace
// pensar que su pedido no existe.
func TestSiAceptaConElPedidoYaRegistradoNoSeLePideNada(t *testing.T) {
	const from = "593986140905"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, cuerpoConsentimiento())
	store.SetProfile(from, conversation.Profile{Identificacion: "0103519336", Nombres: "Carlos Fajardo"})
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 5,
		ProductoNombre: "GAS 15KG", ColorNombre: "BLANCO",
	})

	msg, manejado := ag.ResponderConsentimiento(from, BotonAceptoDatos)

	if !manejado {
		t.Fatal("no se resolvió el consentimiento")
	}
	if pideLaCedula(msg) {
		t.Errorf("con el pedido ya registrado se le pide la cédula: %q", msg)
	}
	// Se le confirma que su pedido sigue en pie, que es lo que necesita oír.
	bajo := normalizar(msg)
	if !strings.Contains(bajo, "pedido") {
		t.Errorf("no se le dice nada de su pedido, que ya existe: %q", msg)
	}
}

// LA CONTRACARA, el caso normal: quien acepta SIN haber dado la cédula sí tiene que recibir la
// petición. Sin este test, un mutante que nunca pida la cédula deja el flujo sin salida.
func TestSiAceptaSinHaberDadoLaCedulaSiSeLePide(t *testing.T) {
	const from = "593999850001"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, cuerpoConsentimiento())

	msg, manejado := ag.ResponderConsentimiento(from, "Sí, acepto")

	if !manejado {
		t.Fatal("no se resolvió el botón de consentimiento")
	}
	if !pideLaCedula(msg) {
		t.Errorf("no se le pide la cédula a quien acaba de autorizar y no la ha dado: %q", msg)
	}
}

// Y quien ya dio cédula Y nombre pero aún no tiene pedido tampoco puede quedarse colgado: se le
// dice qué falta de verdad.
func TestSiAceptaConCedulaYNombreSeSigueElFlujo(t *testing.T) {
	const from = "593999850002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	store.SetConsentimientoPendiente(from)
	store.AppendModel(from, cuerpoConsentimiento())
	store.SetProfile(from, conversation.Profile{
		Identificacion: "0105566777", Nombres: "David Espinoza",
	})

	msg, manejado := ag.ResponderConsentimiento(from, "Sí, acepto")

	if !manejado {
		t.Fatal("no se resolvió el botón")
	}
	if pideLaCedula(msg) {
		t.Errorf("se le pide la cédula a quien ya la dio: %q", msg)
	}
	if strings.TrimSpace(msg) == "" {
		t.Error("se le respondió con un mensaje vacío: el cliente se queda sin saber qué pasó")
	}
}
