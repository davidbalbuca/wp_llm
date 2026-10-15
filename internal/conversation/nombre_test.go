package conversation

import "testing"

// Perfiles REALES de WhatsApp de los clientes (05/10).
func TestNombreSiSirve(t *testing.T) {
	sirven := map[string]string{
		"Adrián Zambrano": "Adrián Zambrano", "Sami 🐣": "Sami", "Adry Urgiles.🌻": "Adry Urgiles",
		"Dra. Joy 💫👩🏻‍⚕️🔥": "Dra Joy", "Erik🐾": "Erik", "Psi.Clín Michelle Reinoso": "Psi.Clín Michelle Reinoso",
		"Davicho": "Davicho", "José alvear": "José alvear",
	}
	for perfil, want := range sirven {
		if got := NombreSiSirve(perfil); got != want {
			t.Errorf("%q: %q, se esperaba %q", perfil, got, want)
		}
	}
	noSirven := []string{"@sd2", ".", "😀", "✨", "Hola 😃", "Hola 👋🏻", "guerraaalberto0999", "MARU_VARIEDADES20",
		"castelar_1963@hotmail.com", "+593 98 937 1904", "UBI", "Xxxx", "*******", "Prueba Proxy", "Hg", "J.L🪽",
		"No disponible el número e", "Edwin<3", "~Mafer >:)", "Petizos Juegos / Sandra P", "", "🙏🙏🙏"}
	for _, perfil := range noSirven {
		if got := NombreSiSirve(perfil); got != "" {
			t.Errorf("%q no sirve como nombre, pero devolvió %q", perfil, got)
		}
	}
}

func TestNombreUsablePrefiereElQueDioElCliente(t *testing.T) {
	s := NewMemStore()
	s.SetProfile("1", Profile{PerfilWhatsApp: "@sd2"})
	if got := NombreUsable(s, "1"); got != "" {
		t.Errorf("con un perfil que no sirve, hay que preguntar: %q", got)
	}
	s.SetProfile("1", Profile{PerfilWhatsApp: "@sd2", Nombres: "Juan Pérez"})
	if got := NombreUsable(s, "1"); got != "Juan Pérez" {
		t.Errorf("NombreUsable = %q", got)
	}
}
