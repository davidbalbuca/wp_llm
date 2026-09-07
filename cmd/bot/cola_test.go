package main

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Los mensajes del MISMO cliente se procesan en orden, uno tras otro: el segundo espera a que
// termine el primero. Sin esto, dos mensajes seguidos ("necesito un tanque" + "para los
// condominios Bemani") arrancaban dos turnos simultáneos que no se veían entre sí.
func TestMensajesDelMismoClienteEnOrden(t *testing.T) {
	const phone = "593999111222"
	var orden []string
	var mu sync.Mutex
	anota := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		orden = append(orden, s)
	}

	primeroEmpezo := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // primer mensaje: entra y se toma su tiempo
		defer wg.Done()
		l := lockCliente(phone)
		l.Lock()
		defer l.Unlock()
		anota("1-inicio")
		close(primeroEmpezo)
		time.Sleep(50 * time.Millisecond)
		anota("1-fin")
	}()

	<-primeroEmpezo // garantiza que el 2.º llega con el 1.º en curso
	go func() {     // segundo mensaje del MISMO cliente: debe esperar
		defer wg.Done()
		l := lockCliente(phone)
		l.Lock()
		defer l.Unlock()
		anota("2-inicio")
	}()

	wg.Wait()

	esperado := []string{"1-inicio", "1-fin", "2-inicio"}
	if len(orden) != len(esperado) {
		t.Fatalf("orden inesperado: %v", orden)
	}
	for i := range esperado {
		if orden[i] != esperado[i] {
			t.Fatalf("el 2.º mensaje no esperó al 1.º: %v (esperado %v)", orden, esperado)
		}
	}
}

// Clientes DISTINTOS no se bloquean entre sí: la cola es por teléfono, no global.
func TestClientesDistintosNoSeBloquean(t *testing.T) {
	maria := lockCliente("593999000001")
	maria.Lock()
	defer maria.Unlock()

	listo := make(chan struct{})
	go func() {
		juan := lockCliente("593999000002")
		juan.Lock()
		juan.Unlock()
		close(listo)
	}()

	select {
	case <-listo:
	case <-time.After(time.Second):
		t.Fatal("el turno de Juan quedó bloqueado por el de María: la cola no es por cliente")
	}
}

// El mismo teléfono devuelve SIEMPRE el mismo mutex (si no, la cola no serializa nada).
func TestLockClienteEsElMismoPorTelefono(t *testing.T) {
	if lockCliente("593999333444") != lockCliente("593999333444") {
		t.Fatal("lockCliente devolvió mutexes distintos para el mismo teléfono")
	}
	if lockCliente("593999333444") == lockCliente("593999555666") {
		t.Fatal("lockCliente devolvió el mismo mutex para teléfonos distintos")
	}
}

// Un turno colgado no puede bloquear la cola para siempre: timeoutTurno lo acota. Este test
// fija el contrato del valor (si alguien lo sube a minutos, el cliente siguiente esperaría eso).
func TestTimeoutTurnoAcotado(t *testing.T) {
	if timeoutTurno <= 0 || timeoutTurno > time.Minute {
		t.Fatalf("timeoutTurno = %s; debe ser > 0 y <= 1m para que un turno colgado no bloquee la cola", timeoutTurno)
	}
}

// Guard ESTRUCTURAL: los tests de arriba prueban la primitiva (lockCliente), no que
// processWebhook la USE. Se comprobó borrando el lock del handler: la suite seguía en verde.
// processWebhook no se puede ejecutar en un test (dispara llamadas reales a WhatsApp y al
// backend), así que se verifica sobre el fuente que el lock se toma ANTES de tocar el store.
// Si alguien lo borra o lo mueve después del primer acceso al store, esto falla.
func TestProcessWebhookTomaElLockAntesDeTocarElStore(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	cuerpo := string(src)
	ini := strings.Index(cuerpo, "func processWebhook(")
	if ini < 0 {
		t.Fatal("no se encontró processWebhook en main.go")
	}
	cuerpo = cuerpo[ini:]
	if fin := strings.Index(cuerpo, "\nfunc "); fin > 0 {
		cuerpo = cuerpo[:fin]
	}

	posLock := strings.Index(cuerpo, "lockCliente(inc.From)")
	if posLock < 0 {
		t.Fatal("processWebhook no toma el lock por cliente: dos mensajes del mismo cliente " +
			"volverían a procesarse en paralelo y a pisarse el estado")
	}
	fin := posLock + 200
	if fin > len(cuerpo) {
		fin = len(cuerpo) // el cuerpo puede ser más corto que la ventana
	}
	if !strings.Contains(cuerpo[posLock:fin], "Lock()") || !strings.Contains(cuerpo[posLock:fin], "defer") {
		t.Fatal("processWebhook obtiene el mutex pero no hace Lock()/defer Unlock() a continuación")
	}
	// El lock debe ir ANTES de CUALQUIER acceso al store (no una lista de métodos concretos:
	// esa lista se queda corta en cuanto alguien añade uno nuevo).
	if pos := strings.Index(cuerpo, "store."); pos >= 0 && pos < posLock {
		linea := cuerpo[pos:]
		if corte := strings.IndexByte(linea, '\n'); corte > 0 {
			linea = linea[:corte]
		}
		t.Fatalf("processWebhook accede al store ANTES de tomar el lock (%q): dos turnos del "+
			"mismo cliente leerían el mismo estado viejo", strings.TrimSpace(linea))
	}
}
