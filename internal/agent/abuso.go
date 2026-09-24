package agent

import (
	"regexp"
	"strings"
)

// CUANDO ALGUIEN NO VIENE A PEDIR GAS.
//
// Dos casos reales:
//
//   - 17/09, varios teléfonos: comandos de herramientas de IA en el chat (`/model
//     claude-opus-5`, `/config`, `/compact`), a ver qué devolvía el bot.
//   - 23/09, Sergy (593968109493): "necesito resolver un algoritmo", "quiero aprender el
//     ordenamiento en burbuja" y, quince minutos después, `-- select * from users`.
//
// El bot contestó bien las tres veces —no se dejó llevar—, pero **nadie se enteró**. Ese es el
// problema: no hay registro, no hay ticket, y si mañana alguien encuentra la frase que sí
// funciona, nos enteramos por el daño.
//
// Lo que se hace ahora: ticket + aviso inmediato a soporte, el chat pasa a un operador, y al
// cliente se le dice que lo va a atender una persona — SIN acusarlo de nada.
//
// EL RIESGO DE ESTE ARCHIVO ES EL FALSO POSITIVO, no el falso negativo. A un atacante que no
// detectamos hoy lo detectamos mañana; a un cliente marcado por error le cortamos el pedido,
// le decimos que espere a un operador y lo dejamos fichado en un ticket. Por eso:
//
//   - se exige que el patrón sea INEQUÍVOCO (SQL con su sintaxis, comandos con su barra,
//     inyección con su verbo Y su objeto), no una palabra suelta;
//   - "olvida/ignora" solo cuentan si apuntan a las INSTRUCCIONES del bot: "olvida mi pedido"
//     es una clienta molesta, no un ataque;
//   - se prueba con tantos mensajes legítimos como sospechosos (ver abuso_test.go).
//
// El aviso de por qué esto importa: al escribir el barrido de la base, un patrón que buscaba
// "DAN" (el jailbreak clásico) marcó como ataque "Para que si no ayuDAN", de una clienta
// enojada. Un patrón de más y el bot le corta el gas a quien ya está molesto.

// patronesDeAbuso son señales inequívocas de que el mensaje no es de un cliente pidiendo gas.
// Cada una es deliberadamente estrecha: mejor que se escape un sondeo a que caiga un cliente.
var patronesDeAbuso = []*regexp.Regexp{
	// SQL con su sintaxis. Se exige la ESTRUCTURA (select…from, drop table), no la palabra:
	// "select" a secas aparece en nombres de locales ("Multiselect"), y "delete" en cualquier lado.
	regexp.MustCompile(`(?i)\bselect\b[\s\S]{0,60}\bfrom\b`),
	regexp.MustCompile(`(?i)\b(drop|truncate|alter)\s+table\b`),
	regexp.MustCompile(`(?i)\bdelete\s+from\b|\binsert\s+into\b|\bunion\s+(all\s+)?select\b`),
	regexp.MustCompile(`(?i)'\s*(or|and)\s+'?1'?\s*=\s*'?1`), // ' OR 1=1

	// Inyección de código / shell.
	regexp.MustCompile(`(?i)<\s*script\b|javascript:\s*\w`),
	regexp.MustCompile(`(?i)\brm\s+-rf\b|\bcurl\s+https?://|\bwget\s+https?://`),
	regexp.MustCompile(`(?i);\s*cat\s+/etc/|\$\(|\bexec\s*\(|\beval\s*\(`),

	// Quiere ver las tripas del bot: su prompt, sus instrucciones, su configuración.
	regexp.MustCompile(`(?i)\b(system\s*prompt|prompt\s+del?\s+sistema)\b`),
	regexp.MustCompile(`(?i)(dime|dame|muestra|muestrame|repite|imprime|cual\s+es)\b[\s\S]{0,40}\b(tu|tus)\b[\s\S]{0,20}\b(prompt|instruccion\w*|reglas|configuracion)\b`),

	// Reasignación de rol: "actúa como", "eres ahora", "pretend to be".
	regexp.MustCompile(`(?i)\bact[uú]a\s+como\b|\bhaz\s+de\s+cuenta\s+que\s+eres\b|\bpretend\s+(to\s+be|you)\b`),
	regexp.MustCompile(`(?i)\beres\s+ahora\b|\bdesde\s+ahora\s+eres\b|\bcomport[aá]te\s+como\b`),
	regexp.MustCompile(`(?i)\b(jailbreak|DAN\s+mode|modo\s+desarrollador|developer\s+mode)\b`),

	// Programación: alguien usando el bot como tutor o probándolo. Se exige que hable de
	// APRENDER/RESOLVER/ESCRIBIR eso, para no marcar al que vive junto a un cyber.
	regexp.MustCompile(`(?i)\b(resolver|aprender|ensename|ens[eé]ñame|escribe|escribeme|hazme|explicame|expl[ií]came)\b[\s\S]{0,40}\b(algoritmo|ordenamiento|burbuja|c[oó]digo|funci[oó]n\s+recursiva|script|programa\s+en)\b`),
	regexp.MustCompile(`(?i)\b(algoritmo|ordenamiento)\s+(de\s+)?burbuja\b`),
}

// ignoraInstrucciones detecta "olvida/ignora lo anterior" SOLO cuando apunta a las
// instrucciones del bot. Va aparte de la lista porque es el patrón que más falsos positivos
// produce: "olvida mi pedido" y "ignora lo que te dije" son cosas que dice un cliente normal,
// y a veces uno molesto —justo a quien peor le caería que le cortemos la conversación—.
var ignoraInstrucciones = regexp.MustCompile(
	`(?i)\b(ignora|olvida|ignore|forget|descarta)\b[\s\S]{0,30}\b(instruccion\w*|reglas?|prompt|system|lo\s+que\s+te\s+(dijeron|programaron)|tu\s+entrenamiento)\b`)

// comandoConArgumento detecta un comando de herramienta al que se le pasa un VALOR
// ("/model claude-opus-5"): eso ya no es teclear por costumbre, es intentar cambiarle algo al bot.
//
// Un comando pelado (`/clear`, `/model`, `/compact`) NO entra aquí. Al revisar la base de
// producción, los cuatro teléfonos que los escribieron resultaron ser DEL EQUIPO —David
// Espinoza, David Balbuca, Ángel, Juan— probando el bot. Tratar eso como ataque abriría un
// ticket y apagaría el bot en cada prueba interna. comandos.go ya los contesta bien; lo único
// que faltaba era avisar, y avisar de una prueba propia es ruido que enseña a ignorar la alarma.
var comandoConArgumento = regexp.MustCompile(`(?i)^\s*/[a-z][a-z0-9_-]{1,20}\s+\S`)

// PareceAbuso dice si el mensaje parece un intento de usar el bot para algo que no es pedir gas:
// inyección de prompt, SQL, código, o sondeo de sus instrucciones.
func PareceAbuso(texto string) bool {
	t := strings.TrimSpace(texto)
	if t == "" {
		return false
	}
	if comandoConArgumento.MatchString(t) {
		return true
	}
	if ignoraInstrucciones.MatchString(t) {
		return true
	}
	for _, p := range patronesDeAbuso {
		if p.MatchString(t) {
			return true
		}
	}
	return false
}

// MensajeAbusoDetectado es lo que se le responde a quien dispara la detección.
//
// NO lo acusa de nada, a propósito: puede ser un cliente real que escribió algo raro, y leer
// "se detectó un intento de ataque" sería ofensivo y además le enseñaría al que sí está
// sondeando qué es lo que detectamos. Se le dice lo único que necesita saber: que a partir de
// ahora lo atiende una persona.
func MensajeAbusoDetectado() string {
	return "Gracias por escribir 🙌 Para ayudarte mejor con esto, te va a atender un operador " +
		"de nuestro equipo en un momento.\n\nSi lo que necesitas es tu gas, me avisas y lo " +
		"dejamos listo 🚚"
}

// MotivoAbuso es el motivo con el que se abre el ticket. Constante: ReportarFallo agrupa por
// motivo, así que varios intentos del mismo teléfono se suman a UN ticket en vez de abrir uno
// por mensaje (alguien probando manda diez seguidos).
const MotivoAbuso = "Posible ataque — uso indebido del bot"
