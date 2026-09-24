package main

import (
	"os"
	"regexp"
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
	if timeoutTurno <= 0 || timeoutTurno > 90*time.Second {
		t.Fatalf("timeoutTurno = %s; debe ser > 0 y <= 90s para que un turno colgado no bloquee la cola", timeoutTurno)
	}
}

// Y el techo NO puede estar pegado a lo que tarda una respuesta normal.
//
// El caso (593984573546, 24-sep 15:57): el cliente escribió su dirección con referencias
// -"Rumiñahui 3-34 y hernando Leopulla esquina a 2 cuadras del rio tomebamba"- y no recibió
// respuesta. En el log: "ERROR tras 30.025s" y "signal: killed". No falló el modelo: el bot
// dejó de esperarlo, canceló el contexto y el proxy mató el proceso.
//
// timeoutTurno estaba en 30s cuando las respuestas reales medidas en producción llegaban a
// 22s. Ocho segundos de margen: cualquier turno un poco más largo caía. Es la misma clase de
// error que topeBusqueda (ver docs/flujo-espera-repartidor.md) — una red de seguridad puesta
// tan cerca del caso normal que deja de ser red y pasa a ser el límite.
//
// El test NO fija el número: fija el MARGEN sobre lo que de verdad tarda el modelo. Falla si
// alguien lo vuelve a bajar hasta rozar el caso normal.
func TestTimeoutTurnoNoAhogaUnaRespuestaNormal(t *testing.T) {
	// Máximo observado en producción (60 peticiones al proxy el 24-sep): 22s. Mediana ~7s.
	const respuestaLentaReal = 22 * time.Second
	const margenMinimo = 2 * respuestaLentaReal

	if timeoutTurno < margenMinimo {
		t.Fatalf("timeoutTurno = %s; con respuestas reales de hasta %s hace falta al menos %s "+
			"de margen. Con el techo pegado al caso normal, el cliente se queda sin respuesta "+
			"justo cuando escribe el mensaje más largo (el caso del 24-sep)",
			timeoutTurno, respuestaLentaReal, margenMinimo)
	}
}

// El turno del bot tiene que vencer ANTES que los relojes que están por debajo, o el orden se
// invierte y quien corta pasa a ser otro.
//
// Son tres, en cascada:
//
//	turno del bot        timeoutTurno            <- el que debe ganar
//	cliente HTTP         90s  (internal/llm/anthropic.go)
//	salida del proxy     100s (claude_proxy/cmd/proxy/main.go)
//
// Si timeoutTurno superara al del cliente HTTP, el error que vería el cliente cambiaría de
// "el turno tardó demasiado" a uno de transporte, y el reintento del proveedor quedaría
// atrapado dentro de un turno ya vencido.
func TestElTurnoVenceAntesQueElClienteHTTP(t *testing.T) {
	// Declarado en internal/llm/anthropic.go: &http.Client{Timeout: 90 * time.Second}.
	const timeoutClienteHTTP = 90 * time.Second

	if timeoutTurno >= timeoutClienteHTTP {
		t.Fatalf("timeoutTurno = %s >= cliente HTTP (%s): el turno tiene que vencer PRIMERO, "+
			"si no el corte lo decide el transporte y no la cola", timeoutTurno, timeoutClienteHTTP)
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

// EL CLIENTE NUNCA PUEDE VER UN ERROR TÉCNICO.
//
// El caso (Norma, 593984573546, 24-sep 11:33): el proxy devolvió un 401 y en el panel quedó
// "La IA falló al responder: anthropic HTTP 401: {...Invalid bearer token...}". A ella SÍ le
// llegó solo la disculpa —ese texto es auditoría interna (LogMessage), no se envía por
// WhatsApp—, pero la distancia entre las dos cosas es una sola línea de código: basta que
// alguien interpole el err en el texto que se manda para filtrar un stacktrace, una URL
// interna o el nombre del proveedor.
//
// Este guard es ESTRUCTURAL a propósito: no prueba el mensaje de hoy, prohíbe la FORMA de
// escribirlo mal. Un test que solo comprobara el texto actual no se enteraría del día en que
// alguien añada un replyClient(..., fmt.Sprintf("... %v", err)) tres funciones más abajo.
func TestNoSeLeMandaElErrorTecnicoAlCliente(t *testing.T) {
	// Las funciones que ESCRIBEN al cliente por WhatsApp. LogMessage/reportarFallo no están:
	// esas son la auditoría del panel, que sí debe llevar el detalle crudo para diagnosticar.
	enviosAlCliente := regexp.MustCompile(`(replyClient|whatsapp\.SendText)\([^)]*`)

	for _, archivo := range []string{"main.go", "cobertura.go", "cierre.go", "takeover.go"} {
		src := codigoSinComentarios(t, archivo)
		for _, envio := range enviosAlCliente.FindAllString(src, -1) {
			// `err` como identificador suelto: err, %v con err, err.Error(), fmt.Sprintf(..., err).
			// Se excluye `err :=` y `err !=`, que son la comprobación normal del valor devuelto.
			if regexp.MustCompile(`\berr\b\s*(?:\.|,|\))`).MatchString(envio) {
				t.Errorf("%s: se le está mandando el error técnico al cliente:\n  %s\n"+
					"El cliente recibe una disculpa; el detalle va a reportarFallo (panel y "+
					"Telegram), nunca a WhatsApp.", archivo, envio)
			}
		}
	}
}

// Y la disculpa tiene que seguir siendo una disculpa: sin jerga, sin códigos, sin nombres de
// proveedor. Si alguien "mejora" el mensaje metiendo el detalle para ayudar al soporte, el
// cliente acaba leyendo "anthropic HTTP 401" y pierde la confianza en el servicio.
func TestLaDisculpaNoLlevaJergaTecnica(t *testing.T) {
	src := codigoSinComentarios(t, "main.go")
	i := strings.Index(src, "inconveniente técnico")
	if i < 0 {
		t.Fatal("no se encontró el mensaje de disculpa; si se renombró, actualizar este test")
	}
	// La línea entera donde vive el mensaje.
	inicio := strings.LastIndex(src[:i], "\n") + 1
	fin := strings.Index(src[i:], "\n")
	linea := src[inicio : i+fin]

	prohibidas := []string{"anthropic", "gemini", "HTTP", "401", "429", "%v", "%s", "%w",
		"err", "token", "API", "status"}
	for _, p := range prohibidas {
		if strings.Contains(linea, p) {
			t.Errorf("la disculpa al cliente contiene %q: %s", p, strings.TrimSpace(linea))
		}
	}
}
