package agent

import (
	"strings"
	"testing"
)

// El caso real del 01/10 (pedido #321, Samantha 593995626224): el pedido se confirmó bien y el
// backend sí mandó el token, pero el modelo redactó la confirmación y se COMIÓ el enlace. El cliente
// solo recibió el seguimiento minutos después, por el aviso del operador. La cara positiva del
// candado anexa el enlace cuando falta.
func TestAnexarEnlaceSiFalta(t *testing.T) {
	const enlace = "https://ws11.geoware.lat/seguimiento/MzIx:1xCHV2:qHLsOykhlsKMNC"

	casos := []struct {
		nombre       string
		texto        string
		enlace       string
		esperaAnexo  bool
		debeContener string
	}{
		{
			nombre:       "el modelo omitió el enlace: se anexa",
			texto:        "¡Listo! 🎉 Tu pedido de 2 x GAS 15KG color BLANCO va en camino. Tu repartidor es JUAN ANDRES PICON. Llega en unos 9 minutos aprox. Te aviso apenas esté llegando. 🙌",
			enlace:       enlace,
			esperaAnexo:  true,
			debeContener: enlace,
		},
		{
			nombre:      "el modelo YA puso el enlace: no se duplica",
			texto:       "¡Listo! Tu pedido va en camino.\n\n📍 Sigue a tu repartidor en vivo aquí:\n" + enlace,
			enlace:      enlace,
			esperaAnexo: false,
		},
		{
			nombre:      "el modelo puso el enlace con un punto al final: tampoco se duplica",
			texto:       "Sigue tu pedido: " + enlace + ".",
			enlace:      enlace,
			esperaAnexo: false,
		},
		{
			nombre:      "sin enlace del pedido (backend no mandó token): no se inventa nada",
			texto:       "¡Listo! Tu pedido va en camino. Te aviso apenas esté llegando.",
			enlace:      "",
			esperaAnexo: false,
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got, anexado := anexarEnlaceSiFalta(c.texto, c.enlace)
			if anexado != c.esperaAnexo {
				t.Fatalf("anexado = %v, se esperaba %v. Resultado: %q", anexado, c.esperaAnexo, got)
			}
			if c.debeContener != "" && !strings.Contains(got, c.debeContener) {
				t.Fatalf("el resultado no contiene el enlace esperado.\n got: %q", got)
			}
			// El enlace nunca puede aparecer dos veces.
			if c.enlace != "" {
				if n := len(enlaceSeguimientoRe.FindAllString(got, -1)); n > 1 {
					t.Fatalf("el enlace aparece %d veces (duplicado):\n%q", n, got)
				}
			}
		})
	}
}
