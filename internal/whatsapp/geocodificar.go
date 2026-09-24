package whatsapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GEOCODIFICAR EL NOMBRE DE UN LUGAR — Y SABER CUÁNDO NO CONFIAR EN EL RESULTADO.
//
// Cuando el link de Maps trae el nombre de un sitio en vez de coordenadas (ver
// lugarconnombre.go), la única forma de sacar un punto es preguntárselo a Google. Pero Google
// SIEMPRE contesta algo, y ahí está el peligro:
//
//	"Gapal, Cuenca, Ecuador"        →  -2.9170,-78.9932  GEOMETRIC_CENTER  ~0.3 km  ✅
//	"Centro De Salud Valle, ..."    →  -2.9388,-78.9636  ROOFTOP           ~0.3 km  ✅
//	"Cuenca, Ecuador"               →  -2.9001,-79.0059  APPROXIMATE       ~8.7 km  ❌
//	"Quito"                         →  -0.2233,-78.5141  APPROXIMATE      ~46.3 km  ❌
//
// Las dos últimas también son "resultados exitosos". Aceptarlas mandaría al repartidor al
// centro geométrico de una ciudad entera, con un pedido que dice tener dirección válida y que
// nadie va a revisar hasta que el repartidor llame perdido.
//
// La regla: solo vale un punto lo bastante PRECISO para entregar en él. Se mide por el tamaño
// del viewport que devuelve Google —la caja que abarca el lugar—, no por el location_type: un
// "APPROXIMATE" de un barrio chico es útil y un "GEOMETRIC_CENTER" de algo enorme no lo es.
// El tamaño es el dato honesto.

// maxKmDelLugar es el lado máximo del área que puede abarcar un lugar para servir como dirección
// de entrega. 2 km cubre un barrio o una urbanización de Cuenca; de ahí para arriba ya es media
// ciudad y el repartidor no tiene a dónde ir.
const maxKmDelLugar = 2.0

// gradoEnKm: un grado de latitud son ~111 km en cualquier parte del planeta.
const gradoEnKm = 111.0

type respuestaGeocoding struct {
	Status  string `json:"status"`
	Results []struct {
		FormattedAddress string `json:"formatted_address"`
		Geometry         struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
			LocationType string `json:"location_type"`
			Viewport     struct {
				Northeast struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"northeast"`
				Southwest struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"southwest"`
			} `json:"viewport"`
		} `json:"geometry"`
	} `json:"results"`
}

// Geocodificador resuelve el nombre de un lugar a coordenadas. Es una interfaz para poder
// probar el flujo completo sin salir a internet.
type Geocodificador interface {
	Coordenadas(lugar string) (lat, lng float64, ok bool)
}

// GeocodificadorGoogle usa la API de Geocoding de Google. apiKey vacía = desactivado (devuelve
// siempre ok=false), que es lo que corresponde en un entorno sin la clave: mejor pedirle el pin
// al cliente que inventar un punto.
type GeocodificadorGoogle struct {
	APIKey string
	// URLBase permite apuntar a un servidor de prueba. Vacía = la API real de Google. Existe
	// porque sin ella la única forma de ejercitar esta función era salir a internet, y entonces
	// la guarda de precisión —lo más delicado del archivo— quedaba sin test.
	URLBase string
	// Sesgo hacia la zona donde se opera: sin esto, "El Valle" puede resolver a un El Valle de
	// otro país. No es una garantía —por eso además se valida el tamaño—, pero desempata.
	CentroLat float64
	CentroLng float64
	http      *http.Client
}

// NuevoGeocodificadorGoogle arma el cliente con un timeout corto: esto corre mientras el cliente
// espera respuesta en WhatsApp.
func NuevoGeocodificadorGoogle(apiKey string, centroLat, centroLng float64) *GeocodificadorGoogle {
	return &GeocodificadorGoogle{
		APIKey:    apiKey,
		CentroLat: centroLat,
		CentroLng: centroLng,
		http:      &http.Client{Timeout: 8 * time.Second},
	}
}

// Coordenadas devuelve el punto del lugar, y ok=false si no se pudo resolver O si el resultado
// es demasiado impreciso para entregar en él.
func (g *GeocodificadorGoogle) Coordenadas(lugar string) (float64, float64, bool) {
	lugar = strings.TrimSpace(lugar)
	if g == nil || g.APIKey == "" || lugar == "" {
		return 0, 0, false
	}
	params := url.Values{}
	params.Set("address", lugar)
	params.Set("key", g.APIKey)
	params.Set("region", "ec")
	if g.CentroLat != 0 || g.CentroLng != 0 {
		// Un cuadro de ~55 km alrededor del centro de operación. Google lo usa para desempatar
		// nombres repetidos, no para descartar lo de afuera.
		params.Set("bounds", coord(g.CentroLat-0.25, g.CentroLng-0.25)+"|"+coord(g.CentroLat+0.25, g.CentroLng+0.25))
	}

	resp, err := g.http.Get(g.base() + "?" + params.Encode())
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	cuerpo, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return 0, 0, false
	}
	var r respuestaGeocoding
	if err := json.Unmarshal(cuerpo, &r); err != nil {
		return 0, 0, false
	}
	if r.Status != "OK" || len(r.Results) == 0 {
		return 0, 0, false
	}
	primero := r.Results[0]
	if !esLoBastantePreciso(primero.Geometry.Viewport.Northeast.Lat,
		primero.Geometry.Viewport.Southwest.Lat,
		primero.Geometry.Viewport.Northeast.Lng,
		primero.Geometry.Viewport.Southwest.Lng) {
		return 0, 0, false
	}
	return primero.Geometry.Location.Lat, primero.Geometry.Location.Lng, true
}

// esLoBastantePreciso mide el viewport del resultado y dice si sirve como dirección de entrega.
//
// Se toma el lado MAYOR de la caja: un lugar alargado (una avenida entera) es tan inservible
// como uno cuadrado y grande. Un viewport vacío (todo en cero) se rechaza: sin la caja no hay
// forma de saber qué tan impreciso es el punto, y ante la duda se le pide el pin al cliente.
func esLoBastantePreciso(neLat, swLat, neLng, swLng float64) bool {
	altoKm := (neLat - swLat) * gradoEnKm
	anchoKm := (neLng - swLng) * gradoEnKm
	if altoKm <= 0 || anchoKm <= 0 {
		return false
	}
	lado := altoKm
	if anchoKm > lado {
		lado = anchoKm
	}
	return lado <= maxKmDelLugar
}

func (g *GeocodificadorGoogle) base() string {
	if g.URLBase != "" {
		return g.URLBase
	}
	return "https://maps.googleapis.com/maps/api/geocode/json"
}

func coord(lat, lng float64) string {
	return strconv.FormatFloat(lat, 'f', 6, 64) + "," + strconv.FormatFloat(lng, 'f', 6, 64)
}
