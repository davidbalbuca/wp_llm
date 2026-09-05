package agent

import (
	"testing"

	"wp-llm-gas/internal/config"
)

// urlSeguimiento arma el enlace público solo cuando hay token Y base configurada. Si falta
// cualquiera, devuelve "" y el bot no ofrece el enlace (nunca manda una URL rota).
func TestUrlSeguimiento(t *testing.T) {
	casos := []struct {
		nombre string
		base   string
		token  string
		want   string
	}{
		{"base y token ok", "https://api.geoware.online", "AbC123", "https://api.geoware.online/seguimiento/AbC123/"},
		{"base con slash final se normaliza", "https://api.geoware.online/", "AbC123", "https://api.geoware.online/seguimiento/AbC123/"},
		{"sin token: vacío", "https://api.geoware.online", "", ""},
		{"sin base (feature apagada): vacío", "", "AbC123", ""},
		{"sin nada: vacío", "", "", ""},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			a := &Agent{cfg: config.Config{SeguimientoBaseURL: c.base}}
			if got := a.urlSeguimiento(c.token); got != c.want {
				t.Errorf("urlSeguimiento(%q) con base %q = %q, want %q", c.token, c.base, got, c.want)
			}
		})
	}
}
