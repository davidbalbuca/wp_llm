// EL NOMBRE DEL NEGOCIO LO PONE EL BACKEND, NO EL PROMPT QUEMADO.
//
// UBIA-66 ("cambio de nombre predeterminado 'Geoware'"): el nombre de la distribuidora estaba fijo
// en el prompt embebido, así que cambiarlo exigía recompilar. Ahora businessInfo publica
// NEGOCIO_NOMBRE (Parametro, editable en el panel, igual que NEGOCIO_TELEFONO) y el bot lo inyecta
// en el prompt con instrucción de que MANDA sobre el nombre por defecto. Si está vacío, el prompt
// conserva su valor por defecto ("Ubi") — no se rompe nada.
package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/georoutes"
)

func TestElNombreDelNegocioDelBackendMandaEnElPrompt(t *testing.T) {
	ctx := &catalog.Context{
		Business: georoutes.Business{Nombre: "GasExpress"},
		Products: []georoutes.Product{{Nombre: "GAS 15KG"}},
	}
	info := renderServiceInfo(ctx, true)

	if !strings.Contains(info, "GasExpress") {
		t.Errorf("el nombre del negocio del backend no aparece en el prompt:\n%s", info)
	}
	// No basta con nombrarlo: tiene que instruir al modelo a usarlo, para que MANDE sobre el
	// "Ubi" quemado del prompt.
	if !strings.Contains(strings.ToLower(info), "usa") || !strings.Contains(strings.ToLower(info), "siempre") {
		t.Errorf("el nombre aparece pero sin instrucción de que mande sobre el por defecto:\n%s", info)
	}
}

// Sin nombre configurado, NO se inventa una línea de negocio: el prompt sigue con su valor por
// defecto y no aparece un "Negocio: ." vacío.
func TestSinNombreConfiguradoNoSaleLineaDeNegocio(t *testing.T) {
	ctx := &catalog.Context{
		Business: georoutes.Business{Nombre: ""},
		Products: []georoutes.Product{{Nombre: "GAS 15KG"}},
	}
	info := renderServiceInfo(ctx, true)
	if strings.Contains(info, "Negocio:") {
		t.Errorf("con nombre vacío no debe salir la línea 'Negocio:':\n%s", info)
	}
}
