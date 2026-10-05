package conversation

import (
	"strings"
	"unicode"
)

// NombreDe devuelve el nombre del cliente, o "" si de verdad no lo conocemos.
//
// Mira DOS fuentes, en este orden:
//
//  1. Nombres — el nombre legal, el que dio al registrarse con su cédula.
//  2. PerfilWhatsApp — cómo se llama en SU perfil de WhatsApp.
//
// El segundo se añadió el 18/09 y arregla un caso concreto: en el grupo de Telegram los hilos
// salían como "+593994582191" a secas, aunque WhatsApp nos dice el nombre de la persona DESDE EL
// PRIMER MENSAJE. El dato ya se guardaba (SetPerfilWhatsApp en cmd/bot), pero esta función no lo
// miraba: solo devolvía el nombre legal, que no existe hasta que el cliente da su cédula —o sea,
// casi al final del pedido, cuando el aviso ya se mandó hace rato—.
//
// El orden importa: el nombre legal manda porque es el que el cliente confirmó para su cuenta y
// el que ve el repartidor. El de WhatsApp es el respaldo, y vale muchísimo más que un número.
//
// Vive aquí —el paquete del estado— porque todos los que lo necesitaban (main, notify, agent)
// dependen del Store: antes había tres copias idénticas repartidas.
func NombreDe(store Store, phone string) string {
	if phone == "" {
		return ""
	}
	p, ok := store.GetProfile(phone)
	if !ok {
		return ""
	}
	if nombre := strings.TrimSpace(p.Nombres); nombre != "" {
		return nombre
	}
	return strings.TrimSpace(p.PerfilWhatsApp)
}

// NombreSiSirve devuelve el nombre limpio si parece el nombre de una PERSONA, o "" si no.
//
// El perfil de WhatsApp trae de todo (dueño, 05/10: "están llegando cosas así, @sd2"). De 291
// perfiles guardados, 61 no servían para que el repartidor ubique a nadie: ".", "😀", "Hola 😃",
// "guerraaalberto0999", "castelar_1963@hotmail.com", "+593 98 937 1904", "UBI", "Xxxx"... Con
// esos el bot saludaba "¡Hola, @sd2!" y el backend guardaba eso como nombre del cliente, que es
// lo que ve el repartidor.
//
// La regla es simple a propósito: sin dígitos ni símbolos de usuario/correo, sin palabras que
// no son nombres ("hola", "prueba"...), de 1 a 4 palabras y al menos una de 3 letras. Los
// emojis y adornos se quitan ("Sami 🐣" -> "Sami"). Un nombre de negocio ("Sscommerce") pasa:
// no es perfecto, pero sirve para ubicar la entrega.
func NombreSiSirve(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "0123456789@_+*/\\<>&#:=|$%~") {
		return ""
	}
	var limpio strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), r == '.', r == '\'', r == '-':
			limpio.WriteRune(r)
		case unicode.IsSpace(r):
			limpio.WriteRune(' ')
		}
	}
	var palabras []string
	alguna := false
	for _, w := range strings.Fields(limpio.String()) {
		w = strings.Trim(w, ".'-")
		if w == "" {
			continue
		}
		// La palabra que no es nombre descarta el perfil si va PRIMERO ("Hola 😃", "Prueba
		// Proxy", "No disponible..."); en medio puede ser un apellido raro y no se juzga.
		if len(palabras) == 0 && noEsNombre[strings.ToLower(w)] {
			return ""
		}
		// Tres LETRAS (no runas): "J.L" son iniciales, no un nombre.
		letras := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letras++
			}
		}
		if letras >= 3 {
			alguna = true
		}
		palabras = append(palabras, w)
	}
	if !alguna || len(palabras) > 4 {
		return ""
	}
	return strings.Join(palabras, " ")
}

// noEsNombre son palabras que aparecen en perfiles y nunca son el nombre de una persona.
var noEsNombre = map[string]bool{
	"hola": true, "holi": true, "prueba": true, "test": true, "ubi": true, "ubigas": true,
	"xxx": true, "xxxx": true, "cliente": true, "disponible": true, "whatsapp": true,
	"desconocido": true, "usuario": true, "no": true,
}

// NombreUsable es el nombre con el que el bot puede tratar al cliente y que puede ver el
// repartidor: el que el cliente dio (o el registrado), y si no hay, el de su WhatsApp. Cada uno
// solo si sirve como nombre (NombreSiSirve). "" = hay que preguntárselo.
func NombreUsable(store Store, phone string) string {
	p, ok := store.GetProfile(phone)
	if !ok {
		return ""
	}
	// Con cédula, el nombre es el del REGISTRO (app o panel): es el que ve el repartidor y el
	// backend no lo cambia por el bot, así que se usa tal cual y no se le pregunta.
	if strings.TrimSpace(p.Identificacion) != "" && strings.TrimSpace(p.Nombres) != "" {
		return strings.TrimSpace(p.Nombres)
	}
	if n := NombreSiSirve(p.Nombres); n != "" {
		return n
	}
	return NombreSiSirve(p.PerfilWhatsApp)
}

// Recortar deja el texto en n runas (no bytes, para no partir un emoji o una tilde por la mitad)
// y le añade "…" si se cortó. Compartida por los avisos (Telegram) y la detección de sondeo, que
// antes tenían cada uno su copia.
func Recortar(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
