package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// La extracción del armado del prompt (de += en HandleMessage a strings.Builder en
// construirSistema) NO puede cambiar el texto que ve el modelo: un byte distinto cambia su
// comportamiento. Este test fija los invariantes de cada bloque condicional.
func TestConstruirSistemaBloques(t *testing.T) {
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	cfg := config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}

	nuevo := func(store conversation.Store) *Agent {
		return &Agent{store: store, catalog: cat, cfg: cfg}
	}

	t.Run("cliente nuevo: sin datos, sin ubicación, con horario", func(t *testing.T) {
		a := nuevo(conversation.NewMemStore())
		fijo, vol := a.construirSistema("593999")
		// La parte fija SIEMPRE incluye las reglas + la info del servicio.
		if !strings.Contains(fijo, "INFORMACIÓN DEL SERVICIO:") {
			t.Error("la parte fija debe tener INFORMACIÓN DEL SERVICIO")
		}
		// Sin perfil no se inyectan DATOS DEL CLIENTE.
		if strings.Contains(vol, "DATOS DEL CLIENTE") {
			t.Error("cliente sin perfil no debe llevar DATOS DEL CLIENTE")
		}
		// Sin ubicación no se dice nada de ubicación.
		if strings.Contains(vol, "YA compartio su ubicacion") {
			t.Error("sin ubicación guardada no debe afirmar que la compartió")
		}
		// La hora SIEMPRE va (en positivo o negativo).
		if !strings.Contains(vol, "HORA ACTUAL:") {
			t.Error("siempre debe incluir HORA ACTUAL")
		}
	})

	t.Run("cliente conocido con último pedido y ubicación", func(t *testing.T) {
		store := conversation.NewMemStore()
		store.SetProfile("593888", conversation.Profile{Identificacion: "0105888887", Nombres: "María José"})
		store.SetLastOrder("593888", conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "ayer"})
		store.SetLocation("593888", -2.9, -79.0)
		a := nuevo(store)
		_, vol := a.construirSistema("593888")

		for _, must := range []string{
			"DATOS DEL CLIENTE",
			"0105888887",
			"María José",
			"ÚLTIMO PEDIDO DEL CLIENTE",
			"2 x GAS 15KG",
			"YA compartio su ubicacion", // ya tiene ubicación guardada
		} {
			if !strings.Contains(vol, must) {
				t.Errorf("falta el bloque esperado: %q", must)
			}
		}
	})

	t.Run("calificación pendiente aparece", func(t *testing.T) {
		store := conversation.NewMemStore()
		store.SetPendingRating("593777", conversation.PendingRating{PedidoID: 5, Conductor: "Pedro"})
		a := nuevo(store)
		_, vol := a.construirSistema("593777")
		if !strings.Contains(vol, "CALIFICACIÓN PENDIENTE") || !strings.Contains(vol, "Pedro") {
			t.Error("debe incluir la calificación pendiente con el conductor")
		}
	})
}
