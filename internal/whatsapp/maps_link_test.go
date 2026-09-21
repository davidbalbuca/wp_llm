package whatsapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// El detector de links cortos de Maps (caso 593959499118: el cliente mandó maps.app.goo.gl y el
// bot lo ignoró porque no trae coordenadas en la URL).
func TestEsLinkCortoDeMaps(t *testing.T) {
	siLo := []string{
		"https://maps.app.goo.gl/DtT9PT12RFWvxNp69?g_st=iw",
		"maps.app.goo.gl/abc",
		"mira: https://goo.gl/maps/xyz por favor",
	}
	for _, s := range siLo {
		if !EsLinkCortoDeMaps(s) {
			t.Errorf("no se reconoció como link corto de Maps: %q", s)
		}
	}
	noLo := []string{
		"hola quiero gas",
		"https://maps.google.com/?q=-2.9,-79.0", // link largo: ParseCoordsFromText ya lo saca
		"-2.898289, -79.003578",                 // par de coordenadas suelto
	}
	for _, s := range noLo {
		if EsLinkCortoDeMaps(s) {
			t.Errorf("se tomó por link corto algo que no lo es: %q", s)
		}
	}
}

// SOLO SE RESUELVEN LINKS DE MAPS. Este test pedía antes un servidor local (127.0.0.1) y
// esperaba coordenadas: eso era justo el agujero. La URL la elige quien escribe por WhatsApp, así
// que si se acepta cualquier host, un mensaje puede hacer que el bot le pida una página a la red
// interna del server de producción. Ahora una URL que no es de Maps no se toca.
func TestResolverLinkCortoDeMaps_SoloHostsDeMaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/maps/place/@-2.898289,-79.003578,17z", http.StatusFound)
	}))
	defer srv.Close()

	if _, _, ok := ResolverLinkCortoDeMaps(srv.URL, cuencaLat, cuencaLng); ok {
		t.Errorf("se resolvió un host que no es de Maps (%s): el bot haría peticiones a donde le digan", srv.URL)
	}
}

// Un link CON forma de Maps que no resuelve a nada devuelve ok=false: el llamador pide el pin
// nativo en vez de dar por buena una ubicación que no existe.
func TestResolverLinkCortoDeMaps_SinCoordenadas(t *testing.T) {
	// Dominio de Maps inexistente: la petición falla y no hay coordenadas que sacar.
	if _, _, ok := ResolverLinkCortoDeMaps("https://maps.app.goo.gl/noexiste-"+t.Name(), cuencaLat, cuencaLng); ok {
		t.Error("no debía resolver coordenadas de un link que no lleva a ninguna parte")
	}
}
