package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// NO SE LE OFRECE AL CLIENTE UN CAMINO QUE DESPUÉS SE LE NIEGA.
//
// El 20/09 el modelo se inventó este menú, que no existe en ninguna parte del código:
//
//	📋 ¿Cómo prefieres compartirla?
//	• Compartir ubicación actual
//	• Escribir dirección
//
// Jessica (593968179884, 08:30:54) eligió la segunda y el bot le respondió "Entendido, pero
// necesito que me compartas tu ubicación por WhatsApp". Otra clienta (593997141025) vio el mismo
// menú a las 14:59 y no volvió a escribir nunca.
//
// La regla de no aceptar direcciones escritas es correcta y no se toca: mandar el gas a otra
// casa es el error más caro del negocio. Lo que está mal es OFRECERLA. Un menú es una promesa
// —"cualquiera de estas opciones vale"— y romperla deja al cliente sin saber qué hacer.
//
// El prompt ya lo prohíbe desde hoy, pero el prompt es una capa: el modelo no siempre obedece,
// que es justo lo que enseñó el candado de los menús. Esto es la capa que no depende de él.

func TestNoSaleUnMenuQueOfreceEscribirLaDireccion(t *testing.T) {
	// Las variantes que el modelo redactó o podría redactar para lo mismo.
	opcionesProhibidas := [][]string{
		{"Compartir ubicación actual", "Escribir dirección"},
		{"Compartir mi ubicación actual", "Escribir mi dirección"},
		{"Enviar ubicación", "Escribir la dirección"},
		{"Mandar ubicación", "Dictar mi dirección"},
		{"Compartir ubicación", "Escribir las calles"},
		{"Ubicación de WhatsApp", "Te escribo la dirección"},
	}
	for _, opciones := range opcionesProhibidas {
		t.Run(strings.Join(opciones, " / "), func(t *testing.T) {
			const from = "593968179884"
			store := conversation.NewMemStore()
			ag := agentDePrueba(nil, store)
			// El envío se inyecta para que el menú PUEDA salir: sin esto el test pasaría
			// aunque el candado no existiera, porque en pruebas no hay credenciales de
			// WhatsApp y ningún menú llega a enviarse.
			salio := false
			ag.enviarMenu = func(string, string, []string) error {
				salio = true
				return nil
			}
			tu := &turno{}

			salida := ag.mostrarMenu(tu, from, map[string]any{
				"cuerpo":   "¿Cómo prefieres compartirla?",
				"opciones": aInterfaces(opciones),
			})

			if salio || tu.menuSent {
				t.Errorf("salió el menú que ofrece escribir la dirección: %v", opciones)
			}
			// Y al modelo se le explica qué hacer en su lugar, para que no se quede mudo.
			if !strings.Contains(strings.ToLower(salida), "ubicaci") {
				t.Errorf("no se le dice al modelo que pida la ubicación: %q", salida)
			}
		})
	}
}

// LA CONTRACARA: los menús legítimos siguen saliendo. Sin esto, un mutante que bloquee todos los
// menús —y deje al bot sin botones, que es justo lo que se pidió construir— sobreviviría.
//
// Se inyecta a.enviarMenu porque en las pruebas no hay credenciales de WhatsApp y el envío real
// corta sin mandar nada (whatsapp.sendPayload). Sin esto el test fallaba SIEMPRE, incluso con
// "1 / 2 / 3", y habría parecido culpa del candado.
func TestLosMenusNormalesSiguenSaliendo(t *testing.T) {
	legitimos := [][]string{
		{"BLANCO", "AMARILLO", "NARANJA", "AZUL"},
		{"1", "2", "3"},
		{"Sí, la misma", "Otra dirección"},
		{"Esperar", "Cancelar"},
		{"Sí, acepto", "No acepto"},
		// El de direcciones GUARDADAS sí ofrece elegir entre ubicaciones, y es correcto: todas
		// son ubicaciones reales que el cliente ya confirmó antes.
		{"Casa (Av. Solano 123)", "Trabajo (Gran Colombia 45)", "Otra ubicación"},
	}
	for _, opciones := range legitimos {
		t.Run(strings.Join(opciones, "/"), func(t *testing.T) {
			const from = "593999800001"
			store := conversation.NewMemStore()
			ag := agentDePrueba(nil, store)
			var enviadas []string
			ag.enviarMenu = func(_, _ string, o []string) error {
				enviadas = o
				return nil
			}
			tu := &turno{}

			ag.mostrarMenu(tu, from, map[string]any{
				"cuerpo":   "Elige una opción",
				"opciones": aInterfaces(opciones),
			})

			if !tu.menuSent {
				t.Fatalf("se bloqueó un menú legítimo: %v", opciones)
			}
			if len(enviadas) != len(opciones) {
				t.Errorf("el menú salió con otras opciones.\nse pidió: %v\nsalió:    %v",
					opciones, enviadas)
			}
		})
	}
}

// aInterfaces convierte las opciones al formato en que llegan desde el modelo (JSON => []any).
func aInterfaces(opciones []string) []any {
	out := make([]any, len(opciones))
	for i, o := range opciones {
		out[i] = o
	}
	return out
}
