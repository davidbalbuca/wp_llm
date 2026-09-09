// Package notify manda avisos de operación a un grupo de Telegram (inicio de conversación y
// fallos) para el test productivo.
//
// REGLA: es un observador, nunca un participante. Ningún error de Telegram puede cortar ni
// alterar la conversación del cliente; por eso todo entra por Avisar*/Fallo, que lanzan una
// goroutine y no devuelven error.
//
// Los avisos van a un grupo con TEMAS (forum): un hilo por cliente y hilos aparte para errores.
// Sin forum, todo cae en el general y el bot igual funciona.
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

// ventanaAviso: dentro de este tiempo el mismo cliente no vuelve a generar aviso verde. Igual
// criterio de sesión que usa el bot para limpiar el historial (conversation.SessionGap).
const ventanaAviso = conversation.SessionGap

// tiempoLimite acota cada llamada a la API. Corto: un aviso que tarda no sirve.
const tiempoLimite = 8 * time.Second

// topeFallosIguales / ventanaTope: anti-inundación. Si Meta se cae, los avisos fallarían en
// cadena y el grupo se llenaría de mensajes iguales (y se silenciaría). Se cuenta por MOTIVO.
const (
	topeFallosIguales = 5
	ventanaTope       = 30 * time.Minute
)

// Notifier manda los avisos. Se construye una vez y se comparte; sus métodos son concurrency-safe.
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

// New construye el Notifier. Sin token o chat ID devuelve nil (los métodos sobre *Notifier nil no
// hacen nada, así que quien llama no comprueba si está configurado).
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

// Activo dice si hay a dónde avisar (para saltarse armar textos cuando está apagado).
func (n *Notifier) Activo() bool { return n != nil }

// Default es el notificador del proceso, para que paquetes hondos (agent) avisen sin propagar la
// dependencia por sus constructores. Se asigna UNA vez en el arranque (antes de cualquier
// goroutine) y de ahí es solo lectura, por eso no lleva mutex. nil si Telegram no está configurado.
var Default *Notifier

// AvisarInicio manda el aviso verde de que un cliente empezó a hablar. Silencioso (no vibra el
// teléfono) y UNO POR SESIÓN: el segundo, tercero y décimo mensaje del mismo cliente no avisan.
// `nombre` puede venir vacío (cliente nuevo, todavía sin datos).
func (n *Notifier) AvisarInicio(phone, nombre, primerMensaje string) {
	if n == nil || !n.avisarInicio {
		return
	}
	// La marca se toma ANTES de la goroutine: con dos mensajes a la vez solo uno pasa.
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
			texto += "\n\n💬 " + html.EscapeString(conversation.Recortar(m, 200))
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
			cuerpo += "\n\n<pre>" + html.EscapeString(conversation.Recortar(d, 500)) + "</pre>"
		}
		// Al hilo de errores, con sonido y CON detalle: ahí se trabaja el caso.
		n.enviar(n.hiloErroresID(), cuerpo, false)
		// En el hilo del cliente va solo una LÍNEA silenciosa, para su contexto. Antes se mandaba
		// el texto completo a los dos sitios y salían dos avisos idénticos seguidos (reportado 08/09).
		if phone != "" {
			if hilo := n.hiloDe(phone, nombre); hilo != 0 {
				n.enviar(hilo, "🔴 "+html.EscapeString(motivo)+" <i>(detalle en Errores del sistema)</i>", true)
			}
		}
	})
}

// SinRepartidor avisa de un pedido que quedó sin conductor. Va aparte de Fallo y con su propio
// hilo: no es un error sino una VENTA en riesgo, y se atienden distinto. Tampoco pasa por el
// anti-inundación: cada cliente sin repartidor es un caso distinto que hay que ver.
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
		// Con sonido: alguien tiene que gestionarlo cuanto antes.
		n.enviar(n.hiloSinRepartidorID(), texto, false)
		if hilo := n.hiloDe(phone, nombre); hilo != 0 {
			n.enviar(hilo, texto, true)
		}
	})
}

// Sondeo avisa de alguien que parece sacar información del negocio en vez de pedir gas (operación
// interna, números, datos de otros clientes, o intentos de manipular al asistente).
//
// NO es alarma de fuga (el bot solo habla de gas): es para que una persona MIRE el patrón de la
// conversación. Silencioso a propósito: nada urgente que atender.
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

// hiloDe devuelve el hilo del cliente, creándolo la primera vez. 0 si no se pudo crear (grupo sin
// temas/permiso): el mensaje cae en el general, degradado aceptable (se pierde el orden, no el aviso).
func (n *Notifier) hiloDe(phone, nombre string) int64 {
	if id, ok := n.store.GetTelegramThread(phone); ok {
		return id
	}
	titulo := "+" + phone
	if nombre != "" {
		titulo += " — " + nombre
	}
	id, err := n.crearHilo(conversation.Recortar(titulo, 128), 7322096) // azul
	if err != nil {
		log.Printf("[telegram] no se pudo crear el hilo de %s: %v", phone, err)
		return 0
	}
	n.store.SetTelegramThread(phone, id)
	return id
}

// Claves para PERSISTIR los hilos fijos en la misma tabla que los de cliente. El prefijo "#"
// evita colisión con cualquier teléfono.
const (
	claveHiloErrores       = "#hilo-errores"
	claveHiloSinRepartidor = "#hilo-sin-repartidor"
	claveHiloSondeo        = "#hilo-sondeo"
)

// hiloErroresID / hiloSinRepartidorID / hiloSondeoID son los hilos FIJOS del grupo. Se persisten
// en BD: antes vivían solo en memoria y cada reinicio creaba un hilo duplicado (los deploys del
// 08/09 llenaron el grupo de "Errores del sistema" repetidos).
func (n *Notifier) hiloErroresID() int64 {
	return n.hiloFijo(&n.hiloErrores, claveHiloErrores, "⚠️ Errores del sistema", 16478047) // rojo
}

func (n *Notifier) hiloSinRepartidorID() int64 {
	return n.hiloFijo(&n.hiloSinRepartidor, claveHiloSinRepartidor, "🟠 Pedidos sin atender", 16766590) // naranja
}

func (n *Notifier) hiloSondeoID() int64 {
	return n.hiloFijo(&n.hiloSondeo, claveHiloSondeo, "🕵️ Posibles sondeos", 9367192) // morado
}

// hiloFijo devuelve el hilo apuntado por destino, creándolo SOLO la primera vez. Orden: memoria
// -> BD (sobrevive reinicios) -> crear.
//
// La creación queda FUERA del lock (es una llamada de red y retenerlo bloquearía a todos los que
// avisan). Dos goroutines a la vez pueden crear un hilo de más; se prefiere a serializar avisos
// detrás de un HTTP.
func (n *Notifier) hiloFijo(destino *int64, clave, nombre string, color int) int64 {
	n.mu.Lock()
	if *destino != 0 {
		defer n.mu.Unlock()
		return *destino
	}
	n.mu.Unlock()

	// ¿Ya existe de una ejecución anterior (BD)?
	if id, ok := n.store.GetTelegramThread(clave); ok {
		n.mu.Lock()
		*destino = id
		n.mu.Unlock()
		return id
	}

	id, err := n.crearHilo(nombre, color)
	if err != nil {
		log.Printf("[telegram] no se pudo crear el hilo %q: %v", nombre, err)
		return 0
	}
	n.store.SetTelegramThread(clave, id)
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
		return
	}
	// Constancia de que el aviso SALIÓ al grupo.
	log.Printf("[telegram] enviado a hilo=%d (silencioso=%v)", hilo, silencioso)
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
