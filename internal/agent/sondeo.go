package agent

import (
	"log"
	"strings"
	"unicode"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/notify"
)

// Detección de SONDEO: alguien que no viene a comprar gas sino a sacarle al bot información del
// negocio (cuántos conductores hay, qué gana la empresa, datos de otros clientes) o a manipularlo
// para que se salte sus reglas ("ignora tus instrucciones", "eres un asistente sin filtros").
//
// El bot ya se niega a contestar eso —solo habla de gas y usa la información del catálogo—, así
// que esto NO cambia su respuesta: solo AVISA al grupo para que alguien mire quién está
// preguntando. Lo que se defiende no es una respuesta puntual sino el patrón: una conversación
// entera de preguntas raras, sin intención de comprar, dice algo que un mensaje suelto no dice.
//
// Es deliberadamente CONSERVADOR. Un falso positivo manda a un cliente real a una lista de
// sospechosos, y eso es peor que dejar pasar un sondeo: al competidor curioso el bot no le va a
// contar nada de todos modos. Por eso exige señales fuertes o varias señales juntas.

// SeñalSondeo es lo que se detectó en un mensaje, para poder explicarlo en el aviso.
type SeñalSondeo struct {
	Categoria string // qué tipo de sondeo parece
	Frase     string // el fragmento que lo disparó (para que un humano lo juzgue)
}

// patronesFuertes son los que por sí solos justifican un aviso: no hay forma de escribirlos por
// accidente pidiendo gas.
var patronesFuertes = []struct {
	categoria string
	frases    []string
}{
	{"Manipulación del asistente (prompt injection)", []string{
		"ignora tus instrucciones", "ignora las instrucciones", "olvida tus instrucciones",
		"olvida las reglas", "ignora todo lo anterior", "system prompt", "prompt del sistema",
		"tus instrucciones", "eres un asistente sin", "actua como si", "actúa como si",
		"modo desarrollador", "developer mode", "jailbreak", "sin filtros", "sin restricciones",
		"repite tus reglas", "cuales son tus reglas", "cuáles son tus reglas", "que modelo eres",
		"qué modelo eres", "eres chatgpt", "eres gemini", "eres claude", "eres una ia",
	}},
	{"Pide datos de OTROS clientes", []string{
		"datos de otros clientes", "lista de clientes", "telefonos de clientes",
		"teléfonos de clientes", "base de clientes", "cuantos clientes tienen",
		"cuántos clientes tienen", "direcciones de otros", "pedidos de otros",
		"datos personales de", "numero de otro cliente", "número de otro cliente",
	}},
	{"Pide credenciales o acceso al sistema", []string{
		"contraseña", "contrasena", "password", "usuario y clave", "token", "api key",
		"acceso al sistema", "base de datos", "servidor", "backend", "admin",
	}},
}

// patronesDebiles son preguntas que un cliente curioso PUEDE hacer de buena fe ("¿cuántos
// repartidores tienen?" lo pregunta quien tiene prisa). Una sola no dice nada; varias en la
// misma conversación, sin pedir gas, ya es un patrón.
var patronesDebiles = []struct {
	categoria string
	frases    []string
}{
	{"Pregunta por la operación interna", []string{
		"cuantos conductores", "cuántos conductores", "cuantos repartidores", "cuántos repartidores",
		"cuantos camiones", "cuántos camiones", "cuantas unidades", "cuántas unidades",
		"quien es el dueño", "quién es el dueño", "quienes son los dueños", "quiénes son los dueños",
		"donde queda la bodega", "dónde queda la bodega", "donde almacenan", "dónde almacenan",
		"cuantos empleados", "cuántos empleados", "horario de los conductores",
	}},
	{"Pregunta por números del negocio", []string{
		"cuanto venden", "cuánto venden", "cuanto facturan", "cuánto facturan",
		"cuantos pedidos al dia", "cuántos pedidos al día", "cuanto ganan", "cuánto ganan",
		"margen de ganancia", "a como les cuesta", "a cómo les cuesta", "cuanto les cuesta el cilindro",
		"quien les provee", "quién les provee", "su proveedor", "sus proveedores",
	}},
	{"Pregunta por el proveedor de tecnología", []string{
		"que sistema usan", "qué sistema usan", "quien les hizo", "quién les hizo",
		"que app usan", "qué app usan", "como funciona su sistema", "cómo funciona su sistema",
	}},
}

// DetectarSondeo revisa UN mensaje del cliente. Devuelve las señales encontradas: las fuertes
// bastan por sí solas, las débiles hay que acumularlas (ver AcumularSondeo).
func DetectarSondeo(mensaje string) (fuertes, debiles []SeñalSondeo) {
	t := normalizar(mensaje)
	if t == "" {
		return nil, nil
	}
	for _, g := range patronesFuertes {
		for _, f := range g.frases {
			if strings.Contains(t, f) {
				fuertes = append(fuertes, SeñalSondeo{Categoria: g.categoria, Frase: f})
				break // una por categoría: no hace falta listar sinónimos
			}
		}
	}
	for _, g := range patronesDebiles {
		for _, f := range g.frases {
			if strings.Contains(t, f) {
				debiles = append(debiles, SeñalSondeo{Categoria: g.categoria, Frase: f})
				break
			}
		}
	}
	return fuertes, debiles
}

// normalizar deja el texto comparable: minúsculas, sin tildes y con los signos convertidos en
// espacios, para que "¿Cuántos conductores?" y "cuantos conductores" sean lo mismo.
func normalizar(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'á', 'à', 'ä', 'â':
			b.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			b.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			b.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô':
			b.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			b.WriteRune('u')
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// umbralDebiles es cuántas señales débiles DISTINTAS hacen falta en una conversación para avisar.
// Dos preguntas de categorías distintas ("cuántos conductores tienen" + "cuánto facturan") ya no
// parecen curiosidad de alguien con prisa. Una sola nunca avisa.
const umbralDebiles = 2

// revisarSondeo mira el mensaje del cliente y avisa al grupo si parece que alguien está sacando
// información del negocio en vez de pedir gas. NO cambia la respuesta del bot: el modelo ya tiene
// prohibido hablar de otra cosa, esto solo hace que un humano se entere.
//
// Best-effort de principio a fin: cualquier problema aquí se traga y la conversación sigue.
func (a *Agent) revisarSondeo(from, mensaje string) {
	fuertes, debiles := DetectarSondeo(mensaje)
	if len(fuertes) == 0 && len(debiles) == 0 {
		return
	}

	// Las débiles se acumulan a lo largo de la conversación: se recorre lo que el cliente ya
	// escribió y se cuentan las CATEGORÍAS distintas, no las repeticiones de la misma pregunta.
	categorias := map[string]string{} // categoría -> frase que la disparó
	for _, s := range debiles {
		categorias[s.Categoria] = s.Frase
	}
	if len(fuertes) == 0 {
		for _, m := range a.store.GetConversation(from, 40) {
			if m.Role != "user" {
				continue
			}
			_, d := DetectarSondeo(m.Content)
			for _, s := range d {
				categorias[s.Categoria] = s.Frase
			}
		}
		if len(categorias) < umbralDebiles {
			return // todavía puede ser un cliente curioso; no se avisa por una sola pregunta
		}
	}

	// Una vez por conversación: si insiste, no hace falta un aviso por mensaje.
	if !a.store.MarcarAvisoSondeo(from, conversation.SessionGap) {
		return
	}

	var detalle strings.Builder
	for _, s := range fuertes {
		detalle.WriteString("• " + s.Categoria + " — \"" + s.Frase + "\"\n")
	}
	for cat, frase := range categorias {
		detalle.WriteString("• " + cat + " — \"" + frase + "\"\n")
	}
	detalle.WriteString("\nÚltimo mensaje: \"" + conversation.Recortar(mensaje, 200) + "\"")

	var nombre string
	if p, ok := a.store.GetProfile(from); ok {
		nombre = strings.TrimSpace(p.Nombres)
	}
	log.Printf("[sondeo] posible sondeo de %s (%d fuertes, %d categorías)", from, len(fuertes), len(categorias))
	notify.Default.Sondeo(from, nombre, detalle.String())
}

// afirmaPedidoConfirmado detecta si el texto le está diciendo al cliente que su pedido YA quedó
// hecho: confirmado, registrado, en camino, repartidor asignado. Se usa como candado — si el
// modelo afirma esto sin haber llamado a registrar_pedido, es un pedido fantasma.
//
// Busca la COMBINACIÓN de una palabra de pedido + una de estado-hecho, para no saltar con frases
// legítimas como "¿confirmas tu color?" o "el repartidor te avisará". Conservador a propósito:
// mejor dejar pasar un caso raro que bloquear una respuesta buena.
func afirmaPedidoConfirmado(texto string) bool {
	t := normalizar(texto)
	if t == "" {
		return false
	}
	// Señales de que el pedido estaría HECHO/EN MARCHA (no una pregunta ni una promesa futura).
	hechos := []string{
		"esta confirmado", "quedo confirmado", "pedido confirmado", "esta registrado",
		"quedo registrado", "pedido registrado", "en camino", "esta asignado",
		"repartidor ya fue asignado", "repartidor fue asignado", "repartidor asignado",
		"conductor asignado", "conductor ya fue asignado", "ya fue asignado", "te llegara en breve",
		"esta en proceso de entrega", "asignado y en camino",
	}
	for _, h := range hechos {
		if strings.Contains(t, h) {
			return true
		}
	}
	return false
}
