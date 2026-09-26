package agent

import (
	"context"
	"strings"
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
)

// "7 DE LA NOCHE" SON LAS 19:00, NO LAS 07:00.
//
// INCIDENTE 25/09 (593986074612): pidió 1 cilindro amarillo y escribió "Puede ser a las 7 de la
// noche por favor." El log registró hora="07:00" — doce horas antes de lo que pidió.
//
// El ajuste de 12h miraba solo "pm"/"am" EN LETRAS, y en español casi nadie las escribe. Y el
// fallo no daba la cara: 07:00 cae DENTRO del horario de atención (07:00–19:00), así que ninguna
// validación posterior lo atrapaba. Nadie se entera hasta que el repartidor llega o no llega.

func TestIncidente_LaHoraDeLaNocheNoEsDeLaManana(t *testing.T) {
	// El mensaje REAL del cliente, palabra por palabra.
	if got := extraerHora("Puede ser a las 7 de la noche por favor."); got != "19:00" {
		t.Fatalf("\"a las 7 de la noche\" -> %q (esperado 19:00). Doce horas de diferencia.", got)
	}
}

func TestExtraerHoraConMarcadoresEnEspanol(t *testing.T) {
	casos := []struct{ in, want string }{
		// Lo que ya funcionaba: no se puede romper.
		{"18:30", "18:30"},
		{"a las 7 pm", "19:00"},
		{"6:30 pm", "18:30"},
		{"quiero a las 15", "15:00"},
		{"6h30", "06:30"},
		{"18h", "18:00"},

		// Lo que fallaba: la franja del día en palabras.
		{"a las 7 de la noche", "19:00"},
		{"7 de la noche", "19:00"},
		{"a las 7 de la tarde", "19:00"},
		{"2 de la tarde", "14:00"},
		{"a las 4 de la tarde porfa", "16:00"},
		{"tipo 8 de la mañana", "08:00"},
		{"a las 8 de la manana", "08:00"}, // sin tilde, como lo escribe mucha gente
		{"en la noche a las 6", "18:00"},

		// Números en palabras.
		{"siete de la noche", "19:00"},
		{"a las cinco de la tarde", "17:00"},
		{"ocho de la mañana", "08:00"},

		// Horas exactas con nombre propio.
		{"al mediodía", "12:00"},
		{"a mediodia", "12:00"},

		// Una hora que YA viene en 24h no se toca aunque se diga la franja.
		{"a las 18 de la tarde", "18:00"},
		{"18:30 de la noche", "18:30"},

		// Lo que NO es una hora.
		{"Blanco", ""},
		{"María Elena Castillo", ""},
		{"dos cilindros", ""}, // número en palabras SIN franja: es una cantidad
	}
	for _, c := range casos {
		if got := extraerHora(c.in); got != c.want {
			t.Errorf("extraerHora(%q) = %q, esperado %q", c.in, got, c.want)
		}
	}
}

func TestLaFranjaDelDiaManda(t *testing.T) {
	// "mañana a las 7 de la noche": el día es mañana, la franja es la noche.
	tarde, manana := franjaDelDia("mañana a las 7 de la noche")
	if !tarde || manana {
		t.Errorf("\"mañana ... de la noche\" -> tarde=%v manana=%v; la franja manda sobre el día", tarde, manana)
	}
	// "12 de la mañana" es medianoche, no mediodía.
	if got := ajustarFranja(12, false, true); got != 0 {
		t.Errorf("12 de la mañana -> %d, esperado 0", got)
	}
}

// LA HORA AMBIGUA SE PREGUNTA (decisión del dueño, 25/09).
//
// "a las 7" a secas no dice si son las 07:00 o las 19:00, y con el horario del negocio
// (07:00–19:00) LAS DOS son válidas: no hay forma de desempatar por horario. Adivinar cuesta una
// entrega a la hora equivocada.

func TestLaHoraAmbiguaSePreguntaNoSeAdivina(t *testing.T) {
	const from = "593986074613"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.91, -79.03)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"¡Listo, lo agendo!"}}, store)
	ag.catalog = catalogoConEquivalencias()
	var cuerpo string
	var opciones []string
	ag.enviarMenu = func(_, c string, o []string) error { cuerpo, opciones = c, o; return nil }
	ag.anotarDelMensaje(from, "Amarillo")
	ag.anotarDelMensaje(from, "1")

	res, err := ag.HandleMessage(context.Background(), from, "puede ser a las 7")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !res.MenuEnviado {
		t.Fatalf("no se preguntó por la hora ambigua; respuesta: %q", res.Texto)
	}
	if len(opciones) != 2 {
		t.Fatalf("se ofrecieron %d opciones: %v", len(opciones), opciones)
	}
	bajo := strings.ToLower(cuerpo)
	if !strings.Contains(bajo, "mañana") || !strings.Contains(bajo, "noche") {
		t.Errorf("la pregunta no ofrece las dos mitades del día: %q", cuerpo)
	}
}

// Y con marcador NO se pregunta: sería molestar al cliente que ya fue claro.
func TestConMarcadorNoSePreguntaNada(t *testing.T) {
	for _, texto := range []string{
		"a las 7 de la noche", "a las 7 pm", "quiero a las 15", "18:30", "al mediodía",
	} {
		if _, ambigua := horaAmbigua(texto); ambigua {
			t.Errorf("%q se tomó por ambigua y el cliente ya había sido claro", texto)
		}
	}
	// Y estas SÍ lo son.
	for _, texto := range []string{"a las 7", "para las 5", "tipo 8"} {
		h, ambigua := horaAmbigua(texto)
		if !ambigua {
			t.Errorf("%q no se detectó como ambigua", texto)
		}
		if h < 1 || h > 11 {
			t.Errorf("%q dio la hora %d, fuera del rango ambiguo", texto, h)
		}
	}
}

// El botón elegido deja la hora correcta en la ficha: la que el cliente TOCÓ.
func TestResponderHoraAmbiguaAnotaLaHoraElegida(t *testing.T) {
	casos := []struct {
		opcion string
		quiero string
	}{
		{"7 de la noche", "19:00"},
		{"7 de la mañana", "07:00"},
		{"5 de la noche", "17:00"},
	}
	for _, c := range casos {
		const from = "593986074614"
		store := conversation.NewMemStore()
		ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
		ag.catalog = catalogoConEquivalencias()
		ag.anotarDelMensaje(from, "Amarillo")
		ag.anotarDelMensaje(from, "1")

		if _, ok := ag.ResponderHoraAmbigua(from, c.opcion); !ok {
			t.Fatalf("no se resolvió el botón %q", c.opcion)
		}
		p, _ := store.GetPedidoEnCurso(from)
		if p.Hora != c.quiero {
			t.Errorf("%q dejó la hora en %q, esperado %q", c.opcion, p.Hora, c.quiero)
		}
		if p.Flujo != conversation.FlujoProgramacion {
			t.Errorf("%q no dejó el pedido en flujo de programación", c.opcion)
		}
	}
}

// Y si el cliente contesta otra cosa, lo atiende el modelo: "mejor a las 4" es conversación, no
// un botón, y taparlo rompería el criterio del dueño (contestar lo que el cliente pregunta).
func TestOtraRespuestaALaHoraAmbiguaVaAlModelo(t *testing.T) {
	const from = "593986074615"
	store := conversation.NewMemStore()
	ag := agentIncidente(&modeloQueDice{respuestas: []string{""}}, store)
	ag.catalog = catalogoConEquivalencias()
	ag.anotarDelMensaje(from, "Amarillo")
	ag.anotarDelMensaje(from, "1")

	for _, texto := range []string{"mejor a las 4", "no importa", "hola"} {
		if _, ok := ag.ResponderHoraAmbigua(from, texto); ok {
			t.Errorf("%q se tomó por una de las dos opciones del menú", texto)
		}
	}
}

// La hora ambigua NO se cuela como cantidad ni deja la ficha a medias mientras se pregunta.
func TestMientrasSePreguntaLaHoraNoSeAnotaNingunaHora(t *testing.T) {
	const from = "593986074616"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.91, -79.03)
	ag := agentIncidente(&modeloQueDice{respuestas: []string{"ok"}}, store)
	ag.catalog = catalogoConEquivalencias()
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}
	ag.enviarMenu = func(string, string, []string) error { return nil }
	ag.anotarDelMensaje(from, "Amarillo")
	ag.anotarDelMensaje(from, "1")

	if _, err := ag.HandleMessage(context.Background(), from, "a las 7"); err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	p, _ := store.GetPedidoEnCurso(from)
	// La cantidad sigue siendo la que pidió: el 7 no se leyó como cilindros.
	if p.Cantidad != 1 {
		t.Errorf("la hora se coló como cantidad: %d cilindros", p.Cantidad)
	}
}

// UN NÚMERO SUELTO NO ES UNA HORA: ES LA RESPUESTA A OTRO MENÚ.
//
// INCIDENTE 26/09 (593964011403): el bot le mostró el menú de CANTIDAD ([1 2 3 4]), el cliente
// tocó "1" y este candado lo leyó como "la 1" y le preguntó "¿mañana o noche?". El cliente
// insistió "1 cilindro!" y volvió a recibir la misma pregunta; acabó contestando "1 de la
// mañana" y el pedido quedó agendado a las 01:00 —fuera del horario 07:00–19:00— seguido de un
// "Noooooooooo".
//
// El candado que evita adivinar la hora no puede, a cambio, adivinar QUE se está hablando de una
// hora. Se exige marcaHoraria, que ya existía justo para esta distinción.
func TestIncidente_LaCantidadDelMenuNoEsUnaHora(t *testing.T) {
	// El menú de cantidad, tal como lo ofrece el bot.
	for _, texto := range []string{"1", "2", "3", "4", "1 cilindro!", "2 cilindros"} {
		if h, ambigua := horaAmbigua(texto); ambigua {
			t.Errorf("%q se tomó por la hora %d; era la cantidad", texto, h)
		}
	}
	// Otras respuestas a menús y datos que tampoco son horas.
	for _, texto := range []string{"Sí", "No", "Blanco", "Amarillo", "0900690256", "Bolivar Sánchez"} {
		if h, ambigua := horaAmbigua(texto); ambigua {
			t.Errorf("%q se tomó por la hora %d", texto, h)
		}
	}
	// Y la hora ambigua de verdad SIGUE preguntándose: el arreglo no puede apagar el candado.
	for _, c := range []struct {
		in string
		h  int
	}{{"a las 7", 7}, {"para las 5", 5}, {"tipo 8", 8}, {"a las 11", 11}} {
		h, ambigua := horaAmbigua(c.in)
		if !ambigua || h != c.h {
			t.Errorf("%q: hora=%d ambigua=%v; esperaba %d ambigua", c.in, h, ambigua, c.h)
		}
	}
}
