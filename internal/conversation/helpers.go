package conversation

import "strings"

// NombreDe devuelve el nombre del cliente desde su perfil, o "" si todavía no lo conocemos
// (cliente nuevo). Vive aquí —el paquete del estado— porque todos los que lo necesitaban
// (main, notify, agent) dependen del Store: antes había tres copias idénticas repartidas.
func NombreDe(store Store, phone string) string {
	if phone == "" {
		return ""
	}
	if p, ok := store.GetProfile(phone); ok {
		return strings.TrimSpace(p.Nombres)
	}
	return ""
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
