package notify

import (
	"testing"

	"wp-llm-gas/internal/conversation"
)

// Los hilos FIJOS del grupo (Errores, Pedidos sin atender, Sondeos) tienen que sobrevivir a un
// reinicio del bot. Antes vivían solo en memoria: cada deploy creaba un hilo nuevo con el mismo
// nombre y el grupo terminó con una pila de "⚠️ Errores del sistema" idénticos (reportado el
// 08/09 con captura). Ahora se guardan en la misma tabla que los hilos de cliente.
func TestLosHilosFijosSobrevivenAlReinicio(t *testing.T) {
	store := conversation.NewMemStore()

	// Primera ejecución del bot: alguien ya creó los hilos y quedaron guardados.
	store.SetTelegramThread(claveHiloErrores, 111)
	store.SetTelegramThread(claveHiloSinRepartidor, 222)
	store.SetTelegramThread(claveHiloSondeo, 333)

	// Reinicio: Notifier nuevo, memoria en cero, MISMO store (la BD sobrevive).
	n := &Notifier{store: store}

	if id := n.hiloErroresID(); id != 111 {
		t.Errorf("tras reiniciar, el hilo de errores es %d y no el guardado (111): se crearía uno nuevo", id)
	}
	if id := n.hiloSinRepartidorID(); id != 222 {
		t.Errorf("tras reiniciar, el hilo de pedidos sin atender es %d y no el guardado (222)", id)
	}
	if id := n.hiloSondeoID(); id != 333 {
		t.Errorf("tras reiniciar, el hilo de sondeos es %d y no el guardado (333)", id)
	}
}

// Las claves de los hilos fijos comparten tabla con los teléfonos de los clientes: no pueden
// colisionar con un número real ni entre ellas.
func TestLasClavesDeHilosFijosNoChocanConTelefonos(t *testing.T) {
	claves := []string{claveHiloErrores, claveHiloSinRepartidor, claveHiloSondeo}
	vistas := map[string]bool{}
	for _, c := range claves {
		if c == "" || c[0] != '#' {
			t.Errorf("la clave %q podría confundirse con un teléfono: debe empezar por '#'", c)
		}
		if vistas[c] {
			t.Errorf("la clave %q está repetida: dos hilos fijos compartirían el mismo tema", c)
		}
		vistas[c] = true
	}
}

// Una vez resuelto, el id queda en memoria: no se vuelve a consultar la BD en cada aviso.
func TestElHiloFijoSeCacheaEnMemoria(t *testing.T) {
	store := conversation.NewMemStore()
	store.SetTelegramThread(claveHiloErrores, 444)
	n := &Notifier{store: store}

	if id := n.hiloErroresID(); id != 444 {
		t.Fatalf("no leyó el hilo guardado: %d", id)
	}
	// Se borra de la BD: si el valor no estuviera cacheado, el siguiente aviso intentaría
	// crear un hilo nuevo (y en producción eso son duplicados).
	store.SetTelegramThread(claveHiloErrores, 0)
	if id := n.hiloErroresID(); id != 444 {
		t.Errorf("el hilo no quedó cacheado en memoria: %d", id)
	}
}
