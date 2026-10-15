// OTRA CIUDAD NO ES UN BARRIO (06/10).
//
// El candado de cobertura (cobertura.go) no deja que el modelo rechace a un cliente por el nombre
// de un lugar: un barrio que no conoce puede estar dentro de Cuenca (el caso La Gloria, 11/09).
// Pero esa regla trataba igual a OTRA CIUDAD. El 06/10 un cliente preguntó "Loja?", el modelo
// contestó bien que no llegamos, y el candado lo cambió por "¡Claro que sí! Atendemos en
// CUENCA…". Un minuto después mandó su ubicación desde Loja y recibió el rechazo: una promesa y
// su desmentida, lo mismo que pasó el 15/09 con Paute, Sígsig, Gualaceo y Santa Isabel.
//
// Aquí está la lista de ciudades y cantones del país donde NO operamos. Si el cliente nombra uno,
// el "no llegamos" es verdad y se respeta; y si el modelo dijera que sí, se corrige. Para un
// barrio o una referencia ("por el ex CREA") todo sigue igual: se pide la ubicación.
package agent

import (
	"strings"
	"unicode"

	"wp-llm-gas/internal/georoutes"
)

// ciudadesFuera: ciudades y cantones conocidos donde no operamos (normalizados -> como se
// nombran). Solo nombres inequívocos: nada que también sea una parroquia o un barrio de Cuenca
// (Baños, Sucre, El Valle, Turi, Tarqui…). Si un día se abre una de estas zonas, la cobertura
// del backend manda: ver lugarFueraMencionado.
var ciudadesFuera = map[string]string{
	"quito": "Quito", "guayaquil": "Guayaquil", "loja": "Loja", "machala": "Machala",
	"ambato": "Ambato", "riobamba": "Riobamba", "azogues": "Azogues", "biblian": "Biblián",
	"canar": "Cañar", "la troncal": "La Troncal", "el tambo": "El Tambo", "deleg": "Déleg",
	"paute": "Paute", "gualaceo": "Gualaceo", "sigsig": "Sígsig", "giron": "Girón",
	"santa isabel": "Santa Isabel", "nabon": "Nabón", "chordeleg": "Chordeleg",
	"sevilla de oro": "Sevilla de Oro", "guachapala": "Guachapala",
	"ponce enriquez": "Ponce Enríquez", "macas": "Macas", "sucua": "Sucúa",
	"zamora": "Zamora", "yantzaza": "Yantzaza", "gualaquiza": "Gualaquiza",
	"saraguro": "Saraguro", "catamayo": "Catamayo", "pasaje": "Pasaje",
	"santa rosa": "Santa Rosa", "huaquillas": "Huaquillas", "manta": "Manta",
	"portoviejo": "Portoviejo", "esmeraldas": "Esmeraldas", "ibarra": "Ibarra",
	"otavalo": "Otavalo", "tulcan": "Tulcán", "latacunga": "Latacunga",
	"santo domingo": "Santo Domingo", "quevedo": "Quevedo", "babahoyo": "Babahoyo",
	"milagro": "Milagro", "duran": "Durán", "salinas": "Salinas", "tena": "Tena",
	"puyo": "Puyo", "guaranda": "Guaranda", "naranjal": "Naranjal",
}

// palabrasNormalizadas: minúsculas, sin tildes y solo letras, separadas por un espacio.
func palabrasNormalizadas(s string) string {
	s = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n").
		Replace(strings.ToLower(s))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }), " ")
}

// lugarFueraMencionado devuelve la ciudad fuera de zona que nombra el texto, o "". Si esa ciudad
// aparece en la cobertura que informa el backend (porque se abrió), no cuenta.
func lugarFueraMencionado(texto string, zonas []georoutes.ZonaCobertura) string {
	t := " " + palabrasNormalizadas(texto) + " "
	cubiertas := " "
	for _, z := range zonas {
		cubiertas += palabrasNormalizadas(z.Zona) + " "
		for _, p := range z.Parroquias {
			cubiertas += palabrasNormalizadas(p) + " "
		}
	}
	for clave, nombre := range ciudadesFuera {
		if strings.Contains(t, " "+clave+" ") && !strings.Contains(cubiertas, " "+clave+" ") {
			return nombre
		}
	}
	return ""
}

// lugarFueraEnElMensaje mira el ÚLTIMO mensaje del cliente (el webhook ya lo dejó en el
// historial antes de llamar al agente).
func (a *Agent) lugarFueraEnElMensaje(from string) string {
	var ultimo string
	for _, m := range a.store.GetConversation(from, 6) {
		if m.Role == "user" {
			ultimo = m.Content
		}
	}
	if ultimo == "" {
		return ""
	}
	var zonas []georoutes.ZonaCobertura
	if a.catalog != nil {
		if contexto, ok := a.catalog.Get(); ok && contexto != nil {
			zonas = contexto.Zonas
		}
	}
	return lugarFueraMencionado(ultimo, zonas)
}

// mensajeCiudadFuera: el "no" honesto y amable, con dónde sí atendemos.
func mensajeCiudadFuera(ciudad string, zonas []georoutes.ZonaCobertura) string {
	donde := "Cuenca y sus parroquias"
	if texto := ZonasEnTexto(zonas); texto != "" {
		donde = "las parroquias urbanas y rurales de " + texto
	}
	return "Por ahora no llegamos a " + ciudad + " 😔 Atendemos en " + donde + ". Si algún día " +
		"estás por aquí, con gusto te llevamos tu gas 😊"
}
