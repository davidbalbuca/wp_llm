package agent

import (
	"log"
	"strings"
)

// Candado de la app móvil.
//
// Caso real del 06/11: un anuncio de Facebook trae gente preguntando "me interesa descargar su
// App para pedir gas, ¿me puede ayudar?" y el modelo contestaba que la app NO existe:
//
//	"No manejamos ninguna app, aquí mismo por WhatsApp te tomo tu pedido"
//	"No tenemos una aplicación ni usuario/contraseña"
//	"Por ahora no manejamos una aplicación para descargar"
//	"en realidad no tenemos una app como tal"
//
// Es FALSO: la app existe y su página de descarga es LinkApp (guardarcontacto.go). El bot la
// promociona él mismo en la bienvenida y al confirmar un pedido, así que se contradecía solo.
//
// Por qué pasa: el prompt (prompts/behavior.md) no menciona la app en ninguna parte. El modelo no
// tiene el dato y rellena el hueco en negativo, que es lo que suena prudente. El arreglo va en
// CÓDIGO y no en el prompt para que el modelo no pueda desobedecerlo.
//
// Se aplica en DOS sitios (agent.go): sobre la respuesta y sobre el cuerpo del menú. Los cuatro
// casos reales acabaron en menú de colores, y un candado que solo reescribe la respuesta se
// pierde justo ahí -la misma lección de conCoberturaConfirmada-.

// niegaLaApp detecta que el texto le está diciendo al cliente que no tenemos app.
//
// Las secuencias son deliberadamente ESTRECHAS y en primera persona del plural: hablan de lo que
// la EMPRESA tiene. "No tienes la app instalada" o "no hay problema con la app" son otra cosa y
// taparlas rompería la conversación. Por eso tampoco entra {"no","hay","app"}: con holgura
// matchearía "no hay problema con la app".
func niegaLaApp(texto string) bool {
	return afirmaSecuencia(texto, [][]string{
		{"no", "tenemos", "app"}, {"no", "tenemos", "aplicacion"},
		{"no", "manejamos", "app"}, {"no", "manejamos", "aplicacion"},
		{"no", "contamos", "app"}, {"no", "contamos", "aplicacion"},
		{"no", "disponemos", "app"}, {"no", "disponemos", "aplicacion"},
		{"no", "existe", "app"}, {"no", "existe", "aplicacion"},
		{"no", "hay", "ninguna", "app"}, {"no", "hay", "ninguna", "aplicacion"},
		{"sin", "app", "propia"},
	}, 2)
}

// MensajeLaAppSiExiste es la verdad, con el enlace. Una sola linea, como las demas invitaciones
// (guardarcontacto.go, bienvenida.go).
//
// Redaccion pedida por David el 06/11: "dos PASOS", no "toques"; y NADA de registro, usuario ni
// contrasena -alarga el mensaje y no hace falta contarlo-. Lo esencial es que se la descargue y
// que le busca al repartidor mas cercano en minutos.
func MensajeLaAppSiExiste() string {
	return "📲 ¡Sí tenemos app! Pide tu gas en dos pasos y te busca al repartidor más cercano " +
		"en minutos 👉 " + LinkApp
}

// sinLaNegacionDeLaApp quita SOLO las oraciones que niegan la app y deja el resto del mensaje.
// El mismo criterio que el candado del pedido fantasma (forzar.go): lo que el modelo escribió
// alrededor -la pregunta del color, el saludo- suele estar bien y tirarlo empeora la respuesta.
func sinLaNegacionDeLaApp(texto string) string {
	var quedan []string
	for _, f := range frases(texto) {
		if !niegaLaApp(f) {
			quedan = append(quedan, f)
		}
	}
	return strings.TrimSpace(strings.Join(quedan, " "))
}

// revisarNegativaDeApp reemplaza la negativa por la verdad con el enlace, conservando el resto.
func (a *Agent) revisarNegativaDeApp(from, reply string) string {
	if !niegaLaApp(reply) {
		return reply
	}
	log.Printf("[app] %s: el modelo dijo que no tenemos app; se reemplaza por el enlace de descarga", from)
	resto := sinLaNegacionDeLaApp(reply)
	if resto == "" {
		return MensajeLaAppSiExiste()
	}
	return MensajeLaAppSiExiste() + "\n\n" + resto
}
