package whatsapp

import (
	"math"
	"os"
	"strings"
	"testing"
)

// EL LINK DE GOOGLE MAPS QUE YA NO TRAE COORDENADAS.
//
// El 21/09 a las 09:06 Carlos (593986140905) mandó su ubicación como enlace de Google Maps. El
// bot respondió "no pude abrir tu enlace" y le pidió el pin nativo. Carlos lo mandó, pero ese
// rechazo aparece en cinco conversaciones más del fin de semana y varias murieron ahí.
//
// El código SÍ resuelve links cortos (ResolverLinkCortoDeMaps sigue el redirect). Lo que cambió
// es lo que hay al final del redirect: para un lugar sin dirección exacta —un conjunto, una casa
// sin nomenclatura— Google ya no pone lat,lng en la URL. Pone un PLUS CODE:
//
//	https://www.google.com/maps/place/52C7%2B6W8+Conjunto+NANCYTA+minidepas,+Unnamed+Road,+Cuenca/...
//
// "52C7+6W8" es un Open Location Code: un estándar abierto y público que se convierte a
// coordenadas con aritmética, sin API key y sin pedirle nada a Google.
//
// EL RIESGO, que es lo que obliga a hacerlo con cuidado: ese código es CORTO. Le faltan los 4
// caracteres del prefijo, que dicen en qué cuadrante del planeta está. Cada prefijo cubre un
// grado de lado —unos 111 km—, y Cuenca centro (6792) y la casa de Carlos (6793) caen en
// prefijos DISTINTOS aunque estén a 8 km. Adivinar mal el prefijo manda el gas a 111 km.
//
// Por eso se usa recoverNearest, que es lo que manda el estándar: se prueban los cuadrantes
// vecinos y se elige el que queda más cerca de la referencia. Los candidatos equivocados quedan
// a 111 km, así que la elección nunca es ajustada.
//
// Y encima hay red: la geocerca del backend valida después la ubicación. Si esto se equivocara,
// el pedido se rechaza por cobertura, no sale un camión a ciegas.

const (
	// El pin REAL que Carlos mandó después, y que es la respuesta correcta.
	carlosLat = -2.829392
	carlosLng = -78.985058
	// Cuenca centro: la referencia desde la que se recupera el prefijo.
	cuencaLat = -2.9001
	cuencaLng = -79.0059
)

// metrosEntre da la distancia aproximada en metros. Suficiente para estos tests: lo que se
// comprueba es "cayó en la casa" contra "cayó en otra provincia".
func metrosEntre(lat1, lng1, lat2, lng2 float64) float64 {
	const gradoEnMetros = 111_320.0
	dLat := (lat1 - lat2) * gradoEnMetros
	dLng := (lng1 - lng2) * gradoEnMetros * math.Cos(lat1*math.Pi/180)
	return math.Sqrt(dLat*dLat + dLng*dLng)
}

// EL CASO CARLOS: el código corto de su link tiene que caer en su casa.
func TestElPlusCodeDeCarlosCaeEnSuCasa(t *testing.T) {
	lat, lng, ok := PlusCodeACoordenadas("52C7+6W8", cuencaLat, cuencaLng)
	if !ok {
		t.Fatal("no se pudo convertir el Plus Code del link que mandó Carlos")
	}
	if d := metrosEntre(lat, lng, carlosLat, carlosLng); d > 50 {
		t.Errorf("el Plus Code cayó a %.0f m del pin real (%.6f,%.6f vs %.6f,%.6f)",
			d, lat, lng, carlosLat, carlosLng)
	}
}

// Y desde la URL completa, que es como llega de verdad.
func TestSeSacaElPlusCodeDeLaURLDeGoogle(t *testing.T) {
	const url = "https://www.google.com/maps/place/52C7%2B6W8+Conjunto+NANCYTA+minidepas," +
		"+Unnamed+Road,+Cuenca/data=!4m2!3m1!1s0x91cd1708a3ae7251:0xf1e1623e0bf57b9a!18m1!1e1"

	lat, lng, ok := CoordenadasDePlusCodeEnURL(url, cuencaLat, cuencaLng)
	if !ok {
		t.Fatal("no se reconoció el Plus Code dentro de la URL de Google")
	}
	if d := metrosEntre(lat, lng, carlosLat, carlosLng); d > 50 {
		t.Errorf("la URL resolvió a %.0f m del pin real", d)
	}
}

// LA PRUEBA QUE IMPORTA DE VERDAD: que la recuperación del prefijo no se vaya a otro cuadrante.
// Un error aquí no es un error de metros, es de cien kilómetros.
func TestElPrefijoRecuperadoNoSeVaAOtraProvincia(t *testing.T) {
	// Puntos reales donde el negocio opera o podría operar, con su código corto.
	casos := []struct {
		nombre   string
		lat, lng float64
	}{
		{"casa de Carlos (Cuenca norte)", carlosLat, carlosLng},
		{"Challuabamba", -2.856928, -78.912484},
		{"Cuenca centro", cuencaLat, cuencaLng},
		{"Baños (Cuenca)", -2.9333, -79.0667},
		{"Ricaurte", -2.8631, -78.9806},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			// Se genera el código corto de ese punto, como haría Google...
			corto, ok := CoordenadasAPlusCodeCorto(c.lat, c.lng)
			if !ok {
				t.Fatalf("no se pudo generar el código corto de %s", c.nombre)
			}
			// ...y se recupera usando Cuenca centro como referencia.
			lat, lng, ok := PlusCodeACoordenadas(corto, cuencaLat, cuencaLng)
			if !ok {
				t.Fatalf("no se pudo recuperar %q", corto)
			}
			if d := metrosEntre(lat, lng, c.lat, c.lng); d > 50 {
				t.Errorf("%s: %q se recuperó a %.0f m del punto original — si el error pasa de "+
					"unos metros, el prefijo es de otro cuadrante y el gas va a otra provincia",
					c.nombre, corto, d)
			}
		})
	}
}

// Basura dentro no puede salir como coordenada: un falso positivo aquí manda un camión a un
// punto inventado. Ante la duda, que el bot pida el pin.
func TestLoQueNoEsUnPlusCodeSeRechaza(t *testing.T) {
	basura := []string{
		"", "hola", "52C7", "++++", "0000+0000",
		"52C7+6W8extra",       // sufijo pegado
		"ZZZZ+ZZ",             // letras fuera del alfabeto del estándar
		"1234+56",             // el alfabeto no tiene 0 ni 1
		"https://ubi.ec/algo", // una URL cualquiera
	}
	for _, texto := range basura {
		t.Run(texto, func(t *testing.T) {
			if _, _, ok := PlusCodeACoordenadas(texto, cuencaLat, cuencaLng); ok {
				t.Errorf("%q se aceptó como Plus Code", texto)
			}
		})
	}
}

// Una URL de Google SIN Plus Code tampoco puede inventar nada: ahí el bot tiene que seguir
// pidiendo el pin, que es lo que hacía antes.
func TestUnaURLSinPlusCodeNoDevuelveNada(t *testing.T) {
	urls := []string{
		"https://www.google.com/maps/place/Parque+Calderon,+Cuenca/data=!4m2",
		"https://www.google.com/maps/search/gas+cuenca",
		"https://ubi.ec/privacidad.html",
	}
	for _, url := range urls {
		if _, _, ok := CoordenadasDePlusCodeEnURL(url, cuencaLat, cuencaLng); ok {
			t.Errorf("se sacó una coordenada de una URL sin Plus Code: %q", url)
		}
	}
}

// EL CABLEADO: que la conversión exista no sirve de nada si el resolvedor de links no la llama.
//
// Es el mutante que casi se escapa: quitar la llamada de ResolverLinkCortoDeMaps deja todos los
// tests de arriba en verde, porque prueban la función suelta. Solo lo cazaba una prueba contra
// la red real, y eso no puede vivir en CI.
func TestElPlusCodeEstaCableadoEnElResolvedorDeLinks(t *testing.T) {
	src, err := os.ReadFile("whatsapp.go")
	if err != nil {
		t.Fatalf("no se pudo leer whatsapp.go: %v", err)
	}
	if !strings.Contains(string(src), "CoordenadasDePlusCodeEnURL(") {
		t.Error("ResolverLinkCortoDeMaps no intenta el Plus Code: los links de Google sin " +
			"coordenadas vuelven a fallar con \"no pude abrir tu enlace\"")
	}
}

// Y que el centro configurado llegue hasta aquí: con la referencia en 0,0 (el Golfo de Guinea) el
// prefijo recuperado sería el de otro continente.
func TestElCentroConfiguradoLlegaAlResolvedor(t *testing.T) {
	src, err := os.ReadFile("../../cmd/bot/main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	if !strings.Contains(string(src), "ResolverLinkCortoDeMaps(inc.Text, cfg.BotCentroLat, cfg.BotCentroLng)") {
		t.Error("el webhook no le pasa el centro configurado al resolvedor de links")
	}
}

// El código LARGO (con los 8 caracteres del prefijo) no necesita referencia y tiene que
// funcionar igual: es el que sale cuando alguien comparte el Plus Code completo.
func TestElPlusCodeCompletoNoNecesitaReferencia(t *testing.T) {
	// El código completo de la casa de Carlos.
	lat, lng, ok := PlusCodeACoordenadas("679352C7+6X", 0, 0)
	if !ok {
		t.Fatal("no se pudo convertir un Plus Code completo")
	}
	if d := metrosEntre(lat, lng, carlosLat, carlosLng); d > 50 {
		t.Errorf("el código completo cayó a %.0f m del pin real", d)
	}
}
