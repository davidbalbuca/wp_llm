package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// CASO REAL 593963518172 (02/10): el cliente escribió "Gas" y recibió DOS mensajes —la
// presentación y, seis segundos después, el menú de colores—. Tiene que salir UNO.
func TestBienvenidaViajaEnElCuerpoDelMenu(t *testing.T) {
	store := conversation.NewMemStore()
	from := "593963518172"
	var cuerpoEnviado string
	ag := &Agent{store: store, enviarMenu: func(_, cuerpo string, _ []string) error {
		cuerpoEnviado = cuerpo
		return nil
	}}

	ag.DejarBienvenidaPendiente(from, "¡Buenas tardes, Leandro! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor más cercano.")
	if err := ag.mandarMenu(from, "¿Qué cilindro de 15kg necesitas?", []string{"Blanco", "Amarillo"}); err != nil {
		t.Fatalf("mandarMenu: %v", err)
	}

	if !strings.Contains(cuerpoEnviado, "*UbiGas*, tu repartidor aquísito") {
		t.Errorf("la presentación NO viajó en el cuerpo del menú:\n%q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, "¿Qué cilindro de 15kg necesitas?") {
		t.Errorf("se perdió la pregunta del menú:\n%q", cuerpoEnviado)
	}
	if strings.Index(cuerpoEnviado, "*UbiGas*, tu repartidor aquísito") > strings.Index(cuerpoEnviado, "¿Qué cilindro") {
		t.Errorf("la presentación va ANTES de la pregunta:\n%q", cuerpoEnviado)
	}
	if _, hay := ag.HayBienvenidaPendiente(from); hay {
		t.Error("la marca sigue pendiente: saldría además un mensaje suelto")
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────
// LAS DOS REGRESIONES DEL 03/10. El primer intento (fe52d6a) metía la presentación en el
// historial del modelo con AppendModel, y eso hizo daño real en producción. Estos dos tests son
// la red que impide que vuelva.
// ─────────────────────────────────────────────────────────────────────────────────────────────

// REGRESIÓN 1 — BIENVENIDA DUPLICADA. Seis clientes (JULIO 5939976, jeff 5939396, Blanca 5939816
// y tres más) recibieron el párrafo DOS VECES: el modelo lo leía en su historial y lo copiaba.
// El historial del modelo NO puede contener la presentación.
func TestElModeloNoVeLaPresentacionEnSuHistorial(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593997600001"

	ag.DejarBienvenidaPendiente(from, "¡Buenas tardes, JULIO! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor más cercano.")

	for _, c := range store.History(from) {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && strings.Contains(p.Text, marcaDePresentacion) {
				t.Fatalf("la presentación está en el historial del modelo (rol %q): la copiará y "+
					"saldrá duplicada, como el 03/10 con JULIO y 5 clientes más", c.Role)
			}
		}
	}
}

// REGRESIÓN 2 — EL MODELO PIERDE EL HILO. Peor que la duplicación: al ocupar el último turno del
// modelo, la presentación desplazaba a `lastMenuText`, que es el dato con el que recuerda QUÉ
// PREGUNTÓ. Jefferson (5939396) contestó "1" al menú de colores, el bot lo tomó como color y le
// volvió a preguntar la cantidad: tuvo que repetir "1" dos veces y "Blanco" una más.
//
// Tras un turno que acaba en menú, el último turno del modelo debe ser LA PREGUNTA DEL MENÚ.
func TestTrasElMenuElUltimoTurnoDelModeloEsLaPregunta(t *testing.T) {
	store := conversation.NewMemStore()
	from := "593939600002"
	ag := &Agent{store: store, enviarMenu: func(_, _ string, _ []string) error { return nil }}

	ag.DejarBienvenidaPendiente(from, "¡Buenas tardes, Jefferson! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor.")

	// Simula el cierre de un turno que acabó en menú (lo que hace HandleMessage).
	t2 := &turno{}
	if err := ag.mandarMenu(from, "¿Qué color/marca de cilindro necesitas?", []string{"Blanco", "Amarillo"}); err != nil {
		t.Fatalf("mandarMenu: %v", err)
	}
	t2.menuSent = true
	t2.lastMenuText = "¿Qué color/marca de cilindro necesitas?\n• Blanco\n• Amarillo"
	store.AppendUser(from, "Gas")
	store.AppendModel(from, t2.lastMenuText)

	hist := store.History(from)
	var ultimoModelo string
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i] != nil && hist[i].Role == "model" && len(hist[i].Parts) > 0 {
			ultimoModelo = hist[i].Parts[0].Text
			break
		}
	}
	if !strings.Contains(ultimoModelo, "color/marca") {
		t.Errorf("el último turno del modelo no es la pregunta del menú — perdería el hilo y "+
			"volvería a pedir datos ya dados (caso Jefferson 03/10).\nobtenido: %q", ultimoModelo)
	}
	if strings.Contains(ultimoModelo, marcaDePresentacion) {
		t.Errorf("la presentación desplazó a lastMenuText como último turno del modelo:\n%q", ultimoModelo)
	}
}

// El candado del doble saludo tiene que seguir viendo que el código se presentó, ahora por la
// BANDERA y no por el historial. Si esto falla, vuelve el doble saludo del 02/10.
func TestElCandadoDelDobleSaludoVeLaBandera(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900600003"

	ag.DejarBienvenidaPendiente(from, "¡Hola, Ana! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor.")
	if !ag.yaSePresentoElCodigo(from) {
		t.Fatal("el candado no ve la presentación: el modelo saludaría otra vez")
	}
	if limpio := ag.limpiarSaludoDelCuerpoDeMenu(from, "📋 ¡Hola, Ana! 👋 ¿Qué color necesitas?"); strings.Contains(limpio, "¡Hola, Ana!") {
		t.Errorf("el candado no quitó el saludo repetido del cuerpo del menú:\n%q", limpio)
	}
}

// TODO JUNTO, como pasa en el turno real: el código se presenta, el modelo redacta el menú
// saludando por su cuenta → sale UN mensaje, con UN saludo y la pregunta.
func TestUnSoloMensajeConUnSoloSaludo(t *testing.T) {
	store := conversation.NewMemStore()
	from := "593963518172"
	var cuerpoEnviado string
	ag := &Agent{store: store, enviarMenu: func(_, cuerpo string, _ []string) error {
		cuerpoEnviado = cuerpo
		return nil
	}}

	ag.DejarBienvenidaPendiente(from, "¡Buenas tardes, Leandro! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor más cercano.")
	cuerpo := ag.limpiarSaludoDelCuerpoDeMenu(from, "📋 ¡Hola, Leandro! 👋 Con gusto te ayudo. ¿Qué color necesitas?")
	if err := ag.mandarMenu(from, cuerpo, []string{"Blanco", "Amarillo"}); err != nil {
		t.Fatalf("mandarMenu: %v", err)
	}

	if n := strings.Count(cuerpoEnviado, "👋"); n != 1 {
		t.Errorf("se esperaba UN saludo, hay %d:\n%q", n, cuerpoEnviado)
	}
	if strings.Contains(cuerpoEnviado, "¡Hola, Leandro!") {
		t.Errorf("sobrevivió el saludo del modelo:\n%q", cuerpoEnviado)
	}
	if !strings.Contains(cuerpoEnviado, "*UbiGas*, tu repartidor aquísito") || !strings.Contains(cuerpoEnviado, "¿Qué color necesitas?") {
		t.Errorf("el mensaje único perdió la presentación o la pregunta:\n%q", cuerpoEnviado)
	}
}

// Cuando el turno acaba en TEXTO (preguntó el precio, no pidió gas), la presentación viaja igual.
func TestBienvenidaViajaEnElTextoDeLaRespuesta(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900600004"

	ag.DejarBienvenidaPendiente(from, "¡Hola! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor más cercano.")
	salida := ag.conBienvenida(from, "El cilindro de 15KG cuesta $3.25 😊")

	if !strings.Contains(salida, "*UbiGas*, tu repartidor aquísito") || !strings.Contains(salida, "$3.25") {
		t.Errorf("el mensaje único perdió la presentación o la respuesta:\n%q", salida)
	}
}

// Sin presentación pendiente no se toca nada: a quien está a media conversación no se le saluda.
func TestSinBienvenidaPendienteNoSeTocaElMensaje(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	const original = "¿Cuántos cilindros necesitas?"
	if got := ag.conBienvenida("593900600005", original); got != original {
		t.Errorf("se modificó el mensaje sin presentación pendiente:\n%q", got)
	}
}

// La presentación se entrega UNA sola vez: el segundo mensaje del turno ya no la lleva.
func TestLaPresentacionSeEntregaUnaSolaVez(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900600006"

	ag.DejarBienvenidaPendiente(from, "¡Hola! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor.")
	primero := ag.conBienvenida(from, "¿Qué color necesitas?")
	segundo := ag.conBienvenida(from, "¿Cuántos cilindros?")

	if !strings.Contains(primero, "*UbiGas*, tu repartidor aquísito") {
		t.Errorf("el primer mensaje debía llevarla:\n%q", primero)
	}
	if strings.Contains(segundo, "*UbiGas*, tu repartidor aquísito") {
		t.Errorf("el segundo mensaje la repite:\n%q", segundo)
	}
}

// La auditoría (lo que ve el panel) SÍ registra la presentación, aunque el modelo no la vea. Son
// dos registros distintos a propósito.
func TestLaAuditoriaRegistraLaPresentacion(t *testing.T) {
	store := conversation.NewMemStore()
	ag := &Agent{store: store}
	from := "593900600007"

	ag.DejarBienvenidaPendiente(from, "¡Hola! 👋 ¿Se te acabó el gas? 😱 ¡Con *UbiGas*, tu repartidor aquísito no más! 🔥\n\nTe conecto con el repartidor.")
	ag.conBienvenida(from, "¿Qué color necesitas?")

	visto := false
	for _, m := range store.GetConversation(from, 0) {
		if strings.Contains(m.Content, marcaDePresentacion) {
			visto = true
		}
	}
	if !visto {
		t.Error("la presentación no quedó en la auditoría: el panel no podría mostrar que salió")
	}
}
