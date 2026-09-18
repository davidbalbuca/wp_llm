package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El destino lleva el NOMBRE que puso el cliente Y la calle registrada en el backend.
//
// CAMBIO DE CRITERIO (18/09, pedido del dueño). Antes este test exigía el alias A SECAS, con el
// argumento de que "Casa" es lo que el cliente reconoce de un vistazo. Es verdad, pero insuficiente:
// con solo el alias, el cliente confirma "te lo envío a Casa" sin poder ver a qué calle apunta esa
// Casa. Si la guardó mal —o si guardó la de su mamá como Casa— no hay forma de que lo note hasta
// que el repartidor no llega, que es el error más caro y más tardío del negocio.
//
// Con las dos, reconoce el nombre de un vistazo Y puede verificar la calle. Ver destino.go.
func TestDestinoLlevaElNombreYLaCalle(t *testing.T) {
	last := conversation.LastOrder{Alias: "Casa", Direccion: "Av. Solano 123"}
	if got := last.Destino(); got != "Casa (Av. Solano 123)" {
		t.Errorf("el destino debe llevar el nombre del cliente Y la calle: %q", got)
	}
}

// El alias INTERNO del backend no es un nombre que el cliente eligiera: no se le puede decir
// "te lo envío otra vez a WhatsApp". Cuando es el único que hay, se usa la calle.
func TestElAliasInternoDelBackendNoSeLeMuestraAlCliente(t *testing.T) {
	last := conversation.LastOrder{Alias: "WhatsApp", Direccion: "Av. Solano 123"}
	if got := last.Destino(); got != "Av. Solano 123" {
		t.Errorf("no se puede mostrar el alias interno del backend como destino: %q", got)
	}
}

// Y el texto de respaldo que escribe el backend cuando solo tuvo el pin son las coordenadas
// disfrazadas de dirección: mostrárselas es peor que no decir nada.
func TestLaDireccionDeRespaldoNoSeLeMuestraAlCliente(t *testing.T) {
	last := conversation.LastOrder{
		Alias:     "Casa",
		Direccion: "Ubicación compartida por WhatsApp (-2.9, -79.0)",
	}
	if got := last.Destino(); got != "Casa" {
		t.Errorf("las coordenadas no se le muestran como si fueran una calle: %q", got)
	}
	// Y sin alias tampoco: mejor vacío (el bot pide la ubicación) que unas coordenadas.
	soloRespaldo := conversation.LastOrder{Direccion: "Ubicación compartida por WhatsApp (-2.9, -79.0)"}
	if got := soloRespaldo.Destino(); got != "" {
		t.Errorf("sin nombre y con solo el respaldo, el destino debe quedar vacío: %q", got)
	}
}

// Sin nombre, la calle sirve: "a Av. Solano 123" es mejor que no decir nada.
func TestDestinoCaeALaCalleSiNoHayNombre(t *testing.T) {
	last := conversation.LastOrder{Direccion: "Av. Solano 123"}
	if got := last.Destino(); got != "Av. Solano 123" {
		t.Errorf("sin nombre se muestra la calle: %q", got)
	}
}

// REGLA DURA: sin ubicación guardada NO se inventa un destino. Los pedidos anteriores a esta
// feature no la tienen, y decirle "te lo envío a X" sin saberlo sería peor que preguntar.
func TestDestinoVacioCuandoNoSeSabeDondeSeEntrego(t *testing.T) {
	if got := (conversation.LastOrder{}).Destino(); got != "" {
		t.Errorf("sin datos de entrega el destino debe ser vacío, no inventado: %q", got)
	}
}

// El texto del pedido nombra cantidad, color y destino.
func TestDescribirPedidoNombraCantidadColorYDestino(t *testing.T) {
	txt := describirPedido(conversation.LastOrder{Cantidad: 2, Color: "BLANCO", Alias: "Casa"})
	for _, esperado := range []string{"2", "BLANCO", "Casa"} {
		if !strings.Contains(txt, esperado) {
			t.Errorf("falta %q en el texto del pedido: %q", esperado, txt)
		}
	}
}

// NO-REGRESIÓN: un pedido viejo (sin ubicación) se describe sin arrastrar un " a " colgando.
func TestDescribirPedidoViejoNoMencionaDestino(t *testing.T) {
	if txt := describirPedido(conversation.LastOrder{Cantidad: 1, Color: "BLANCO"}); txt != "1 BLANCO" {
		t.Errorf("un pedido sin ubicación se describe solo con cantidad y color: %q", txt)
	}
}
