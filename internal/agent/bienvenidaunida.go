// LA PRESENTACIÓN VIAJA DENTRO DEL PRIMER MENSAJE, NO COMO UN WHATSAPP APARTE.
//
// Pedido del dueño (02/10, caso 593963518172): el cliente escribió "Gas" y recibió DOS mensajes
// —la presentación y, seis segundos después, el menú de colores—. "la idea es q envies solo uno
// con todo eso".
//
// ─────────────────────────────────────────────────────────────────────────────────────────────
// SEGUNDO INTENTO. El primero (fe52d6a, revertido en e55f1ab) rompió el flujo e hizo daño a
// clientes reales el 03/10. Lo que falló, porque define este diseño:
//
// Para que el candado del doble saludo (saludounico.go) siguiera viendo la presentación, aquel
// intento la registraba en el HISTORIAL DEL MODELO con AppendModel. Eso trajo dos males:
//
//  1. El modelo LEÍA el párrafo y lo COPIABA. Seis clientes (JULIO 5939976, jeff 5939396,
//     Blanca 5939816 y tres más) recibieron la bienvenida DUPLICADA en vez de unida.
//  2. Peor: el último turno del modelo pasó a ser la presentación en vez de `lastMenuText`, así
//     que el modelo perdía el hilo de QUÉ HABÍA PREGUNTADO. Jefferson (5939396) contestó "1" al
//     menú de colores, el bot lo interpretó como color y le volvió a preguntar la cantidad: tuvo
//     que repetir "1" dos veces y "Blanco" una de más.
//
// LA LECCIÓN: el historial del modelo es su memoria de trabajo, no un registro de lo enviado.
// Meter ahí un texto que el modelo no escribió lo invita a repetirlo y desplaza el dato que sí
// necesita. La presentación la decide y la escribe el CÓDIGO: el modelo no tiene que verla.
//
// ─────────────────────────────────────────────────────────────────────────────────────────────
// EL DISEÑO DE AHORA, en tres piezas separadas a propósito:
//
//   - LO QUE SE ENVÍA: la presentación se antepone al cuerpo del primer mensaje del turno (menú
//     o texto), igual que `conCoberturaConfirmada`. Un solo WhatsApp.
//   - LO QUE VE EL MODELO: NADA. Su historial sigue teniendo `lastMenuText` como último turno,
//     que es lo que necesita para no repetir preguntas.
//   - LO QUE SABE EL CANDADO: una BANDERA de estado (`SetYaSePresento`), no un turno del
//     historial. El candado del doble saludo pregunta por la bandera.
//
// Así cada quien tiene su dato y ninguno pisa al otro.
package agent

import (
	"log"
	"strings"
)

// DejarBienvenidaPendiente guarda la presentación para que salga DENTRO del primer mensaje del
// turno. La llama el webhook en vez de enviarla por su cuenta.
//
// NO toca el historial del modelo, y eso es lo que arregla el intento anterior: el modelo no ve
// este texto, así que no puede copiarlo ni desplaza su `lastMenuText`. Lo único que se marca es
// la BANDERA que el candado del doble saludo consulta (ver yaSePresentoElCodigo).
func (a *Agent) DejarBienvenidaPendiente(from, texto string) {
	if strings.TrimSpace(texto) == "" {
		return
	}
	a.store.SetBienvenidaPendiente(from, texto)
	// La bandera para el candado del doble saludo. Se marca AQUÍ y no al enviar, porque los
	// candados corren durante el turno y necesitan el dato ya puesto: la presentación va a salir
	// sí o sí (dentro del primer mensaje, o suelta por la red de seguridad del webhook).
	a.store.MarcarYaSePresento(from)
}

// HayBienvenidaPendiente dice si queda una presentación sin entregar. El webhook lo consulta al
// terminar el turno: hay caminos que cortan sin que el modelo hable (fuera de cobertura, media no
// soportada, control humano) y en ellos la presentación tiene que salir suelta — un cliente nuevo
// sin saludo es peor que dos mensajes.
func (a *Agent) HayBienvenidaPendiente(from string) (string, bool) {
	texto := a.store.BienvenidaPendiente(from)
	return texto, strings.TrimSpace(texto) != ""
}

// ConsumirBienvenidaPendiente la marca como entregada.
func (a *Agent) ConsumirBienvenidaPendiente(from string) {
	a.store.LimpiarBienvenidaPendiente(from)
}

// conBienvenida antepone la presentación al mensaje que va a salir —cuerpo de menú o texto de
// respuesta— y consume la marca. Si no hay nada pendiente, devuelve el mensaje tal cual.
//
// El separador es una línea en blanco: en WhatsApp deja la presentación como un párrafo y la
// pregunta debajo, que es como se lee bien dentro de un solo globo.
func (a *Agent) conBienvenida(from, mensaje string) string {
	bienvenida := strings.TrimSpace(a.store.BienvenidaPendiente(from))
	if bienvenida == "" {
		return mensaje
	}
	a.store.LimpiarBienvenidaPendiente(from)
	// AUDITORÍA (lo que el panel muestra), no historial del modelo. Son dos registros distintos a
	// propósito: el panel tiene que poder mostrar que la presentación salió, y el modelo NO tiene
	// que verla. Mezclarlos fue el error del primer intento.
	a.store.LogMessage(from, "system", bienvenida)
	if strings.TrimSpace(mensaje) == "" {
		return bienvenida
	}
	log.Printf("[bienvenida] %s: la presentación viaja dentro del primer mensaje del turno", from)
	return bienvenida + "\n\n" + mensaje
}
