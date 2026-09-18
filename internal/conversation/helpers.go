package conversation

import "strings"

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
