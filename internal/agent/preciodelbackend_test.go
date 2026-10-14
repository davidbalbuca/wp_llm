package agent

import (
	"testing"
	"wp-llm-gas/internal/georoutes"
)

// El bot es un CANAL, no la fuente de los precios: el valor a pagar que le dice al cliente debe
// ser el que el backend acaba de grabar, no uno recalculado del catálogo.
//
// Antes se recalculaba porque un comentario afirmaba que "wppOrder devuelve solo total_productos
// (el cilindro suelto)". Era FALSO -en catalog_selectors.py `total_productos` es la suma de los
// CUATRO rubros-, y el nombre engañoso provocó un cálculo duplicado. Con listas de precios y
// promociones, ese duplicado hace que el bot anuncie un precio y el backend cobre otro.
//
// Ver memory/17-bot-no-calcula-precios.md.

// elegirTotal reproduce la decisión de pedido.go: manda el total del backend; el del catálogo es
// solo la red de seguridad para una versión del backend que no lo mande.
func elegirTotal(totalBackend float64, lineas []lineaPedido) float64 {
	if totalBackend > 0 {
		return totalBackend
	}
	return totalPagarDe(lineas)
}

func lineasDePrueba() []lineaPedido {
	// El precio real de produccion: cilindro regulado 1.65 + servicios 1.60 = 3.25/u.
	p := georoutes.Product{
		Nombre: "GAS 15KG", PrecioUnitario: 1.65,
		CostoEnvio: 1.00, CostoInstalacion: 0.45, CostoServicio: 0.15,
	}
	return []lineaPedido{{Producto: p, Cantidad: 10}}
}

func TestMandaElTotalDelBackendNoElDelCatalogo(t *testing.T) {
	lineas := lineasDePrueba()
	// El catálogo daría 32.50; el backend cobró 24.50 (lista de mayorista).
	if got := elegirTotal(24.50, lineas); got != 24.50 {
		t.Fatalf("el bot debe decir el total del backend (24.50), dijo %.2f", got)
	}
}

func TestSinTotalDelBackendCaeAlCatalogo(t *testing.T) {
	lineas := lineasDePrueba()
	// Un backend viejo que no manda total: más vale el del catálogo que ninguno.
	if got := elegirTotal(0, lineas); got != 32.50 {
		t.Fatalf("sin total del backend debe usar el del catálogo (32.50), usó %.2f", got)
	}
}

func TestElTotalDelCatalogoSumaTodosLosRubros(t *testing.T) {
	// Fija que PrecioTotal son los 4 rubros: si alguien quitara uno, el fallback mentiría.
	if got := totalPagarDe(lineasDePrueba()); got != 32.50 {
		t.Fatalf("10 x 3.25 = 32.50, dio %.2f", got)
	}
}
