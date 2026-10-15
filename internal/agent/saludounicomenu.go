package agent

import "log"

// limpiarSaludoDelCuerpoDeMenu aplica el candado del saludo duplicado al CUERPO DE UN MENÚ antes
// de enviarlo. Es el mismo problema de saludounico.go pero en otro camino: cuando el turno acaba
// en menú, `revisarSaludoDuplicado` (que solo actúa sobre `reply`) no llega a ver el cuerpo.
//
// CASO REAL (593995446872, 2-oct): el código mandó la bienvenida ("Soy *Ubi*") y el modelo
// redactó el menú de color con "📋 ¡Hola! 👋 Con gusto te ayudo…". Dos saludos en un segundo. Los
// 7 casos del 2-oct son TODOS menús: "menu=True" en el análisis forense. La tarea #55 (MEJ-1)
// solucionó los saludos de texto plano, pero esto es el camino de menú.
//
// El candado se aplica en DOS sitios (ambos envían menús):
//   - mostrarMenu (agent.go:879) — el modelo llamó mostrar_menu como tool
//   - mandarMenuDelCandado (menuseguro.go:113) — el código mandó menú si el modelo no lo hizo
func (a *Agent) limpiarSaludoDelCuerpoDeMenu(from, cuerpo string) string {
	if !a.yaSePresentoElCodigo(from) {
		return cuerpo // el código no se presentó: el modelo PUEDE saludar
	}
	limpio := quitarSaludoDuplicado(cuerpo, a.nombresDelCliente(from)...)
	if limpio == cuerpo {
		return cuerpo // el modelo no saludó en el cuerpo: perfecto
	}
	if limpio == "" {
		// El cuerpo ERA solo el saludo (el modelo no redactó pregunta). Se deja el texto original
		// para no mandar un menú vacío — los botones necesitan contexto. El saludo repetido sale,
		// pero eso es mejor que un menú sin cuerpo: el cliente al menos entiende qué eligiendo.
		log.Printf("[saludo-menu] %s: el cuerpo del menú ERA solo saludo; se deja (no puedo mandar menú vacío)", from)
		return cuerpo
	}
	log.Printf("[saludo-menu] %s: se quitó el saludo repetido del cuerpo del menú", from)
	return limpio
}
