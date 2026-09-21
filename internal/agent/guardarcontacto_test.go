package agent

import (
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// AL CERRAR EL PEDIDO SE LE PIDE AL CLIENTE QUE NOS GUARDE EN SUS CONTACTOS.
//
// Pedido de David (21/09): "al finalizar el pedido acolítenlo para que el man diga algo como
// guárdame en tus contactos favoritos o cosas así — algo como 'recuerda que Ubi te contacta con
// el repartidor de gas más cercano'".
//
// POR QUÉ ESTE MOMENTO Y NO OTRO. El cliente acaba de recibir su gas y de calificar: es el
// instante de más buena voluntad de toda la conversación, y el único en el que pedirle algo no
// interrumpe nada. Pedírselo antes —mientras espera, o a mitad del pedido— sería ruido en medio
// de lo que vino a hacer.
//
// Y ES LO QUE DECIDE SI VUELVE. Un número guardado se busca por nombre; uno sin guardar se
// pierde entre los chats y el cliente termina pidiendo por teléfono o a otro.

// EL CAMINO FELIZ: la calificación SÍ se registra en el backend.
//
// Hace falta montar el backend falso y la cuenta del cliente, y no es un adorno: sin ellos
// calificarConductor falla y sale por la rama de error, así que la frase del agradecimiento con
// el nombre del conductor NO se ejecuta nunca. La primera versión de este test no los montaba y
// el mutante que le quitaba la invitación a esa rama SOBREVIVIÓ.
func TestAlCalificarSePideGuardarElContacto(t *testing.T) {
	const from = "593999900100"
	esp := nuevoBackendEspia(t)
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = georoutes.NewClient(esp.servidor.URL)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
	store.SetPendingRating(from, conversation.PendingRating{PedidoID: 271, Conductor: "Juan Picón"})

	respuesta, manejado := ag.ResponderCalificacion(from, "⭐⭐⭐⭐⭐ 5")

	if !manejado {
		t.Fatal("no se resolvió la calificación")
	}
	// Se comprueba que pasó por la rama BUENA y no por la de error, que es lo que engañó a la
	// primera versión de este test.
	if !strings.Contains(respuesta, "Juan Picón") {
		t.Fatalf("la calificación no se registró (salió por la rama de error): %q", respuesta)
	}
	// Se compara con el mensaje REAL, no con la palabra "contacto": buscar una palabra suelta
	// deja pasar un mutante que la conserve por casualidad en otra frase.
	if !strings.Contains(respuesta, MensajeGuardarContacto()) {
		t.Errorf("no se le pide que nos guarde en contactos.\nse esperaba que contuviera: %q\nsalió: %q",
			MensajeGuardarContacto(), respuesta)
	}
}

// TAMBIÉN CUANDO LA CALIFICACIÓN NO SE PUDO REGISTRAR. Que el backend falle es un problema
// nuestro; el cliente igual recibió su gas y el ciclo se cerró igual para él.
func TestSiLaCalificacionFallaTambienSePideElContacto(t *testing.T) {
	const from = "593999900101"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	// Sin cuenta ni backend, calificarConductor no llega a registrar nada.
	store.SetPendingRating(from, conversation.PendingRating{PedidoID: 999, Conductor: "Ana"})

	respuesta, manejado := ag.ResponderCalificacion(from, "⭐ 1")

	if !manejado {
		t.Fatal("no se resolvió la calificación")
	}
	if !strings.Contains(respuesta, MensajeGuardarContacto()) {
		t.Errorf("con la calificación fallida no se le pide guardar el contacto: %q", respuesta)
	}
}

// EL AVISO DE ENTREGA TAMBIÉN LO LLEVA, y es el que más importa: ese lo recibe TODO el que
// recibe su gas, mientras que la mayoría no llega a calificar nunca. Si solo estuviera en el
// agradecimiento por calificar, no le llegaría a casi nadie.
//
// OJO CON CÓMO SE COMPRUEBA. La primera versión buscaba el texto "agent.MensajeGuardarContacto()"
// en main.go y NO SERVÍA: al quitar la línea que lo usa, el comentario que la explica seguía
// mencionándola y el test pasaba igual. El mutante sobrevivió. Ahora se busca la LÍNEA de código
// concreta, sin comentarios.
func TestElAvisoDeEntregaTambienPideGuardarElContacto(t *testing.T) {
	src := leerSinComentarios(t, "../../cmd/bot/main.go")
	if !strings.Contains(src, "msg += agent.MensajeGuardarContacto()") {
		t.Error("notifyOrderFinished no incluye la invitación a guardar el contacto: solo la " +
			"verían los que califican, que son los menos")
	}
}

// Y el camino del cliente que ESCRIBE su nota en vez de tocar el botón (lo cierra el modelo):
// la despedida tiene que decir lo mismo, o el mensaje de marca sale distinto según cómo
// calificó, que es justo lo que no puede pasar.
func TestElCaminoEscritoTambienLoLleva(t *testing.T) {
	src := leerSinComentarios(t, "agent.go")
	if !strings.Contains(src, "MensajeGuardarContacto()") {
		t.Error("calificarConductor no le pasa el texto al modelo: quien escribe su calificación " +
			"en vez de tocar el botón no recibe la invitación")
	}
}

// leerSinComentarios devuelve el código del archivo con las líneas de comentario quitadas.
//
// Existe por el fallo de arriba: un test que busca una llamada en el texto crudo del archivo
// también la encuentra en el comentario que la explica, así que pasa aunque la llamada real se
// haya borrado. Comprobar el cableado leyendo la fuente solo vale si se lee el código.
func leerSinComentarios(t *testing.T, archivo string) string {
	t.Helper()
	crudo, err := os.ReadFile(archivo)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", archivo, err)
	}
	var b strings.Builder
	for _, linea := range strings.Split(string(crudo), "\n") {
		if strings.HasPrefix(strings.TrimSpace(linea), "//") {
			continue
		}
		b.WriteString(linea)
		b.WriteString("\n")
	}
	return b.String()
}

// El mensaje tiene que nombrar a UBI: es la marca que el cliente tiene que buscar después en su
// agenda. "Guárdanos" a secas no le dice cómo nos va a encontrar.
func TestElMensajeNombraALaMarca(t *testing.T) {
	msg := MensajeGuardarContacto()
	if !strings.Contains(msg, "Ubi") {
		t.Errorf("el mensaje no nombra a Ubi, que es lo que el cliente va a buscar en su agenda: %q", msg)
	}
	// Corto: va pegado al agradecimiento, no puede ser un párrafo aparte que nadie lee.
	if len([]rune(msg)) > 220 {
		t.Errorf("el mensaje es demasiado largo (%d caracteres): se lee como spam al final de "+
			"una conversación que ya terminó", len([]rune(msg)))
	}
}
