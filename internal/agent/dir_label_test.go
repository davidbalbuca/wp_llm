package agent

import (
	"testing"

	"wp-llm-gas/internal/georoutes"
)

func TestFriendlyDirName(t *testing.T) {
	casos := []struct {
		nombre string
		in     georoutes.SavedDirection
		want   string
	}{
		{"nombrada quita prefijo", georoutes.SavedDirection{Alias: "WhatsApp - Casa"}, "Casa"},
		{"nombrada sin espacio", georoutes.SavedDirection{Alias: "WhatsApp-Trabajo"}, "Trabajo"},
		{"nombre propio tal cual", georoutes.SavedDirection{Alias: "Depa"}, "Depa"},
		{"generica sin ref ni dir util", georoutes.SavedDirection{
			Alias: "WhatsApp", Direccion: "Ubicación compartida por WhatsApp (-2.9, -79.0)"}, ""},
		{"generica cae a referencia", georoutes.SavedDirection{
			Alias: "WhatsApp", Referencia: "Casa azul"}, "Casa azul"},
		{"generica cae a direccion real", georoutes.SavedDirection{
			Alias: "WhatsApp", Direccion: "Av. Solano 123"}, "Av. Solano 123"},
	}
	for _, c := range casos {
		if got := friendlyDirName(c.in); got != c.want {
			t.Errorf("%s: friendlyDirName=%q, want %q", c.nombre, got, c.want)
		}
	}
}
