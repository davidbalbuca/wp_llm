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

// ResolverLinkCortoDeMaps sigue el redirect y saca las coordenadas de la URL final. Se prueba con
// un servidor local que redirige a una URL con ?q=lat,lng, como hace el acortador real.
func TestResolverLinkCortoDeMaps(t *testing.T) {
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer destino.Close()

	corto := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destino.URL+"/maps/place/@-2.898289,-79.003578,17z", http.StatusFound)
	}))
	defer corto.Close()

	lat, lng, ok := ResolverLinkCortoDeMaps(corto.URL)
	if !ok {
		t.Fatal("no se resolvieron coordenadas del redirect")
	}
	if lat < -2.9 || lat > -2.89 || lng < -79.01 || lng > -79.0 {
		t.Errorf("coordenadas fuera de lo esperado: %f, %f", lat, lng)
	}
}

// Un redirect que no lleva coordenadas en ningún lado devuelve ok=false: el llamador pide el pin.
func TestResolverLinkCortoDeMaps_SinCoordenadas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>sin nada util</html>"))
	}))
	defer srv.Close()

	if _, _, ok := ResolverLinkCortoDeMaps(srv.URL); ok {
		t.Error("no debía resolver coordenadas de una página sin ellas")
	}
}
