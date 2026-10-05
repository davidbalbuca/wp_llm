// UN SOLO SALUDO POR CONVERSACIÓN, DECIDIDO EN CÓDIGO.
//
// INCIDENTE (593999376151, 28/09), y no es un caso aislado sino 70 clientes del histórico:
//
//	07:49:47  cliente  "Deseo pedir GAS 😄"
//	07:49:48  system   "¡Hola, Chri! 👋 Soy *Ubi* 🔥  Te conecto con el repartidor…"
//	07:49:56  model    "¡Hola, Chri! 👋 Con gusto te ayudo con tu pedido de gas 😊…"
//
// Dos saludos en nueve segundos, los dos con el mismo nombre. Y en varios casos el segundo
// CONTRADICE al primero, que es peor que repetir: "¡Hola, Doris!" seguido de "¡Buenas noches,
// Doris!", o "¡Hola, CARLOS!" + "¡Buenas tardes, Carlos!".
//
// POR QUÉ PASA. No es que el modelo no vea el saludo —`avisarCliente` hace AppendModel y eso ocurre
// mucho antes de llamarlo—. Es que el prompt le ORDENA saludar (behavior.md: "si el cliente saluda,
// salúdalo primero", "el saludo va DENTRO del cuerpo, no se omite"). El modelo obedece las dos
// cosas: el código ya saludó y él saluda otra vez. Las reglas del prompt son anteriores al saludo
// en código y nadie las revisó al añadirlo.
//
// POR QUÉ EN CÓDIGO Y NO PIDIÉNDOSELO AL PROMPT. Porque el prompt es justamente la mitad del
// problema: añadir "no saludes si ya saludaste" sería una tercera regla compitiendo con las otras
// dos, y el proyecto ya aprendió cuatro veces que el prompt PIDE y el modelo a veces no obedece.
// Aquí el código ya sabe con certeza si se presentó —lo decidió él—, así que puede quitar el saludo
// sobrante sin preguntarle a nadie.
//
// QUÉ NO SE TOCA: el saludo a mitad de conversación. Un "¡Hola, David! ¿Deseas lo mismo de la
// última vez?" conserva el suyo, porque ahí el código NO se presentó y `behavior.md:103` sigue
// vigente (nació de un incidente: "un menú sin saludo se siente como hablarle a una máquina").
package agent

import (
	"log"
	"regexp"
	"strings"

	"wp-llm-gas/internal/conversation"
)

// aperturaDeSaludo reconoce que un texto EMPIEZA con una fórmula de saludo.
//
// Anclado al principio (^) a propósito: un "hola" en medio de una frase no es un saludo de apertura,
// y este candado solo quita lo que sobra al principio. Las formas son las que el modelo usa de
// verdad en producción (93 aperturas medidas el 28/09): "¡Hola! 👋", "¡Hola, Chri! 👋",
// "Hola Sergy 👋", "¡Buenos días, Geovanny! 👋", "¡Buenas noches, Doris! 👋".
// El nombre va dentro del recorte: sin él, "¡Hola, Ana! 👋" dejaba "Ana! 👋" —un jirón de saludo que
// se lee peor que el saludo entero—. Por eso la parte del nombre es GOLOSA hasta el cierre (!/.) y
// el conjunto de caracteres se limita a letras, espacios y comas: así no se come una frase como
// "Hola, el precio es $3.25" más allá de su primera coma.
var aperturaDeSaludo = regexp.MustCompile(
	`^[¡!\s📋]*(?i:hola|buenos\s+d[íi]as|buenas\s+tardes|buenas\s+noches|qu[eé]\s+tal)` +
		`(?:[,\s]+[\p{L}\p{M}'´]+){0,3}` + // ", Ana" / " Sergy" / ", Dra. Fanny"
		`[!.…]*\s*(?:👋|🙌|😊|🔥|✨)?[\s,]*`)

// saludoDeCortesia detecta las coletillas que acompañan al saludo y tampoco aportan nada cuando el
// código ya se presentó: "Qué gusto que nos escribas", "Con gusto te ayudo", "Qué bueno que...".
// Van justo después del saludo, así que se quitan en el mismo paso o queda una frase huérfana.
// Muerde UNA SOLA frase, y sin cruzar emoji. Con un límite por caracteres se comía el contenido
// útil: "Con gusto te ayudo con tu pedido 😊 Dime el color." se quedaba en nada, porque el 😊 no es
// un signo de puntuación y el recorte siguió hasta el punto final.
// ⚠️ "Claro que sí" NO entra aquí, aunque suene a cortesía: en "Hola Sergy 👋 Claro que sí, pero
// aquí atendemos solo gas" es la frase que LLEVA la respuesta. Al incluirla, el recorte dejaba el
// mensaje vacío. Solo se quitan las fórmulas que no dicen nada por sí mismas.
var saludoDeCortesia = regexp.MustCompile(
	`^(?i:(qu[eé]\s+(gusto|bueno)|con\s+gusto\s+te\s+ayudo|soy\s+ubi)` +
		`[^.!?\n😊🙌👋🚚🔥]{0,60}[.!…]*\s*)`)

// emojiHuerfano: los emojis de cortesía que quedan al principio tras recortar el saludo. Solo los
// de saludo/cortesía, no cualquier emoji: un "📍 tu ubicación" o un "🚚 va en camino" empiezan por
// emoji a propósito y llevan información.
var emojiHuerfano = regexp.MustCompile(`^[\s😊🙌👋✨🔥📋]+`)

// formulaDeSaludo es la apertura sin el nombre, para armar el recorte con el nombre EXACTO.
const formulaDeSaludo = `^[¡!\s📋]*(?i:hola|buenos\s+d[íi]as|buenas\s+tardes|buenas\s+noches|qu[eé]\s+tal)`

// aperturaConNombre reconoce el saludo dirigido a ESTE nombre, escrito tal cual. El patrón genérico
// solo acepta letras en el nombre, y los nombres de perfil de WhatsApp traen de todo. CASO 04/10
// (593984***145): el perfil es "J.L🪽"; el genérico cortaba en el punto ("¡Buenos días, J.") y el
// cliente recibió "L🪽! 👋 No hay problema..." pegado a la bienvenida. El código ya sabe el nombre
// —es el mismo que usó para presentarse—, así que lo recorta literal en vez de adivinarlo.
func aperturaConNombre(nombre string) *regexp.Regexp {
	return regexp.MustCompile(formulaDeSaludo + `[,\s]+` + regexp.QuoteMeta(nombre) +
		`[!.…]*\s*(?:👋|🙌|😊|🔥|✨)?[\s,]*`)
}

// quitarSaludoDuplicado devuelve el texto sin la apertura de saludo. Si el texto ERA solo el
// saludo, devuelve "" y el llamador decide (ver revisarSaludoDuplicado).
//
// nombres son los nombres con los que el código conoce al cliente (el primero y el completo): si
// el saludo va dirigido a uno de ellos se recorta ese nombre literal, sea cual sea su forma. Sin
// nombres, o si el saludo usa otro, queda el patrón genérico de siempre.
func quitarSaludoDuplicado(texto string, nombres ...string) string {
	t := strings.TrimSpace(texto)
	rec := ""
	for _, n := range nombres {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if r := aperturaConNombre(n).FindString(t); len(r) > len(rec) {
			rec = r
		}
	}
	if rec == "" {
		rec = aperturaDeSaludo.FindString(t)
	}
	if rec == "" {
		return t // no empieza saludando: nada que quitar
	}
	resto := strings.TrimSpace(t[len(rec):])
	// La cortesía que seguía al saludo se va con él: sola no dice nada.
	if c := saludoDeCortesia.FindString(resto); c != "" {
		resto = strings.TrimSpace(resto[len(c):])
	}
	// Y el emoji que quedó huérfano al cortar. "Con gusto te ayudo 😊 Dime el color" dejaba
	// "😊 Dime el color": el emoji era la puntuación de la frase que ya se fue.
	resto = strings.TrimSpace(emojiHuerfano.ReplaceAllString(resto, ""))
	// La primera letra en mayúscula: al cortar "¡Hola! " lo que sigue solía ir en minúscula.
	return mayusculaInicial(resto)
}

// mayusculaInicial pone en mayúscula la primera letra, respetando emojis y signos de apertura.
func mayusculaInicial(s string) string {
	for i, r := range s {
		if r == '¿' || r == '¡' || r == '"' || r == '*' || r == ' ' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			return s[:i] + strings.ToUpper(string(r)) + s[i+len(string(r)):]
		}
		return s // ya es mayúscula, un emoji o un número: se deja
	}
	return s
}

// yaSePresentoElCodigo dice si la presentación del bot salió JUSTO ANTES de este turno.
//
// MIRA PRIMERO LA BANDERA, y esa es la corrección del 03/10. Hasta entonces solo se leía del
// HISTORIAL DEL MODELO, lo que obligaba a meter la presentación ahí (AppendModel) para que el
// candado la viera. Al unir la bienvenida al primer mensaje eso explotó en producción: el modelo
// LEÍA el párrafo y lo copiaba (bienvenida duplicada a 6 clientes) y, peor, la presentación
// desplazaba a `lastMenuText` como último turno del modelo, que es el dato con el que recuerda QUÉ
// PREGUNTÓ — así que volvía a pedir cosas ya dichas (Jefferson 5939396 repitió "1" dos veces y
// "Blanco" una más). El historial del modelo es su memoria de trabajo, no el registro de lo
// enviado: lo que el código escribe por su cuenta no tiene por qué estar ahí.
//
// Ahora el código marca una bandera de estado al presentarse (ver bienvenidaunida.go) y el
// historial queda limpio. Se conserva la lectura del historial como RESPALDO porque los avisos que
// sí salen solos (`avisarCliente`) hacen AppendModel con el saludo, y esos caminos seguirían
// necesitándolo. La marca es el texto fijo —"*UbiGas*, tu repartidor aquísito"— que solo escribe `textoBienvenidaA`.
func (a *Agent) yaSePresentoElCodigo(from string) bool {
	if a.store.YaSePresento(from) {
		return true
	}
	hist := a.store.History(from)
	// Se mira solo el ÚLTIMO turno del modelo: si el saludo fue hace diez mensajes, el modelo
	// puede volver a saludar con toda la razón (una conversación nueva del mismo día).
	for i := len(hist) - 1; i >= 0 && i >= len(hist)-2; i-- {
		c := hist[i]
		if c == nil || c.Role != "model" {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && strings.Contains(p.Text, marcaDePresentacion) {
				return true
			}
		}
		return false // el último turno del modelo no era la presentación
	}
	return false
}

// marcaDePresentacion es el trozo del saludo de bienvenida que lo identifica sin ambigüedad. Vive
// aquí junto a quien lo busca; textoBienvenidaA es quien lo escribe.
const marcaDePresentacion = "*UbiGas*, tu repartidor aquísito"

// nombresDelCliente devuelve cómo lo conoce el código: el primer nombre (el que usa la bienvenida)
// y el completo (el modelo a veces usa ese). Vacío si no se sabe.
func (a *Agent) nombresDelCliente(from string) []string {
	nombre := strings.TrimSpace(conversation.NombreDe(a.store, from))
	if nombre == "" {
		return nil
	}
	return []string{primerNombre(nombre), nombre}
}

// revisarSaludoDuplicado es el candado. Solo actúa si el CÓDIGO ya se presentó justo antes: esa es
// la única condición, y el código la comprueba él mismo en el historial.
//
// Si al quitar el saludo no queda nada (el modelo solo saludó, sin aportar), se devuelve "" para
// que el llamador no mande un mensaje vacío ni un segundo saludo: el del código ya salió y basta.
func (a *Agent) revisarSaludoDuplicado(from, reply string) string {
	if strings.TrimSpace(reply) == "" || !a.yaSePresentoElCodigo(from) {
		return reply
	}
	limpio := quitarSaludoDuplicado(reply, a.nombresDelCliente(from)...)
	if limpio == reply {
		return reply // el modelo no saludó: perfecto, no hay nada que hacer
	}
	if limpio == "" {
		log.Printf("[saludo-unico] %s: el modelo solo repitió el saludo; se omite su turno", from)
		return ""
	}
	log.Printf("[saludo-unico] %s: se quitó el saludo repetido del modelo (el código ya se presentó)", from)
	return limpio
}
