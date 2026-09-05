package agent

import (
	"testing"

	"wp-llm-gas/internal/georoutes"
)

// El VALOR A PAGAR se calcula con Product.PrecioTotal (unitario + envío + instalación + servicio)
// × cantidad, NO con el total del backend (que en wppOrder viene incompleto: solo el cilindro
// suelto). Este test fija que el cálculo sume TODOS los rubros — si alguien "simplifica" a
// PrecioUnitario, el cliente vería un valor menor al real.
func TestValorAPagarSumaTodosLosRubros(t *testing.T) {
	p := georoutes.Product{
		PrecioUnitario:   3.00,
		CostoEnvio:       1.00,
		CostoInstalacion: 0.50,
		CostoServicio:    0.25,
	}
	// por unidad: 3.00 + 1.00 + 0.50 + 0.25 = 4.75
	if got := p.PrecioTotal(); got != 4.75 {
		t.Fatalf("PrecioTotal por unidad = %.2f, want 4.75", got)
	}
	// 2 cilindros = 9.50
	if got := p.PrecioTotal() * 2; got != 9.50 {
		t.Errorf("valor a pagar 2 unidades = %.2f, want 9.50", got)
	}
}
