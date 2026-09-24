// EL MENÚ NO DEPENDE DE QUE EL MODELO SE ACUERDE.
//
// Pedido del dueño (18/09): "por más que haya botones, el modelo de IA no siempre lo manda;
// debemos asegurar que siempre lo haga y no dejar esto opcional a la IA, sino que sea parte de la
// programación".
//
// Tiene razón, y es el mismo problema de fondo que ya explotó cuatro veces en este proyecto: el
// prompt PIDE algo (llamar a `mostrar_menu`, llamar a `registrar_pedido`, llamar a
// `cancelar_pedido`) y el modelo a veces no lo hace. Cuando eso pasa con un menú, el cliente
// recibe una pregunta en texto y le toca escribir — justo lo que se quería evitar.
//
// La solución es la de siempre aquí: no se le pide al modelo que se acuerde, se COMPRUEBA el
// resultado. Este candado mira la respuesta ya redactada y, si el bot está preguntando el color o
// la cantidad SIN haber mandado menú, lo manda el código.
//
// POR QUÉ ESTOS DOS Y NO TODOS. Color y cantidad son los únicos datos del pedido que (a) el
// código sabe cuándo faltan —la ficha PedidoEnCurso lo dice— y (b) tienen un conjunto de opciones
// que el código puede construir solo: los colores salen del catálogo y las cantidades son
// números. El resto (cédula, nombre, ubicación) no tiene opciones que ofrecer.
//
// El texto del modelo SE RESPETA como cuerpo del menú: está redactado con el saludo y el tono de
// la conversación, y reemplazarlo por una frase genérica sonaría a máquina. Solo se le añaden los
// botones que le faltaban.
package agent

import (
	"log"
	"strings"
)

// BotonMasCilindros es la última opción del menú de cantidad, para quien necesita más de los
// sugeridos. No es un pedido: abre la puerta a escribir el número.
//
// Hace falta porque los botones tappables de WhatsApp son 3 como máximo, y con [1/2/3] el
// cliente que quiere 6 —hay dos pedidos reales de 6 en producción— tenía que adivinar que podía
// escribirlo. Con 4 o más opciones WhatsApp manda una LISTA desplegable, así que la opción cabe.
const BotonMasCilindros = "Más de 3"

// cantidadesSugeridas son las opciones de "¿cuántos?". Decisión del dueño (24-sep): lista
// desplegable, y para más de 3 que el cliente escriba el número.
//
// Son CUATRO entradas a propósito: con tres o menos WhatsApp las manda como botones y no cabría
// la salida para cantidades mayores (ver buildInteractiveMenu). El reparto real de producción es
// 1 → 67 pedidos, 2 → 23, 3 → 5, 6 → 2: los tres primeros cubren el 95%, y el resto ya no queda
// sin camino.
func cantidadesSugeridas() []string {
	return []string{"1", "2", "3", BotonMasCilindros}
}

// preguntaElColor detecta que el texto le está preguntando al cliente qué color o marca quiere.
func preguntaElColor(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"que", "color"}, {"cual", "color"}, {"cuál", "color"},
		{"que", "marca"}, {"cual", "marca"},
		{"color", "prefieres"}, {"color", "necesitas"}, {"color", "quieres"},
		{"marca", "prefieres"}, {"marca", "necesitas"}, {"marca", "quieres"},
		{"de", "que", "color"}, {"que", "tipo", "de", "cilindro"},
	}, 3)
}

// preguntaLaCantidad detecta que el texto le está preguntando cuántos cilindros quiere.
func preguntaLaCantidad(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"cuantos", "cilindros"}, {"cuantos", "tanques"}, {"cuantos", "necesitas"},
		{"cuantos", "quieres"}, {"cuantos", "deseas"}, {"cuantas", "unidades"},
		{"cuantos", "te", "llevo"}, {"cuantos", "van"}, {"que", "cantidad"},
	}, 3)
}

// revisarMenuDeProducto es el CANDADO: si el bot preguntó el color o la cantidad sin mandar el
// menú, lo manda el código.
//
// Decide por ESTADO (la ficha dice qué falta) y usa el texto solo para saber QUÉ está preguntando.
// Devuelve la respuesta que debe salir; si mandó el menú, marca t.menuSent para que el llamador no
// envíe además el texto.
func (a *Agent) revisarMenuDeProducto(t *turno, from, reply string) string {
	// Si el modelo YA mandó un menú en este turno, no hay nada que arreglar. Y si mandó uno, no se
	// puede mandar otro: el tope de una pregunta por turno también vale aquí.
	if t.menuSent || strings.TrimSpace(reply) == "" {
		return reply
	}

	p, _ := a.store.GetPedidoEnCurso(from)
	lineas := p.Lineas()

	// COLOR: solo si de verdad falta (no hay ninguna línea con color) y el texto lo está pidiendo.
	if len(lineas) == 0 && preguntaElColor(reply) {
		if opciones := a.coloresDisponibles(); len(opciones) >= 2 {
			return a.mandarMenuDelCandado(t, from, reply, opciones, "color")
		}
	}

	// CANTIDAD: solo si hay color elegido y a alguna línea le falta el número.
	if len(lineas) > 0 && preguntaLaCantidad(reply) {
		faltaCantidad := false
		for _, l := range lineas {
			if l.Cantidad < 1 {
				faltaCantidad = true
				break
			}
		}
		if faltaCantidad {
			return a.mandarMenuDelCandado(t, from, reply, cantidadesSugeridas(), "cantidad")
		}
	}

	return reply
}

// mandarMenuDelCandado envía el menú usando el TEXTO DEL MODELO como cuerpo, para no perder su
// saludo ni su tono. Si el envío falla, se devuelve el texto tal cual: el cliente recibe la
// pregunta escrita, como antes, en vez de quedarse sin respuesta.
func (a *Agent) mandarMenuDelCandado(t *turno, from, cuerpo string, opciones []string, que string) string {
	if err := a.mandarMenu(from, cuerpo, opciones); err != nil {
		log.Printf("[menu-seguro] %s: el menú de %s falló (%v); sale como texto", from, que, err)
		return cuerpo
	}
	log.Printf("[menu-seguro] %s: el modelo preguntó el %s sin menú; se mandó en código", from, que)
	t.menuSent = true
	t.lastMenuText = cuerpo + " [" + strings.Join(opciones, " / ") + "]"
	return ""
}

// coloresDisponibles saca del catálogo los colores/marcas para el menú. Devuelve nil si no hay
// catálogo o si hay demasiados para un menú de WhatsApp (tope de 10 filas).
func (a *Agent) coloresDisponibles() []string {
	contexto, ok := a.catalog.Get()
	if !ok || contexto == nil {
		return nil
	}
	var colores []string
	vistos := map[string]bool{}
	for _, prod := range contexto.Products {
		for _, c := range prod.Colores {
			nombre := strings.TrimSpace(c.Nombre)
			if nombre == "" || vistos[strings.ToUpper(nombre)] {
				continue
			}
			vistos[strings.ToUpper(nombre)] = true
			colores = append(colores, nombre)
		}
	}
	if len(colores) > maxOpcionesMenu {
		// Con más de 10 no cabe un menú de WhatsApp: el prompt ya manda caer a lista numerada.
		return nil
	}
	return colores
}

// maxOpcionesMenu es el tope de WhatsApp para una lista interactiva.
const maxOpcionesMenu = 10

// NOTA: la respuesta al menú de cantidad NO necesita interceptor. El botón devuelve "2", y
// anotarDelMensaje (encurso.go) ya reconoce un número suelto como la cantidad del color en curso:
// es el mismo camino que si el cliente lo escribiera. Añadir un interceptor aquí sería un segundo
// sitio decidiendo lo mismo.
