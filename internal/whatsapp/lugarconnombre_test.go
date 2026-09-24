package whatsapp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// EL TERCER FORMATO DE LINK DE GOOGLE MAPS.
//
// Cuando alguien comparte su ubicación desde Maps, la URL final puede venir de tres maneras:
//
//  1. con COORDENADAS          → .../@-2.89,-79.00  o  ?q=-2.89,-79.00     (ParseCoordsFromText)
//  2. con PLUS CODE            → ?q=8FQ5RC9R%2B7X                          (pluscode.go, 21/09)
//  3. con el NOMBRE DEL LUGAR  → ?q=Gapal,+Cuenca,+Ecuador&ftid=0x91cd...  ← esto
//
// El tercero aparece cuando el cliente no manda un pin sino que elige un LUGAR del buscador de
// Maps —"Gapal", "Centro de Salud Valle"—. Google resuelve el link a la ficha de ese sitio y la
// URL no lleva ni coordenadas ni Plus Code: solo el nombre y un `ftid` (identificador interno).
//
// Casos reales en producción:
//
//	22-sep 23:01  593961868344  .../8ctjuVrvkDhwZD4p7  → ?q=Gapal,+Cuenca,+Ecuador
//	08-sep 22:14  593995041865  .../2p4CXgCpFYSGDL8EA  → ?q=Centro+De+Salud+Valle,...
//
// El HTML de esa página tampoco trae coordenadas (Maps las carga por JS), así que seguir
// leyendo el cuerpo no sirve: hay que quedarse con el NOMBRE y geocodificarlo.

func TestNombreDelLugarEnLaURL(t *testing.T) {
	casos := []struct{ url, quiere string }{
		// Los dos casos reales, con la URL final tal como la devuelve Google.
		{"https://www.google.com/maps?q=Gapal,+Cuenca,+Ecuador&ftid=0x91cd185c374063ab:0xf7ccd93d694c4013&entry=gps",
			"Gapal, Cuenca, Ecuador"},
		{"https://www.google.com/maps?q=Centro+De+Salud+Valle,+Subcentro+de+Salud+de+El+Valle,+Cuenca&ftid=0x91cd1919c30c54af:0x9bfef86085751e05",
			"Centro De Salud Valle, Subcentro de Salud de El Valle, Cuenca"},
		// Con %20 en vez de "+".
		{"https://www.google.com/maps?q=Parque%20Calderon,%20Cuenca", "Parque Calderon, Cuenca"},
	}
	for _, c := range casos {
		got, ok := NombreDeLugarEnURL(c.url)
		if !ok {
			t.Errorf("no se extrajo el nombre de %q", c.url)
			continue
		}
		if got != c.quiere {
			t.Errorf("nombre distinto:\n  url:      %s\n  esperado: %q\n  salió:    %q", c.url, c.quiere, got)
		}
	}
}

// Y lo que NO es un nombre de lugar. Devolver "−2.89" o un Plus Code como si fuera el nombre de
// un sitio haría que el geocodificador busque un lugar llamado así y devuelva cualquier cosa:
// una coordenada equivocada manda al repartidor a otro lado, que es peor que no resolver.
func TestLoQueNoEsNombreDeLugar(t *testing.T) {
	noSon := []string{
		// Coordenadas: las resuelve ParseCoordsFromText, no esto.
		"https://www.google.com/maps?q=-2.898289,-79.003578",
		"https://www.google.com/maps/@-2.8982,-79.0035,17z",
		// Plus Code: lo resuelve pluscode.go.
		"https://www.google.com/maps?q=8FQ5RC9R%2B7X",
		"https://www.google.com/maps?q=RC9R%2B7X+Cuenca",
		// Sin ?q= no hay nada que sacar.
		"https://www.google.com/maps/place/data=!4m2!3m1!1s0x91cd",
		"https://www.google.com/maps",
	}
	for _, u := range noSon {
		if nombre, ok := NombreDeLugarEnURL(u); ok {
			t.Errorf("se tomó por nombre de lugar algo que no lo es:\n  url: %s\n  salió: %q", u, nombre)
		}
	}
}

// LA GUARDA DE PRECISIÓN. Google SIEMPRE contesta algo: preguntarle por "Cuenca" devuelve el
// centro de la ciudad con cara de resultado válido. Aceptarlo mandaría al repartidor al Parque
// Calderón con un pedido que dice tener dirección correcta, y nadie se enteraría hasta que
// llame perdido.
//
// Los números salen de consultas REALES a la API con la key de producción (23-sep-2026).
func TestSoloValeUnLugarLoBastantePreciso(t *testing.T) {
	// Un cuadro centrado en Cuenca, del alto/ancho que se indique en km.
	caja := func(km float64) (neLat, swLat, neLng, swLng float64) {
		d := km / 2 / 111.0
		return -2.9001 + d, -2.9001 - d, -79.0059 + d, -79.0059 - d
	}
	sirven := []struct {
		nombre string
		km     float64
	}{
		{"Gapal (GEOMETRIC_CENTER, real)", 0.3},
		{"Centro de Salud Valle (ROOFTOP, real)", 0.3},
		{"El Valle (APPROXIMATE pero chico, real)", 0.3},
		{"una urbanización grande", 1.8},
	}
	for _, c := range sirven {
		if !esLoBastantePreciso(caja(c.km)) {
			t.Errorf("se rechazó un lugar que SÍ sirve para entregar: %s (~%.1f km)", c.nombre, c.km)
		}
	}

	noSirven := []struct {
		nombre string
		km     float64
	}{
		{"Cuenca, Ecuador (real)", 8.7},
		{"Quito (real)", 46.3},
		{"una parroquia entera", 5.0},
		{"justo por encima del límite", 2.5},
	}
	for _, c := range noSirven {
		if esLoBastantePreciso(caja(c.km)) {
			t.Errorf("se aceptó como dirección de entrega algo de media ciudad: %s (~%.1f km)", c.nombre, c.km)
		}
	}

	// Sin viewport no hay forma de saber qué tan impreciso es el punto. Ante la duda, se le pide
	// el pin al cliente: es un mensaje de más, no un repartidor perdido.
	if esLoBastantePreciso(0, 0, 0, 0) {
		t.Error("un resultado sin viewport se aceptó: no hay manera de saber si sirve")
	}
}

// Y sin API key el geocodificador no adivina: devuelve que no pudo, y el flujo le pide el pin
// al cliente como siempre. Un entorno mal configurado no puede inventar direcciones.
func TestSinClaveNoInventaCoordenadas(t *testing.T) {
	g := NuevoGeocodificadorGoogle("", -2.9001, -79.0059)
	if _, _, ok := g.Coordenadas("Gapal, Cuenca"); ok {
		t.Error("sin API key devolvió coordenadas: estaría inventando una dirección de entrega")
	}
	// Y un nombre vacío tampoco sale a internet.
	g2 := NuevoGeocodificadorGoogle("una-clave", -2.9001, -79.0059)
	if _, _, ok := g2.Coordenadas("   "); ok {
		t.Error("con el nombre vacío devolvió coordenadas")
	}
}

// geoFalso responde sin salir a internet, y ANOTA qué se le preguntó: así el test puede
// comprobar que el nombre llegó bien hasta el geocodificador, no solo que hubo respuesta.
type geoFalso struct {
	lat, lng  float64
	ok        bool
	preguntas []string
}

func (g *geoFalso) Coordenadas(lugar string) (float64, float64, bool) {
	g.preguntas = append(g.preguntas, lugar)
	return g.lat, g.lng, g.ok
}

// EL CAMINO COMPLETO: URL final con nombre de lugar → geocodificación → coordenadas.
//
// Se entra por resolverURLFinal y no por ResolverLinkCortoDeMaps porque ese solo acepta
// dominios de Google (bien: no sale a cualquier URL que le manden en un mensaje), y un
// servidor de prueba vive en 127.0.0.1. Lo que importa verificar es lo de después del
// redirect, que es donde estaba el hueco.
//
// Sin este test las piezas pasaban por separado y nadie comprobaba que el resolvedor las
// usara: borrar el bloque entero del tercer formato no rompía ningún test.
func TestElLinkConNombreDeLugarSeResuelveDePuntaAPunta(t *testing.T) {
	// Imita la página a la que redirige maps.app.goo.gl con el link de Gapal (22/09): sin
	// coordenadas en la URL ni en el cuerpo, solo el nombre en el ?q=.
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>pagina de maps sin coordenadas</html>"))
	}))
	defer final.Close()
	urlFinal := final.URL + "/maps?q=Gapal,+Cuenca,+Ecuador&ftid=0x91cd185c374063ab"

	geo := &geoFalso{lat: -2.91702, lng: -78.99322, ok: true}
	lat, lng, ok := resolverURLFinal(urlFinal, "", cuencaLat, cuencaLng, geo)
	if !ok {
		t.Fatal("no se resolvió la URL con nombre de lugar: el cliente recibiría 'no pude abrir tu enlace'")
	}
	if lat != -2.91702 || lng != -78.99322 {
		t.Errorf("coordenadas distintas: %.5f, %.5f", lat, lng)
	}
	if len(geo.preguntas) != 1 || geo.preguntas[0] != "Gapal, Cuenca, Ecuador" {
		t.Errorf("al geocodificador le llegó otra cosa: %q", geo.preguntas)
	}
}

// Si el geocodificador no puede (sin clave, sin resultado, o resultado impreciso), el
// resolvedor NO inventa: devuelve que no pudo y el flujo le pide el pin al cliente.
func TestSiElGeocodificadorNoPuedeNoSeInventaUnPunto(t *testing.T) {
	urlFinal := "https://www.google.com/maps?q=Un+Lugar+Cualquiera"

	geo := &geoFalso{ok: false}
	if _, _, ok := resolverURLFinal(urlFinal, "", cuencaLat, cuencaLng, geo); ok {
		t.Error("se dio por resuelto sin que el geocodificador pudiera: sería un punto inventado")
	}
	// Y sin geocodificador (nil) tampoco: es el caso de un entorno sin la clave.
	if _, _, ok := resolverURLFinal(urlFinal, "", cuencaLat, cuencaLng, nil); ok {
		t.Error("sin geocodificador se devolvió una coordenada")
	}
}

// Un nombre con solo símbolos no se le manda al geocodificador: Google devolvería cualquier
// cosa o gastaría una consulta para nada.
func TestNoSeGeocodificaLoQueNoParezcaUnNombre(t *testing.T) {
	geo := &geoFalso{lat: -2.9, lng: -79.0, ok: true}
	resolverURLFinal("https://www.google.com/maps?q=%2B%2B%2B+123", "", cuencaLat, cuencaLng, geo)
	if len(geo.preguntas) > 0 {
		t.Errorf("se geocodificó algo que no es un nombre de lugar: %q", geo.preguntas)
	}
}

// googleFalso responde como la API de Geocoding, con el viewport que se le pida. Los datos
// salen de consultas REALES hechas el 23-sep-2026 con la clave de producción.
func googleFalso(t *testing.T, status string, lat, lng, ladoKm float64) *httptest.Server {
	t.Helper()
	d := ladoKm / 2 / 111.0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != "OK" {
			fmt.Fprintf(w, `{"status":%q,"results":[]}`, status)
			return
		}
		fmt.Fprintf(w, `{"status":"OK","results":[{"formatted_address":"X","geometry":{
			"location":{"lat":%f,"lng":%f},"location_type":"APPROXIMATE",
			"viewport":{"northeast":{"lat":%f,"lng":%f},"southwest":{"lat":%f,"lng":%f}}}}]}`,
			lat, lng, lat+d, lng+d, lat-d, lng-d)
	}))
}

// La guarda de precisión, ejercitada por la función COMPLETA y no solo por el ayudante que
// mide el viewport. Sin esto, quitar la llamada a esLoBastantePreciso dentro de Coordenadas no
// rompía ningún test: el ayudante seguía probado, pero nadie lo usaba.
func TestCoordenadasRechazaLoImpreciso(t *testing.T) {
	// "Gapal": 0.3 km. Sirve.
	srv := googleFalso(t, "OK", -2.91702, -78.99322, 0.3)
	defer srv.Close()
	g := &GeocodificadorGoogle{APIKey: "x", URLBase: srv.URL, http: srv.Client()}
	lat, _, ok := g.Coordenadas("Gapal, Cuenca")
	if !ok || lat != -2.91702 {
		t.Errorf("se rechazó un lugar preciso (0.3 km): ok=%v lat=%v", ok, lat)
	}

	// "Cuenca, Ecuador": 8.7 km. Es el centro de la ciudad entera.
	grande := googleFalso(t, "OK", -2.9001, -79.0059, 8.7)
	defer grande.Close()
	g2 := &GeocodificadorGoogle{APIKey: "x", URLBase: grande.URL, http: grande.Client()}
	if _, _, ok := g2.Coordenadas("Cuenca, Ecuador"); ok {
		t.Error("se aceptó el centro de Cuenca (8.7 km) como dirección de entrega: el repartidor " +
			"saldría al Parque Calderón con un pedido que dice tener dirección correcta")
	}

	// Sin resultados: no se inventa nada.
	vacio := googleFalso(t, "ZERO_RESULTS", 0, 0, 0)
	defer vacio.Close()
	g3 := &GeocodificadorGoogle{APIKey: "x", URLBase: vacio.URL, http: vacio.Client()}
	if _, _, ok := g3.Coordenadas("asdkjhasd xyz"); ok {
		t.Error("sin resultados devolvió coordenadas")
	}
}

// Un lugar ALARGADO —una avenida entera— es tan inservible como uno grande y cuadrado: el
// punto medio de la Av. de las Américas no es la casa de nadie. Se mide el lado MAYOR.
func TestUnLugarAlargadoTampocoSirve(t *testing.T) {
	d := 6.0 / 2 / 111.0 // 6 km de ancho...
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ...y solo 200 m de alto: una avenida.
		fmt.Fprintf(w, `{"status":"OK","results":[{"geometry":{
			"location":{"lat":-2.9,"lng":-79.0},
			"viewport":{"northeast":{"lat":%f,"lng":%f},"southwest":{"lat":%f,"lng":%f}}}}]}`,
			-2.9+0.0009, -79.0+d, -2.9-0.0009, -79.0-d)
	}))
	defer srv.Close()
	g := &GeocodificadorGoogle{APIKey: "x", URLBase: srv.URL, http: srv.Client()}
	if _, _, ok := g.Coordenadas("Avenida de las Américas"); ok {
		t.Error("se aceptó una avenida de 6 km de largo como dirección de entrega")
	}
}

// Y sin clave NO se sale a internet: ni siquiera se hace la petición. Un entorno mal
// configurado no puede gastar consultas ni, peor, devolver un punto.
func TestSinClaveNiSiquieraConsulta(t *testing.T) {
	consultas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		consultas++
		fmt.Fprint(w, `{"status":"OK","results":[{"geometry":{"location":{"lat":-2.9,"lng":-79.0},
			"viewport":{"northeast":{"lat":-2.899,"lng":-78.999},"southwest":{"lat":-2.901,"lng":-79.001}}}}]}`)
	}))
	defer srv.Close()
	g := &GeocodificadorGoogle{APIKey: "", URLBase: srv.URL, http: srv.Client()}
	if _, _, ok := g.Coordenadas("Gapal"); ok {
		t.Error("sin clave devolvió coordenadas")
	}
	if consultas != 0 {
		t.Errorf("sin clave salió a consultar igual (%d veces)", consultas)
	}
}
