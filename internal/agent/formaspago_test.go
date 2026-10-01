// EL BOT SÍ LE DICE AL CLIENTE CÓMO PAGAR.
//
// UBIA-108: la lista real de formas de pago (tabla TipoPago, getPayments) el bot la usaba SOLO
// internamente para poner el idtipopago al registrar el pedido; nunca la mostraba al cliente. El
// único texto de pago visible venía de businessInfo.formas_pago, que en prod está vacío. Así, si
// el cliente preguntaba "¿cómo puedo pagar?", el bot no tenía el dato en su prompt. Ahora, si no
// hay texto libre, se listan los métodos reales de la tabla.
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/georoutes"
)

func TestLaFormaDePagoRealAparecEnElPrompt(t *testing.T) {
	ctx := &catalog.Context{
		Products: []georoutes.Product{{Nombre: "GAS 15KG"}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
		// businessInfo sin texto de formas de pago (como en prod).
		Business: georoutes.Business{},
	}
	info := renderServiceInfo(ctx, true)
	if !strings.Contains(info, "Efectivo") {
		t.Errorf("la forma de pago real no llega al prompt:\n%s", info)
	}
	if !strings.Contains(strings.ToLower(info), "forma") || !strings.Contains(strings.ToLower(info), "pago") {
		t.Errorf("no hay una línea de formas de pago:\n%s", info)
	}
}

func TestVariasFormasDePagoSeListanTodas(t *testing.T) {
	ctx := &catalog.Context{
		Products: []georoutes.Product{{Nombre: "GAS 15KG"}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}, {ID: 2, Nombre: "Transferencia"}},
	}
	info := renderServiceInfo(ctx, true)
	if !strings.Contains(info, "Efectivo") || !strings.Contains(info, "Transferencia") {
		t.Errorf("no se listaron todas las formas de pago:\n%s", info)
	}
}

// El texto libre del negocio (si el dueño lo escribió) MANDA sobre la lista de la tabla: permite
// matices ("efectivo o transferencia, contra entrega") que la tabla no captura.
func TestElTextoLibreDeFormasDePagoTienePrioridad(t *testing.T) {
	ctx := &catalog.Context{
		Products: []georoutes.Product{{Nombre: "GAS 15KG"}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
		Business: georoutes.Business{FormasPago: "Solo transferencia bancaria por ahora"},
	}
	info := renderServiceInfo(ctx, true)
	if !strings.Contains(info, "Solo transferencia bancaria por ahora") {
		t.Errorf("el texto libre del negocio no se respetó:\n%s", info)
	}
	// Y no se duplica con la línea de la tabla.
	if strings.Contains(info, "Formas de pago aceptadas:") {
		t.Errorf("con texto libre presente NO debe salir además la lista de la tabla:\n%s", info)
	}
}

// Sin ninguna forma de pago (ni texto ni tabla) no se inventa una línea vacía.
func TestSinFormasDePagoNoSaleLinea(t *testing.T) {
	ctx := &catalog.Context{Products: []georoutes.Product{{Nombre: "GAS 15KG"}}}
	info := renderServiceInfo(ctx, true)
	if strings.Contains(strings.ToLower(info), "formas de pago") {
		t.Errorf("sin datos de pago no debe salir la línea:\n%s", info)
	}
}
