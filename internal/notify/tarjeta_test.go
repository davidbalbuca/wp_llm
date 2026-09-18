package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wp-llm-gas/internal/conversation"
)

// LA TARJETA DE ESTADO ES UN MENSAJE QUE AVANZA, NO CUATRO MENSAJES SUELTOS.
//
// Pedido del dueño (18/09): estados por cliente en Telegram —pidió gas, pedido registrado,
// entregado, y si hay algún error— además de los avisos que ya existen.
//
// Lo que se protege aquí no es el texto: es que el grupo siga siendo LEGIBLE. Cuatro avisos por
// cliente, por todos los clientes del día, vuelven el grupo un muro que nadie lee — y un grupo que
// nadie lee es peor que no tener avisos, porque da falsa sensación de vigilancia.

// telegramFalso registra cada llamada que recibe la "API de Telegram".
type telegramFalso struct {
	mu        sync.Mutex
	metodos   []string // en orden: "sendMessage", "editMessageText"...
	textos    []string // el texto de cada llamada
	servidor  *httptest.Server
	siguiente int64 // message_id que se devuelve al crear
}

func nuevoTelegramFalso(t *testing.T) *telegramFalso {
	t.Helper()
	tg := &telegramFalso{siguiente: 500}
	tg.servidor = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(cuerpo, &payload)

		tg.mu.Lock()
		metodo := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		tg.metodos = append(tg.metodos, metodo)
		tg.textos = append(tg.textos, payload.Text)
		id := tg.siguiente
		tg.siguiente++
		tg.mu.Unlock()

		w.Write([]byte(`{"ok":true,"result":{"message_id":` + strconv.FormatInt(id, 10) + `,"message_thread_id":9}}`))
	}))
	t.Cleanup(tg.servidor.Close)
	return tg
}

// cuenta cuántas veces se llamó a un método.
func (tg *telegramFalso) cuenta(metodo string) int {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	n := 0
	for _, m := range tg.metodos {
		if m == metodo {
			n++
		}
	}
	return n
}

// ultimoTexto devuelve el texto de la última llamada ("" si no hubo).
func (tg *telegramFalso) ultimoTexto() string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if len(tg.textos) == 0 {
		return ""
	}
	return tg.textos[len(tg.textos)-1]
}

// notificadorDePrueba arma un Notifier apuntado al Telegram falso, con el hilo del cliente ya
// resuelto para que no haya que crear temas.
func notificadorDePrueba(t *testing.T, tg *telegramFalso, phone string) (*Notifier, conversation.Store) {
	t.Helper()
	store := conversation.NewMemStore()
	store.SetTelegramThread(phone, 9) // hilo ya existente: evita el createForumTopic
	return &Notifier{
		token:   "t",
		chatID:  "c",
		store:   store,
		cliente: tg.servidor.Client(),
		base:    tg.servidor.URL,
	}, store
}

// esperarCondicion espera a que algo sea cierto (los avisos son asíncronos).
//
// Se prefiere a contar llamadas cuando lo que importa es el EFECTO: contar es frágil porque
// cualquier llamada nueva —el renombrado del hilo, sin ir más lejos— desplaza el número y el test
// sigue adelante antes de tiempo. Pasó el 18/09 con TestTrasCerrarElCiclo.
func esperarCondicion(t *testing.T, que string, cumple func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cumple() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no se cumplió a tiempo: %s", que)
}

// olvidarNombrado borra la marca de "a este hilo ya se le puso el nombre".
//
// Hace falta porque hilosYaNombrados es global y SOBREVIVE entre tests (y entre repeticiones con
// -count=N): sin esto, el segundo pase daría por nombrado un hilo que en ese pase nunca se tocó.
// Lo destapó correr la suite con -count=3.
func olvidarNombrado(phone string) { hilosYaNombrados.Delete(phone) }

// esperarTexto espera a que el ÚLTIMO mensaje enviado contenga un fragmento. Es la forma robusta
// de sincronizar: mide el efecto que importa (lo que el grupo acaba viendo) y no se rompe porque
// aparezca una llamada nueva en el camino.
func esperarTexto(t *testing.T, tg *telegramFalso, fragmento string) {
	t.Helper()
	esperarCondicion(t, "el mensaje contiene "+fragmento, func() bool {
		return strings.Contains(tg.ultimoTexto(), fragmento)
	})
}

// esperarLlamadas espera a que se hayan hecho al menos n llamadas (los avisos son asíncronos).
func esperarLlamadas(t *testing.T, tg *telegramFalso, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		tg.mu.Lock()
		hechas := len(tg.metodos)
		tg.mu.Unlock()
		if hechas >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no llegaron %d llamadas a Telegram", n)
}

// LA PRUEBA CENTRAL: cuatro etapas generan UN mensaje y tres ediciones, no cuatro mensajes.
func TestElCicloCompletoDejaUnSoloMensajeEnElGrupo(t *testing.T) {
	const phone = "593999400001"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	// Se espera el EFECTO de cada paso, no un número de llamadas: contar es frágil (una llamada
	// nueva en cualquier sitio desplaza el número y el test sigue antes de tiempo).
	n.EstadoCliente(phone, "María Pérez", conversation.EtapaEscribio, "", 0)
	esperarTexto(t, tg, "Escribió")
	n.EstadoCliente(phone, "María Pérez", conversation.EtapaPidioGas, "2 x GAS 15KG (BLANCO)", 0)
	esperarTexto(t, tg, "GAS 15KG")
	n.EstadoCliente(phone, "María Pérez", conversation.EtapaRegistrado, "", 942)
	esperarTexto(t, tg, "942")
	n.CerrarTarjeta(phone, "María Pérez", false)
	esperarTexto(t, tg, "✅ Entregado")

	if creados := tg.cuenta("sendMessage"); creados != 1 {
		t.Errorf("se crearon %d mensajes; debía ser 1 que se va editando. Cuatro avisos por cliente "+
			"vuelven el grupo ilegible, y un grupo que nadie lee es peor que no tener avisos", creados)
	}
	if ediciones := tg.cuenta("editMessageText"); ediciones != 3 {
		t.Errorf("hubo %d ediciones; debían ser 3 (pidió gas, registrado, entregado)", ediciones)
	}
	// Y el mensaje final cuenta la historia completa.
	final := tg.ultimoTexto()
	for _, esperado := range []string{"María Pérez", "Entregado", "942", "GAS 15KG"} {
		if !strings.Contains(final, esperado) {
			t.Errorf("la tarjeta final no menciona %q: %q", esperado, final)
		}
	}
}

// LA TARJETA NO RETROCEDE. Los avisos llegan de goroutines distintas (el webhook, el reintento de
// repartidor a los 5 min, el aviso de entrega del backend) y pueden desordenarse. Una tarjeta que
// vuelve de "Entregado" a "Pidió gas" hace que el grupo desconfíe de todas las demás.
func TestLaTarjetaNoRetrocede(t *testing.T) {
	const phone = "593999400002"
	tg := nuevoTelegramFalso(t)
	n, store := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Juan", conversation.EtapaRegistrado, "1 x GAS 15KG", 700)
	esperarLlamadas(t, tg, 1)
	// Llega tarde un aviso de una etapa ANTERIOR.
	n.EstadoCliente(phone, "Juan", conversation.EtapaPidioGas, "", 0)
	// No hay forma de esperar algo que no debe pasar: se da margen y se comprueba el estado.
	time.Sleep(60 * time.Millisecond)

	tarjeta, _ := store.GetTarjetaEstado(phone)
	if tarjeta.Etapa != conversation.EtapaRegistrado {
		t.Errorf("la tarjeta retrocedió a la etapa %d: el grupo vería un pedido registrado volver "+
			"a 'pidió gas'", tarjeta.Etapa)
	}
	if llamadas := tg.cuenta("sendMessage") + tg.cuenta("editMessageText"); llamadas != 1 {
		t.Errorf("un aviso atrasado generó %d llamadas; no debía generar ninguna extra", llamadas)
	}
}

// El detalle del pedido SE CONSERVA entre etapas: pasar "" no lo borra. Si se perdiera, la tarjeta
// final diría "Entregado" sin decir qué se entregó.
func TestElDetalleDelPedidoSeConservaEntreEtapas(t *testing.T) {
	const phone = "593999400003"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Ana", conversation.EtapaPidioGas, "3 x GAS 23KG (NARANJA)", 0)
	esperarTexto(t, tg, "GAS 23KG")
	n.EstadoCliente(phone, "Ana", conversation.EtapaRegistrado, "", 801) // sin detalle
	esperarTexto(t, tg, "801")

	if final := tg.ultimoTexto(); !strings.Contains(final, "GAS 23KG") {
		t.Errorf("se perdió el detalle del pedido al avanzar de etapa: %q", final)
	}
}

// Un ERROR no borra la etapa: un pedido puede estar registrado Y haber tenido un problema que
// alguien debe mirar. Las dos cosas tienen que verse a la vez.
func TestUnErrorNoBorraElAvanceDelPedido(t *testing.T) {
	const phone = "593999400004"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Luis", conversation.EtapaRegistrado, "1 x GAS 15KG", 900)
	esperarTexto(t, tg, "900")
	n.ErrorCliente(phone, "Luis", "No se pudo avisar la entrega")
	esperarTexto(t, tg, "No se pudo avisar la entrega")

	final := tg.ultimoTexto()
	if !strings.Contains(final, "No se pudo avisar la entrega") {
		t.Errorf("el error no aparece en la tarjeta: %q", final)
	}
	if !strings.Contains(final, "Registrado") || !strings.Contains(final, "900") {
		t.Errorf("el error borró el avance del pedido: %q", final)
	}
}

// Cancelar es un final ALTERNATIVO, no un paso más: mostrar "Entregado" en gris al lado sugeriría
// que el gas todavía puede llegar.
func TestElPedidoCanceladoNoAparentaSeguirEnCamino(t *testing.T) {
	const phone = "593999400005"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Rosa", conversation.EtapaRegistrado, "1 x GAS 15KG", 950)
	esperarTexto(t, tg, "950")
	n.CerrarTarjeta(phone, "Rosa", true)
	esperarTexto(t, tg, "Cancelado")

	final := tg.ultimoTexto()
	if !strings.Contains(final, "Cancelado") {
		t.Errorf("la tarjeta no dice que se canceló: %q", final)
	}
	if strings.Contains(final, "Entregado") {
		t.Errorf("un pedido cancelado sigue mostrando 'Entregado': parece que el gas va en camino. %q", final)
	}
}

// Al cerrar el ciclo se OLVIDA la tarjeta: el próximo pedido del cliente abre una nueva en vez de
// reescribir la historia del anterior.
func TestTrasCerrarElCicloElProximoPedidoAbreTarjetaNueva(t *testing.T) {
	const phone = "593999400006"
	tg := nuevoTelegramFalso(t)
	n, store := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Pedro", conversation.EtapaRegistrado, "1 x GAS 15KG", 960)
	esperarLlamadas(t, tg, 1)
	n.CerrarTarjeta(phone, "Pedro", false)
	// Se espera el EFECTO (que la tarjeta se olvide), no un número de llamadas.
	esperarCondicion(t, "la tarjeta se olvida al cerrar el ciclo", func() bool {
		_, hay := store.GetTarjetaEstado(phone)
		return !hay
	})

	// Pedido nuevo: tiene que CREAR otro mensaje, no editar el cerrado.
	n.EstadoCliente(phone, "Pedro", conversation.EtapaEscribio, "", 0)
	esperarCondicion(t, "el pedido nuevo abre su propia tarjeta", func() bool {
		return tg.cuenta("sendMessage") == 2
	})
}

// AVISOS SIMULTÁNEOS DEL MISMO CLIENTE NO DUPLICAN LA TARJETA. Es un caso real: el webhook y el
// reintento de repartidor corren en goroutines distintas y pueden coincidir.
func TestAvisosSimultaneosNoDuplicanLaTarjeta(t *testing.T) {
	const phone = "593999400007"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); n.EstadoCliente(phone, "Carmen", conversation.EtapaPidioGas, "1 x GAS", 0) }()
	}
	wg.Wait()
	esperarLlamadas(t, tg, 1)
	time.Sleep(80 * time.Millisecond) // margen para que lleguen las que fueran a llegar

	if creados := tg.cuenta("sendMessage"); creados != 1 {
		t.Errorf("ocho avisos a la vez crearon %d tarjetas; debía ser 1", creados)
	}
}

// Y un Notifier nil (Telegram sin configurar) no hace nada ni revienta: es la regla del paquete,
// un observador nunca puede tumbar el proceso que atiende a los clientes.
func TestSinTelegramConfiguradoNoPasaNada(t *testing.T) {
	var n *Notifier
	n.EstadoCliente("593999400008", "X", conversation.EtapaPidioGas, "1 x GAS", 0)
	n.ErrorCliente("593999400008", "X", "algo")
	n.CerrarTarjeta("593999400008", "X", false)
}

// EL HILO SE RENOMBRA CUANDO SE APRENDE EL NOMBRE.
//
// Un hilo nace en el instante del primer mensaje. Si en ese momento no se sabía el nombre, el
// título queda como "+593994582191" y Telegram NO lo actualiza solo: en el grupo se ven decenas
// así y no se distingue quién es quién (captura del dueño, 18/09).
func TestElHiloSeRenombraCuandoSeAprendeElNombre(t *testing.T) {
	const phone = "593994582191"
	olvidarNombrado(phone)
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone) // el hilo YA existe, creado sin nombre

	n.EstadoCliente(phone, "María Pérez", conversation.EtapaEscribio, "", 0)
	esperarLlamadas(t, tg, 2) // el renombrado + la tarjeta

	if tg.cuenta("editForumTopic") != 1 {
		t.Errorf("no se renombró el hilo: seguiría mostrando solo el número. Llamadas: %v", tg.metodos)
	}
}

// Pero NO se renombra en cada mensaje: sería una llamada a Telegram por cada cosa que escriba el
// cliente, para cambiar un título que ya está bien.
func TestElHiloNoSeRenombraEnCadaMensaje(t *testing.T) {
	const phone = "593994582194"
	olvidarNombrado(phone)
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Juan Pérez", conversation.EtapaEscribio, "", 0)
	esperarLlamadas(t, tg, 2)
	n.EstadoCliente(phone, "Juan Pérez", conversation.EtapaPidioGas, "1 x GAS", 0)
	esperarLlamadas(t, tg, 3)

	if veces := tg.cuenta("editForumTopic"); veces != 1 {
		t.Errorf("se renombró %d veces el mismo hilo; basta una", veces)
	}
}

// Sin nombre no se renombra nada: no hay con qué mejorar el título.
func TestSinNombreNoSeTocaElTituloDelHilo(t *testing.T) {
	const phone = "593994582195"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "", conversation.EtapaEscribio, "", 0)
	esperarLlamadas(t, tg, 1)

	if tg.cuenta("editForumTopic") != 0 {
		t.Error("se intentó renombrar un hilo sin saber el nombre del cliente")
	}
}

// ═══ DOS BUGS VISTOS EN PRODUCCIÓN EL 18/09 (captura del dueño) ═══

// BUG 1: SE CREABAN DOS TEMAS PARA EL MISMO NÚMERO.
//
// Al llegar el primer mensaje, AvisarInicio y EstadoCliente arrancan sus goroutines casi a la vez.
// Las dos llamaban a hiloDe, las dos leían "no hay hilo" antes de que ninguna lo guardara, y las
// dos creaban uno. El grupo terminaba con dos temas del mismo cliente y la conversación partida.
func TestNoSeCreanDosTemasParaElMismoCliente(t *testing.T) {
	const phone = "593939399431"
	tg := nuevoTelegramFalso(t)
	store := conversation.NewMemStore() // SIN hilo previo: se tiene que crear
	n := &Notifier{
		token: "t", chatID: "c", avisarInicio: true,
		store: store, cliente: tg.servidor.Client(), base: tg.servidor.URL,
	}

	// Los dos avisos del primer mensaje, a la vez, como en producción.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); n.AvisarInicio(phone, "Yovany Morocho", "hola") }()
	go func() {
		defer wg.Done()
		n.EstadoCliente(phone, "Yovany Morocho", conversation.EtapaEscribio, "", 0)
	}()
	wg.Wait()
	esperarCondicion(t, "se creó el tema del cliente", func() bool {
		_, hay := store.GetTelegramThread(phone)
		return hay
	})
	time.Sleep(80 * time.Millisecond) // margen por si el segundo tema fuera a crearse

	if temas := tg.cuenta("createForumTopic"); temas != 1 {
		t.Errorf("se crearon %d temas para el mismo cliente; debía ser 1. El grupo queda con la "+
			"conversación partida en dos", temas)
	}
}

// BUG 2: LA TARJETA DECÍA "ENTREGADO" EN UN PEDIDO QUE NI SIQUIERA SE HABÍA HECHO.
//
// Se pintaba la fila entera y lo pendiente iba en cursiva. Sobre el papel se distingue; en
// Telegram no —y en la notificación del móvil el formato se pierde del todo—. El dueño vio
// "Escribió → Pidió gas → Registrado → Entregado" en alguien que solo había escrito.
//
// Un aviso que se lee mal es un aviso que miente.
func TestLaTarjetaNoMencionaEtapasQueNoHanOcurrido(t *testing.T) {
	const phone = "593939399432"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	// El cliente SOLO escribió.
	n.EstadoCliente(phone, "Yovany Morocho", conversation.EtapaEscribio, "", 0)
	esperarTexto(t, tg, "Escribió")

	texto := tg.ultimoTexto()
	for _, futuro := range []string{"Entregado", "Registrado", "Pidió gas"} {
		if strings.Contains(texto, futuro) {
			t.Errorf("la tarjeta menciona %q cuando el cliente solo escribió: se lee como que el "+
				"pedido ya pasó por ahí. Tarjeta: %q", futuro, texto)
		}
	}
}

// Y la fila CRECE conforme avanza: lo que se ve, ocurrió.
func TestLaFilaCreceConformeAvanzaElPedido(t *testing.T) {
	const phone = "593939399433"
	tg := nuevoTelegramFalso(t)
	n, _ := notificadorDePrueba(t, tg, phone)

	n.EstadoCliente(phone, "Ana", conversation.EtapaPidioGas, "2 x GAS 15KG", 0)
	esperarTexto(t, tg, "Pidió gas")
	if texto := tg.ultimoTexto(); !strings.Contains(texto, "Escribió") {
		t.Errorf("se perdió la etapa anterior: %q", texto)
	}
	if texto := tg.ultimoTexto(); strings.Contains(texto, "Entregado") {
		t.Errorf("aparece una etapa futura: %q", texto)
	}

	n.EstadoCliente(phone, "Ana", conversation.EtapaRegistrado, "", 777)
	esperarTexto(t, tg, "Registrado")
	if texto := tg.ultimoTexto(); strings.Contains(texto, "Entregado") {
		t.Errorf("el pedido registrado ya aparece como entregado: %q", texto)
	}

	n.CerrarTarjeta(phone, "Ana", false)
	esperarTexto(t, tg, "Entregado")
}
