// Candado de CAMBIO DE COLOR: el modelo no puede prometer un intercambio que el negocio no hace.
//
// El caso (593980787206, 24/09): el cliente tenía dos cilindros AZULES y preguntó dos veces si se
// los cambiaban por BLANCOS. El bot respondió "¡Claro que sí! Podemos cambiar esos dos azules por
// dos blancos sin problema 😊" y, al insistir, "¡Sí, claro! 😊 No hay problema, pides el color que
// tú quieras sin importar cuál tengas ahora". Las dos veces se lo inventó: en la tabla del backend
// solo están configurados BLANCO<->AMARILLO y NARANJA<->AZUL, así que azul->blanco NO es un cambio
// que el negocio haga. Un operador tuvo que entrar al chat a explicárselo y llamar a los
// repartidores a mano para negociarlo.
//
// Y salió bien por SUERTE: BLANCO tenía 465 unidades en stock. Con un color escaso, el cliente
// habría dado su cédula y su ubicación para que el pedido se cayera al final.
//
// Es el mismo hueco que la cobertura: un hecho del negocio que vive en una tabla y que el modelo
// no tiene forma de conocer, así que rellena con lo que suena amable. La solución es la de
// siempre: el código lo verifica contra el backend.
//
// LO QUE NO HACE, a propósito: no toca el pedido ni cambia el color de nada. El cliente que pide
// "2 blancos" tiene un pedido de 2 blancos, con equivalencia o sin ella — el intercambio de los
// envases vacíos es un trato aparte que hacen los repartidores. Este candado solo corrige lo que
// el bot AFIRMA sobre ese trato.
package agent

import (
	"fmt"
	"log"
	"strings"
)

// El candado se dispara por lo que el bot PROMETE, no por lo que el cliente pregunta: la promesa
// es el daño, y así también cubre al modelo que ofrece el cambio sin que nadie lo haya pedido. El
// mensaje del cliente se usa solo para leer el PAR de colores, porque es el que trae los dos
// ("tengo azules, quiero blancos"); la respuesta del modelo suele traer uno solo.

// prometeElCambio detecta que la respuesta del bot le está diciendo al cliente que sí se le
// cambian los cilindros.
//
// Las secuencias son estrechas a propósito: tienen que hablar del CAMBIO, no de un "sí" ni de un
// color cualquiera. "Sí, tenemos blancos" es disponibilidad, no intercambio, y taparlo rompería
// la conversación normal. afirmaSecuencia ya descarta las negaciones, así que "no hacemos ese
// cambio" no cae aquí.
func prometeElCambio(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"podemos", "cambiar"}, {"puedo", "cambiar"}, {"si", "cambiamos"},
		{"si", "los", "cambiamos"}, {"te", "los", "cambiamos"}, {"te", "cambiamos"},
		{"se", "los", "cambiamos"}, {"hacemos", "el", "cambio"},
		{"si", "hay", "como", "cambiar"}, {"sin", "importar", "cual", "tengas"},
		{"pides", "el", "color", "que", "tu", "quieras"},
		{"no", "importa", "el", "color", "que", "tengas"},
	}, 4)
}

// coloresMencionados devuelve los colores del catálogo que aparecen en el texto, en el orden en
// que aparecen. Es lo que permite leer "tengo azules y quiero blancos" como el par (AZUL, BLANCO).
//
// Se buscan los nombres del CATÁLOGO y no una lista quemada: si mañana entra VERDE, funciona sin
// tocar esto. Se compara sobre el texto normalizado (sin tildes, minúsculas) y por prefijo, para
// que "blancos"/"blanco"/"Blancas" cuenten como BLANCO.
func (a *Agent) coloresMencionados(texto string) []string {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return nil
	}
	norm := normalizar(texto)
	// Raíz del nombre para tolerar plural y género: BLANCO -> "blanc". Sin esto "blancos" no
	// casaría con "blanco" y el candado no vería el color que el cliente pidió.
	type marca struct {
		pos    int
		nombre string
	}
	var marcas []marca
	vistos := map[string]bool{}
	for _, prod := range contexto.Products {
		for _, c := range prod.Colores {
			nombre := strings.TrimSpace(c.Nombre)
			if nombre == "" || vistos[strings.ToUpper(nombre)] {
				continue
			}
			raiz := raizDeColor(normalizar(nombre))
			if raiz == "" {
				continue
			}
			if pos := strings.Index(norm, raiz); pos >= 0 {
				vistos[strings.ToUpper(nombre)] = true
				marcas = append(marcas, marca{pos: pos, nombre: strings.ToUpper(nombre)})
			}
		}
	}
	// Orden de aparición: el primero es el que el cliente TIENE, el segundo el que quiere.
	for i := 1; i < len(marcas); i++ {
		for j := i; j > 0 && marcas[j].pos < marcas[j-1].pos; j-- {
			marcas[j], marcas[j-1] = marcas[j-1], marcas[j]
		}
	}
	out := make([]string, 0, len(marcas))
	for _, m := range marcas {
		out = append(out, m.nombre)
	}
	return out
}

// raizDeColor recorta la última vocal de género para que el nombre case en plural y femenino
// ("blanco" -> "blanc", y así "blancos"/"blancas" también entran). Los nombres que no acaban en
// vocal de género se dejan tal cual ("azul").
func raizDeColor(nombre string) string {
	if len(nombre) < 4 {
		return nombre
	}
	if u := nombre[len(nombre)-1]; u == 'o' || u == 'a' {
		return nombre[:len(nombre)-1]
	}
	return nombre
}

// cambioConfigurado dice si el negocio acepta cambiar entre esos dos colores, según la tabla del
// backend. El segundo valor es false cuando no hay datos para responder (sin catálogo): en ese
// caso NO se afirma nada.
func (a *Agent) cambioConfigurado(tiene, quiere string) (bool, bool) {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil || contexto.Equivalencias.PorColor == nil {
		return false, false
	}
	for _, equiv := range contexto.Equivalencias.PorColor[strings.ToUpper(tiene)] {
		if strings.EqualFold(equiv, quiere) {
			return true, true
		}
	}
	return false, true
}

// revisarCambioDeColorPrometido es el CANDADO: si el cliente preguntó por cambiar de color y el
// bot se lo prometió, se comprueba contra el backend. Si el cambio no está configurado, se
// reemplaza el texto por la verdad y se deriva a un operador.
//
// Devuelve la respuesta que debe salir. El caso legítimo (equivalencia configurada) pasa intacto:
// el bot puede y debe decir que sí cuando es verdad.
func (a *Agent) revisarCambioDeColorPrometido(t *turno, from, mensajeCliente, reply string) string {
	if !prometeElCambio(reply) {
		return reply
	}
	// El par de colores se lee del mensaje del CLIENTE (es el que trae los dos) y, si ahí solo
	// hay uno, se completa con la respuesta del bot.
	colores := a.coloresMencionados(mensajeCliente)
	if len(colores) < 2 {
		colores = append(colores, a.coloresMencionados(reply)...)
	}
	if len(colores) < 2 {
		// Sin dos colores no se puede saber qué cambio se prometió. No se toca el texto: tapar
		// una frase que no se entendió sería peor que dejarla pasar.
		return reply
	}
	tiene, quiere := colores[0], colores[1]
	if strings.EqualFold(tiene, quiere) {
		return reply
	}

	permitido, hayDatos := a.cambioConfigurado(tiene, quiere)
	if permitido {
		return reply // es verdad: el negocio hace ese cambio
	}
	if !hayDatos {
		log.Printf("[cambio-color] %s: el bot prometió %s->%s y no hay equivalencias cargadas; "+
			"se deriva a un operador", from, tiene, quiere)
	} else {
		log.Printf("[cambio-color] %s: el bot prometió %s->%s, que NO está configurado; "+
			"se reemplaza y se deriva a un operador", from, tiene, quiere)
	}

	// Se deriva de verdad, para que "un operador lo revisa" no sea otra promesa vacía. Por
	// runTool, como el resto de los candados: es quien crea el ticket.
	a.runTool(t, from, "escalar_al_dueno", map[string]any{
		"motivo": fmt.Sprintf("Cambio de color no configurado: %s por %s", tiene, quiere),
		"resumen": fmt.Sprintf("El cliente quiere cambiar sus cilindros %s por %s. Ese cambio NO está "+
			"en las equivalencias del panel, así que el bot no lo prometió. Hay que confirmar con los "+
			"repartidores si alguno lo acepta y contestarle.", tiene, quiere),
	})
	return mensajeCambioNoDisponible(tiene, quiere, a.equivalenciasDe(tiene))
}

// equivalenciasDe devuelve los colores por los que SÍ se puede cambiar el que el cliente tiene.
// Sirve para ofrecerle la alternativa real en vez de dejarlo sin salida.
func (a *Agent) equivalenciasDe(color string) []string {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return nil
	}
	return contexto.Equivalencias.PorColor[strings.ToUpper(color)]
}

// mensajeCambioNoDisponible redacta la respuesta honesta. Dice que ese cambio no se hace, ofrece
// los que sí (si hay) y avisa de que un operador lo va a revisar — que es lo que de verdad pasa,
// porque el candado acaba de crear el ticket.
func mensajeCambioNoDisponible(tiene, quiere string, alternativas []string) string {
	t, q := strings.ToLower(tiene), strings.ToLower(quiere)
	msg := fmt.Sprintf("Te cuento con sinceridad 🙏 el cambio de cilindros %s por %s no es uno de "+
		"los que hacemos normalmente.", t, q)
	if len(alternativas) > 0 {
		vistos := map[string]bool{}
		var nombres []string
		for _, alt := range alternativas {
			bajo := strings.ToLower(alt)
			if bajo == "" || vistos[bajo] {
				continue
			}
			vistos[bajo] = true
			nombres = append(nombres, bajo)
		}
		if len(nombres) > 0 {
			msg += fmt.Sprintf(" Tus %s sí se pueden cambiar por %s.", t, unirConY(nombres))
		}
	}
	msg += " De todos modos ya le pasé tu caso a un operador para que lo revise con los " +
		"repartidores y te confirme 😊"
	return msg
}

// unirConY arma "amarillo y naranja" (o "amarillo, naranja y verde") para que la frase se lea
// como la escribiría una persona.
func unirConY(nombres []string) string {
	switch len(nombres) {
	case 0:
		return ""
	case 1:
		return nombres[0]
	default:
		return strings.Join(nombres[:len(nombres)-1], ", ") + " y " + nombres[len(nombres)-1]
	}
}
