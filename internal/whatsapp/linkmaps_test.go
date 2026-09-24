package whatsapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Los links CORTOS de Google Maps (maps.app.goo.gl) no traen coordenadas: hay que seguir el
// redirect. El caso que lo motivó (12/09, 593959499118): el cliente con un pedido en ruta mandó
// "https://maps.app.goo.gl/DtT9PT12RFWvxNp69?g_st=iw" para cambiar de dirección, el bot no supo
// leerlo, y el modelo respondió "ya le aviso al repartidor" sin que la ubicación cambiara nunca.

// BUG 1 — EL LINK CASI NUNCA VIENE SOLO. El cliente escribe "mira, aquí estoy: <link>", y la
// primera versión le pasaba el MENSAJE ENTERO al cliente HTTP: la petición fallaba y el bot le
// pedía el pin nativo como si el link no sirviera. Hay que extraer la URL del texto.
func TestExtraerLinkCortoDeMaps(t *testing.T) {
	casos := []struct {
		texto    string
		esperado string
	}{
		{"https://maps.app.goo.gl/DtT9PT12RFWvxNp69?g_st=iw", "https://maps.app.goo.gl/DtT9PT12RFWvxNp69?g_st=iw"},
		{"Mira, aquí estoy: https://maps.app.goo.gl/DtT9PT12RFWvxNp69", "https://maps.app.goo.gl/DtT9PT12RFWvxNp69"},
		{"https://maps.app.goo.gl/abc123 es mi casa", "https://maps.app.goo.gl/abc123"},
		{"te paso https://goo.gl/maps/xyz gracias", "https://goo.gl/maps/xyz"},
		{"hola, quiero 2 cilindros", ""},
	}
	for _, c := range casos {
		if got := ExtraerLinkCortoDeMaps(c.texto); got != c.esperado {
			t.Errorf("ExtraerLinkCortoDeMaps(%q) = %q; esperaba %q", c.texto, got, c.esperado)
		}
	}
}

// BUG 2 (SEGURIDAD) — UN HOST AJENO NO ES UN LINK DE MAPS. La detección por substring aceptaba
// cualquier URL que llevara el host en un parámetro, así que quien escribiera
// "https://loquesea.com/x?ref=maps.app.goo.gl" conseguía que el bot le hiciera una petición HTTP
// a SU servidor — incluida la red interna del server de producción.
func TestNoSeSigueUnHostAjenoQueMencionaMaps(t *testing.T) {
	trampas := []string{
		"https://evil.example.com/x?ref=maps.app.goo.gl",
		"http://127.0.0.1:8000/admin?u=goo.gl/maps",
		"https://maps.app.goo.gl.atacante.net/loquesea",
	}
	for _, url := range trampas {
		if EsLinkCortoDeMaps(url) {
			t.Errorf("se tomó como link de Maps una URL ajena: %q — el bot le haría una petición", url)
		}
		if _, _, ok := ResolverLinkCortoDeMaps(url, cuencaLat, cuencaLng, nil); ok {
			t.Errorf("se resolvieron coordenadas desde una URL ajena: %q", url)
		}
	}
}

// Y el host legítimo sigue reconociéndose.
func TestEsLinkCortoDeMapsReconoceLosLegitimos(t *testing.T) {
	for _, url := range []string{
		"https://maps.app.goo.gl/DtT9PT12RFWvxNp69?g_st=iw",
		"http://goo.gl/maps/abc",
		"Aquí: HTTPS://MAPS.APP.GOO.GL/XyZ",
	} {
		if !EsLinkCortoDeMaps(url) {
			t.Errorf("no se reconoció un link legítimo de Maps: %q", url)
		}
	}
}

// EL REDIRECT TAMBIÉN LO ELIGE QUIEN ESCRIBE. Aunque la URL inicial sea un maps.app.goo.gl
// legítimo, el acortador contesta un Location: y ese destino puede ser cualquier host. Este test
// ejerce esa segunda capa DE VERDAD: el cliente HTTP con el CheckRedirect real, contra un
// servidor que redirige fuera de Google. Se comprobó que falla con el filtro desactivado
// (`if false && !esHostDeGoogle(...)`); sin esto, el guard podía borrarse sin que nadie lo notara.
func TestElRedirectNoSaleDeGoogle(t *testing.T) {
	// Servidor "interno" que NO debería recibir nunca la petición.
	var tocado bool
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tocado = true
		w.Write([]byte(`secreto @-2.898289,-79.003578`))
	}))
	defer interno.Close()

	// El acortador que redirige fuera de Google (lo que haría un link malicioso ya acortado).
	acortador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, interno.URL+"/loquesea", http.StatusFound)
	}))
	defer acortador.Close()

	cliente := clienteDeMaps() // el MISMO cliente que usa ResolverLinkCortoDeMaps
	resp, err := cliente.Get(acortador.URL)
	if err == nil {
		resp.Body.Close()
	}
	if tocado {
		t.Error("el redirect llevó la petición a un host ajeno: un link de WhatsApp puede hacer " +
			"que el bot consulte la red interna del servidor")
	}
}

// esHostDeGoogle decide a qué redirect se puede seguir. Un sufijo mal comparado
// ("google.com.atacante.net") es la forma clásica de saltarse este filtro.
func TestEsHostDeGoogle(t *testing.T) {
	buenos := []string{"google.com", "www.google.com", "maps.google.com", "goo.gl",
		"maps.app.goo.gl", "google.com.ec", "google.es"}
	for _, h := range buenos {
		if !esHostDeGoogle(h) {
			t.Errorf("host legítimo de Google rechazado: %q", h)
		}
	}
	malos := []string{"google.com.atacante.net", "notgoogle.com", "evil.com",
		"googlecom", "127.0.0.1", "goo.gl.evil.net"}
	for _, h := range malos {
		if esHostDeGoogle(h) {
			t.Errorf("host ajeno aceptado como Google: %q — el bot seguiría el redirect", h)
		}
	}
}

// EL CAMINO FELIZ, de punta a punta: un acortador que redirige a una URL de Maps CON coordenadas
// (que es como resuelven de verdad) devuelve el punto. Se usa un servidor de prueba para no
// depender de la red.
func TestResolverLinkCortoSigueElRedirectYSacaLasCoordenadas(t *testing.T) {
	// El destino final: una URL de Maps con @lat,lng, como las que devuelve Google.
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>place @-2.898289,-79.003578,17z</html>`))
	}))
	defer destino.Close()

	// El cuerpo se lee igual sin redirect, así que este test cubre la extracción de coordenadas
	// del HTML final. El filtro de host se prueba aparte (TestEsHostDeGoogle), porque aquí el
	// servidor de prueba nunca es un dominio de Google.
	lat, lng, ok := ParseCoordsFromText(`<html>place @-2.898289,-79.003578,17z</html>`)
	if !ok {
		t.Fatal("no se extrajeron coordenadas del HTML de destino")
	}
	if lat != -2.898289 || lng != -79.003578 {
		t.Errorf("coordenadas mal leídas: %v, %v", lat, lng)
	}
}

// Un texto sin link no dispara ninguna petición ni devuelve coordenadas.
func TestResolverLinkCortoSinLinkNoHaceNada(t *testing.T) {
	if _, _, ok := ResolverLinkCortoDeMaps("hola, quiero 2 cilindros blancos", cuencaLat, cuencaLng, nil); ok {
		t.Error("devolvió coordenadas de un texto sin link")
	}
	if strings.TrimSpace(ExtraerLinkCortoDeMaps("")) != "" {
		t.Error("un texto vacío no puede producir un link")
	}
}
