package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/georoutes"
)

// LAS 33 PARROQUIAS REALES de producción (getCoverageZones, 24-sep-2026), en el orden en que las
// manda el backend: alfabético. Ese orden es la causa del problema que se arregla aquí.
var parroquiasReales = []string{
	"BANOS", "BELLAVISTA", "CAÑARIBAMBA", "CHECA", "CHIQUINTAD", "CUMBE", "EL BATAN",
	"EL SAGRARIO", "EL VALE", "EL VECINO", "GIL RAMIREZ D.", "HERMANO MIGUEL", "HUAYNA CAPAC",
	"LLACAO", "MACHANGARA", "MONAY", "NULTI", "OCTAVIO  PALACIOS", "PACCHA", "QUINGEO",
	"RICAURTE", "SAN BLAS", "SAN JOAQUIN", "SAN SEBASTIAN", "SANTA ANA", "SAYAUSI", "SIDCAY",
	"SININCAY", "SUCRE", "TARQUI", "TOTORACOCHA", "TURI", "YANUNCAY",
}

// EL PROBLEMA: cortando las 2 primeras de una lista alfabética, el cliente SIEMPRE leía
// "CUENCA (BANOS, BELLAVISTA y más)". Quien vive en Sinincay, Turi o Hermano Miguel —parroquias
// cubiertas -- no se veía nombrado y se iba pensando que no llegamos.
func TestElTextoDeZonasNoSeQuedaSiempreEnLasDosPrimeras(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: parroquiasReales}}
	texto := ZonasEnTexto(zonas)

	if !strings.Contains(texto, "CUENCA") {
		t.Fatalf("no nombra la zona: %s", texto)
	}
	// Tiene que llegar más allá del arranque del alfabeto.
	var deLaSegundaMitad int
	for _, p := range parroquiasReales[len(parroquiasReales)/2:] {
		if strings.Contains(texto, p) {
			deLaSegundaMitad++
		}
	}
	if deLaSegundaMitad == 0 {
		t.Errorf("solo salen parroquias del principio del alfabeto:\n%s", texto)
	}
	// Y avisar de que hay más, porque no se listan las 33.
	if !strings.Contains(texto, "y más") {
		t.Errorf("no dice que hay más parroquias de las nombradas:\n%s", texto)
	}
}

// El mensaje al cliente tiene que decir URBANAS Y RURALES: la geocerca cubre el cantón completo, y
// sin esa palabra el de una parroquia rural asume que solo se atiende el centro.
func TestElMensajeDiceUrbanasYRurales(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: parroquiasReales}}
	msg := mensajeCoberturaEnPositivo(zonas)
	bajo := strings.ToLower(msg)
	if !strings.Contains(bajo, "urbanas y rurales") {
		t.Errorf("el mensaje no distingue urbanas y rurales:\n%s", msg)
	}
	// Y sigue pidiendo la ubicación, que es lo único que decide.
	if !strings.Contains(bajo, "ubicación") {
		t.Errorf("el mensaje dejó de pedir la ubicación:\n%s", msg)
	}
}

// La muestra se reparte por TODA la lista y es DETERMINISTA: el mismo listado da siempre los
// mismos ejemplos, así que el mensaje no baila entre turnos.
func TestLaMuestraDeParroquiasSeRepiteYSeReparte(t *testing.T) {
	primera := parroquiasDeMuestra(parroquiasReales, 6)
	segunda := parroquiasDeMuestra(parroquiasReales, 6)
	if len(primera) != 6 {
		t.Fatalf("se pidieron 6 ejemplos y salieron %d: %v", len(primera), primera)
	}
	if strings.Join(primera, "|") != strings.Join(segunda, "|") {
		t.Errorf("la muestra cambia entre llamadas: %v vs %v", primera, segunda)
	}
	// Repartida: el último ejemplo debe venir de la segunda mitad de la lista.
	ultimo := primera[len(primera)-1]
	var pos int
	for i, p := range parroquiasReales {
		if p == ultimo {
			pos = i
		}
	}
	if pos < len(parroquiasReales)/2 {
		t.Errorf("la muestra se concentra al principio (último ejemplo %q en la posición %d de %d): %v",
			ultimo, pos, len(parroquiasReales), primera)
	}
	// Sin duplicados: repetir un nombre en la misma frase se lee como un error.
	vistos := map[string]bool{}
	for _, p := range primera {
		if vistos[p] {
			t.Errorf("ejemplo duplicado %q en %v", p, primera)
		}
		vistos[p] = true
	}
}

// Con menos parroquias que ejemplos pedidos se listan todas y NO se dice "y más" (sería mentira).
func TestConPocasParroquiasNoSePrometeQueHayMas(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{Zona: "ZAMORA", Parroquias: []string{"ZAMORA"}}}
	texto := ZonasEnTexto(zonas)
	if !strings.Contains(texto, "ZAMORA") {
		t.Fatalf("no nombra la parroquia: %s", texto)
	}
	if strings.Contains(texto, "y más") {
		t.Errorf("con una sola parroquia dice que hay más: %s", texto)
	}
}

// Sin zonas no se inventa nada: el mensaje sale sin nombrar ninguna.
func TestSinZonasNoSeInventaNinguna(t *testing.T) {
	if got := ZonasEnTexto(nil); got != "" {
		t.Errorf("sin zonas devolvió %q", got)
	}
	if got := parroquiasDeMuestra(nil, 6); got != nil {
		t.Errorf("sin parroquias devolvió %v", got)
	}
	// Nombres vacíos del backend no deben colarse como ", , ".
	sucias := parroquiasDeMuestra([]string{"BANOS", "", "  ", "TURI"}, 6)
	for _, p := range sucias {
		if strings.TrimSpace(p) == "" {
			t.Errorf("se colo un nombre vacio en %v", sucias)
		}
	}
}

// EL MODELO ve MENOS ejemplos que el cliente, a propósito. Una lista larga en el prompt la lee
// como catálogo cerrado y deduce "no está => no hay cobertura" (caso La Gloria, 11/09). Son dos
// lectores distintos y sus topes suben por separado; este test fija esa relación.
func TestElPromptVeMenosEjemplosQueElCliente(t *testing.T) {
	if ejemplosParaElModelo >= ejemplosPorZona {
		t.Fatalf("el prompt ve %d ejemplos y el cliente %d: al modelo hay que darle MENOS, "+
			"si no vuelve a usar la lista para descartar barrios (caso La Gloria)",
			ejemplosParaElModelo, ejemplosPorZona)
	}
}

// Y el bloque del prompt tiene que decirle CUÁNTAS hay y que son urbanas y rurales: es lo que
// evita que trate los tres ejemplos como la lista completa.
func TestElPromptDiceCuantasParroquiasHay(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{Zona: "CUENCA", Parroquias: parroquiasReales}}
	texto := renderCobertura(zonas)
	if !strings.Contains(texto, "33") {
		t.Errorf("el prompt no dice cuántas parroquias hay:\n%s", texto)
	}
	if !strings.Contains(strings.ToLower(texto), "urbanas y rurales") {
		t.Errorf("el prompt no dice que son urbanas y rurales:\n%s", texto)
	}
}
