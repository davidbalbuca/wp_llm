// Package notify manda avisos de operación a un grupo de Telegram: cuándo empieza a hablar un
// cliente y qué se rompió. Es para la etapa de TEST PRODUCTIVO, donde hace falta enterarse rápido
// sin abrir el panel.
//
// REGLA DE ESTE PAQUETE: es un observador, nunca un participante. Si el token está mal, si no hay
// red o si Telegram responde error, se escribe una línea en el log y se sigue. Nada de lo que pase
// aquí puede cortar la conversación del cliente ni cambiar lo que recibe por WhatsApp. Por eso
// todo entra por Avisar*/Fallo, que arrancan una goroutine y no devuelven error: quien llama no
// tiene nada que decidir con el resultado.
//
// Los mensajes van a un grupo con TEMAS (forum) activados: cada cliente tiene su hilo, así el
// grupo no es un muro cronológico donde se mezclan cinco conversaciones. Los errores van a un
// hilo aparte. Si el grupo no es forum, todo cae en el general y el bot igual funciona.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"wp-llm-gas/internal/conversation"
)

// ventanaAviso es cuánto dura una "sesión" a efectos del aviso de inicio: dentro de este tiempo,
// el mismo cliente NO vuelve a generar un aviso verde. Es el mismo criterio de sesión que usa el
// bot para limpiar el historial (conversation.SessionGap), para que el grupo cuente lo mismo que
// el bot considera una conversación nueva.
const ventanaAviso = conversation.SessionGap

// tiempoLimite acota cada llamada a la API. Corto a propósito: un aviso que tarda no sirve, y
// nada de lo que hay detrás justifica retener una goroutine.
const tiempoLimite = 8 * time.Second

// topeFallosIguales / ventanaTope limitan los avisos repetidos: si Meta se cae, los 5 notifyOrder*
// fallan en cadena y sin esto el grupo recibe cientos de mensajes iguales (y se silencia, que es
// justo lo que no queremos). Se cuenta por MOTIVO, no por cliente: el motivo es lo que se repite.
const (
	topeFallosIguales = 5
	ventanaTope       = 30 * time.Minute
)

// Notifier manda los avisos. Se construye una vez en el arranque y se comparte; sus métodos son
// seguros para usar desde varias goroutines.
type Notifier struct {
	token        string
	chatID       string
	avisarInicio bool
	store        conversation.Store
	cliente      *http.Client

	mu sync.Mutex
	// Hilos fijos del grupo: se resuelven una sola vez (el primer aviso los crea).
	hiloErrores       int64
	hiloSinRepartidor int64
	hiloSondeo        int64
	// vistos cuenta fallos por motivo dentro de ventanaTope, para el anti-inundación.
	vistos map[string]*contador
}

type contador struct {
	n       int
	desde   time.Time
	avisado bool // ya se dijo en el grupo que se está silenciando este motivo
}

// New construye el Notifier. Si falta el token o el chat ID devuelve nil: es la forma de apagar
// la función sin condicionales repartidos por el código — los métodos sobre un *Notifier nil no
// hacen nada, así que quien llama no necesita comprobar si está configurado.
func New(token, chatID string, avisarInicio bool, store conversation.Store) *Notifier {
	token, chatID = strings.TrimSpace(token), strings.TrimSpace(chatID)
	if token == "" || chatID == "" {
		log.Printf("[telegram] sin TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID; los avisos quedan apagados")
		return nil
	}
	log.Printf("[telegram] avisos activos (aviso de inicio de conversación: %v)", avisarInicio)
	return &Notifier{
		token:        token,
		chatID:       chatID,
		avisarInicio: avisarInicio,
		store:        store,
		cliente:      &http.Client{Timeout: tiempoLimite},
		vistos:       make(map[string]*contador),
	}
}

// Activo dice si hay a dónde avisar. Sirve para saltarse trabajo de preparación (armar textos)
// cuando la función está apagada.
func (n *Notifier) Activo() bool { return n != nil }

// Default es el notificador del proceso. Existe para que paquetes hondos (agent) puedan avisar
// sin arrastrar la dependencia por la firma de sus constructores, que tendrían que propagarla
// hasta sitios que no tienen nada que ver con esto. Se asigna UNA vez en el arranque, antes de
// que exista cualquier goroutine, y de ahí en adelante es de solo lectura: por eso no lleva
// mutex. Vale nil cuando Telegram no está configurado, y los métodos sobre nil no hacen nada.
var Default *Notifier

// AvisarInicio manda el aviso verde de que un cliente empezó a hablar. Silencioso (no vibra el
// teléfono) y UNO POR SESIÓN: el segundo, tercero y décimo mensaje del mismo cliente no avisan.
// `nombre` puede venir vacío (cliente nuevo, todavía sin datos).
func (n *Notifier) AvisarInicio(phone, nombre, primerMensaje string) {
	if n == nil || !n.avisarInicio {
		return
	}
	// La marca se toma ANTES de la goroutine: si dos mensajes llegan a la vez, solo uno pasa.
	if !n.store.MarcarAvisoInicio(phone, ventanaAviso) {
		return
	}
	go n.protegido("AvisarInicio", func() {
		quien := nombre
		if quien == "" {
			quien = "cliente nuevo"
		}
		texto := fmt.Sprintf("🟢 <b>%s</b> inició una conversación\n<code>+%s</code>",
			html.EscapeString(quien), html.EscapeString(phone))
		if m := strings.TrimSpace(primerMensaje); m != "" {
			texto += "\n\n💬 " + html.EscapeString(recortar(m, 200))
		}
		n.enviar(n.hiloDe(phone, nombre), texto, true)
	})
}

// Fallo avisa de algo que se rompió: va al hilo del cliente (para verlo en su contexto) Y al hilo
// de errores, este último CON sonido. `motivo` es la etiqueta corta que agrupa el anti-inundación.
// Si phone viene vacío, va solo al hilo de errores.
func (n *Notifier) Fallo(phone, nombre, motivo, detalle string) {
	if n == nil {
		return
	}
	if !n.permitido(motivo) {
		return
	}
	go n.protegido("Fallo", func() {
		cuerpo := fmt.Sprintf("🔴 <b>%s</b>", html.EscapeString(motivo))
		if phone != "" {
			quien := nombre
			if quien == "" {
				quien = "cliente"
			}
			cuerpo += fmt.Sprintf("\n%s <code>+%s</code>", html.EscapeString(quien), html.EscapeString(phone))
		}
		if d := strings.TrimSpace(detalle); d != "" {
			cuerpo += "\n\n<pre>" + html.EscapeString(recortar(d, 500)) + "</pre>"
		}
		// Al hilo de errores, con sonido: es lo que hay que atender.
		n.enviar(n.hiloErroresID(), cuerpo, false)
		// Y en el hilo del cliente, silencioso, para que su historia quede completa.
		if phone != "" {
			if hilo := n.hiloDe(phone, nombre); hilo != 0 {
				n.enviar(hilo, cuerpo, true)
			}
		}
	})
}

// SinRepartidor avisa de un pedido que quedó sin conductor: el cliente tiene ubicación, producto
// y cantidad, pero nadie se lo va a llevar salvo que una persona lo gestione.
//
// Va aparte de Fallo a propósito, y con su propio hilo. No es un error -el bot buscó, no encontró
// y se lo dijo al cliente, todo correcto- sino una VENTA en riesgo, y las dos cosas se atienden
// distinto. Mezcladas en el mismo hilo terminarían ignorándose las dos.
//
// Tampoco pasa por el anti-inundación: si cinco clientes se quedan sin repartidor a la vez, hay
// que ver los cinco. Cada uno es un cliente distinto esperando, no la misma alerta repetida.
func (n *Notifier) SinRepartidor(phone, nombre, pedido, motivo string, lat, lng float64) {
	if n == nil {
		return
	}
	go n.protegido("SinRepartidor", func() {
		quien := nombre
		if quien == "" {
			quien = "cliente"
		}
		texto := fmt.Sprintf("🟠 <b>PEDIDO SIN REPARTIDOR</b> — hay que gestionarlo\n\n"+
			"<b>%s</b>\n<code>+%s</code>\n📦 %s",
			html.EscapeString(quien), html.EscapeString(phone), html.EscapeString(pedido))
		if lat != 0 || lng != 0 {
			texto += fmt.Sprintf("\n📍 <a href=\"https://maps.google.com/?q=%.6f,%.6f\">ver ubicación</a>", lat, lng)
		}
		if motivo != "" {
			texto += "\n\n" + html.EscapeString(motivo)
		}
		// Con sonido: alguien tiene que hacer algo, y cuanto antes.
		n.enviar(n.hiloSinRepartidorID(), texto, false)
		if hilo := n.hiloDe(phone, nombre); hilo != 0 {
			n.enviar(hilo, texto, true)
		}
	})
}

// Sondeo avisa de alguien que parece estar sacando información del negocio en vez de pedir gas:
// preguntas por la operación interna, por los números, por datos de otros clientes, o intentos de
// manipular al asistente para que se salte sus reglas.
//
// NO es una alarma de que algo se filtró: el bot solo habla de gas y usa el catálogo, así que no
// tiene nada que contar. Es para que una persona MIRE quién está preguntando — el patrón de una
// conversación entera de preguntas raras dice algo que un mensaje suelto no dice.
//
// Silencioso a propósito: no hay nada que atender de urgencia, y una alerta que suena por algo
// que no requiere acción inmediata es la que hace que se silencie el grupo entero.
func (n *Notifier) Sondeo(phone, nombre, detalle string) {
	if n == nil {
		return
	}
	go n.protegido("Sondeo", func() {
		quien := nombre
		if quien == "" {
			quien = "desconocido"
		}
		texto := fmt.Sprintf("🕵️ <b>POSIBLE SONDEO</b> — alguien pregunta cosas que no son del servicio\n\n"+
			"<b>%s</b>\n<code>+%s</code>\n\n%s\n\n"+
			"<i>El bot no le contestó nada de esto (solo habla de gas). Mira el chat si te parece raro.</i>",
			html.EscapeString(quien), html.EscapeString(phone), html.EscapeString(detalle))
		n.enviar(n.hiloSondeoID(), texto, true)
		if hilo := n.hiloDe(phone, nombre); hilo != 0 {
			n.enviar(hilo, texto, true)
		}
	})
}

// Resumen manda un texto ya armado al hilo de errores (parte diaria). Silencioso.
func (n *Notifier) Resumen(texto string) {
	if n == nil {
		return
	}
	go n.protegido("Resumen", func() { n.enviar(n.hiloErroresID(), texto, true) })
}

// permitido aplica el anti-inundación: deja pasar hasta topeFallosIguales del mismo motivo dentro
// de ventanaTope. Al llegar al tope manda UN aviso de que silencia ese motivo y calla el resto.
func (n *Notifier) permitido(motivo string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, ok := n.vistos[motivo]
	if !ok || time.Since(c.desde) > ventanaTope {
		n.vistos[motivo] = &contador{n: 1, desde: time.Now()}
		return true
	}
	c.n++
	if c.n <= topeFallosIguales {
		return true
	}
	if !c.avisado {
		c.avisado = true
		aviso := fmt.Sprintf("🔕 <b>%s</b> se repitió más de %d veces en %s. Silencio los siguientes "+
			"hasta que pasen %s; míralo en el panel de Soporte.",
			html.EscapeString(motivo), topeFallosIguales, ventanaTope, ventanaTope)
		go n.protegido("aviso de silencio", func() { n.enviar(n.hiloErroresID(), aviso, false) })
	}
	return false
}

// hiloDe devuelve el hilo del cliente, creándolo la primera vez. Devuelve 0 si no se pudo crear
// (grupo sin temas o sin permiso): con 0 el mensaje cae en el general, que es un degradado
// aceptable — se pierde el orden, no el aviso.
func (n *Notifier) hiloDe(phone, nombre string) int64 {
	if id, ok := n.store.GetTelegramThread(phone); ok {
		return id
	}
	titulo := "+" + phone
	if nombre != "" {
		titulo += " — " + nombre
	}
	id, err := n.crearHilo(recortar(titulo, 128), 7322096) // azul
	if err != nil {
		log.Printf("[telegram] no se pudo crear el hilo de %s: %v", phone, err)
		return 0
	}
	n.store.SetTelegramThread(phone, id)
	return id
}

// hiloErroresID / hiloSinRepartidorID son los dos hilos FIJOS del grupo (no dependen del
// cliente). Se crean la primera vez que hacen falta y se recuerdan en memoria: si el bot
// reinicia se crea uno nuevo, cosa asumible para hilos de bandeja.
func (n *Notifier) hiloErroresID() int64 {
	return n.hiloFijo(&n.hiloErrores, "⚠️ Errores del sistema", 16478047) // rojo
}

func (n *Notifier) hiloSinRepartidorID() int64 {
	return n.hiloFijo(&n.hiloSinRepartidor, "🟠 Pedidos sin atender", 16766590) // naranja
}

func (n *Notifier) hiloSondeoID() int64 {
	return n.hiloFijo(&n.hiloSondeo, "🕵️ Posibles sondeos", 9367192) // morado
}

// hiloFijo devuelve el hilo apuntado por destino, creándolo la primera vez. La creación queda
// FUERA del lock: es una llamada de red y retenerlo dejaría bloqueado a todo el que quiera
// avisar. Si dos goroutines entran a la vez puede crearse un hilo de más; se prefiere eso a
// serializar los avisos detrás de una petición HTTP.
func (n *Notifier) hiloFijo(destino *int64, nombre string, color int) int64 {
	n.mu.Lock()
	if *destino != 0 {
		defer n.mu.Unlock()
		return *destino
	}
	n.mu.Unlock()

	id, err := n.crearHilo(nombre, color)
	if err != nil {
		log.Printf("[telegram] no se pudo crear el hilo %q: %v", nombre, err)
		return 0
	}
	n.mu.Lock()
	*destino = id
	n.mu.Unlock()
	return id
}

func (n *Notifier) crearHilo(nombre string, color int) (int64, error) {
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ThreadID int64 `json:"message_thread_id"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := n.llamar("createForumTopic", map[string]any{
		"chat_id": n.chatID, "name": nombre, "icon_color": color,
	}, &resp); err != nil {
		return 0, err
	}
	if !resp.OK {
		return 0, fmt.Errorf("telegram: %s", resp.Description)
	}
	return resp.Result.ThreadID, nil
}

// enviar manda el mensaje. hilo 0 = al general. silencioso quita la vibración del teléfono.
func (n *Notifier) enviar(hilo int64, texto string, silencioso bool) {
	cuerpo := map[string]any{
		"chat_id":              n.chatID,
		"text":                 texto,
		"parse_mode":           "HTML",
		"disable_notification": silencioso,
	}
	if hilo != 0 {
		cuerpo["message_thread_id"] = hilo
	}
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := n.llamar("sendMessage", cuerpo, &resp); err != nil {
		log.Printf("[telegram] error enviando: %v", err)
		return
	}
	if !resp.OK {
		log.Printf("[telegram] rechazado: %s", resp.Description)
	}
}

func (n *Notifier) llamar(metodo string, cuerpo map[string]any, salida any) error {
	b, err := json.Marshal(cuerpo)
	if err != nil {
		return err
	}
	url := "https://api.telegram.org/bot" + n.token + "/" + metodo
	resp, err := n.cliente.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(salida)
}

// protegido corre fn aislando cualquier panic. Un bug en el aviso (un nil, un índice) no puede
// tumbar el proceso que está atendiendo a los clientes.
func (n *Notifier) protegido(que string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[telegram] panic recuperado en %s: %v", que, r)
		}
	}()
	fn()
}

// recortar deja el texto en n caracteres (contando runas, para no partir un emoji por la mitad).
func recortar(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
