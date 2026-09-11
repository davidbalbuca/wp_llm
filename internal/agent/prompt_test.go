package agent

import (
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// La extracción del armado del prompt (de += en HandleMessage a strings.Builder en
// construirSistema) NO puede cambiar el texto que ve el modelo: un byte distinto cambia su
// comportamiento. Este test fija los invariantes de cada bloque condicional.
func TestConstruirSistemaBloques(t *testing.T) {
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{{ID: 10, Nombre: "BLANCO"}}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	cfg := config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}

	nuevo := func(store conversation.Store) *Agent {
		return &Agent{store: store, catalog: cat, cfg: cfg}
	}

	t.Run("cliente nuevo: sin datos, sin ubicación, con horario", func(t *testing.T) {
		a := nuevo(conversation.NewMemStore())
		fijo, vol := a.construirSistema("593999")
		// La parte fija SIEMPRE incluye las reglas + la info del servicio.
		if !strings.Contains(fijo, "INFORMACIÓN DEL SERVICIO:") {
			t.Error("la parte fija debe tener INFORMACIÓN DEL SERVICIO")
		}
		// Sin perfil no se inyectan DATOS DEL CLIENTE.
		if strings.Contains(vol, "DATOS DEL CLIENTE") {
			t.Error("cliente sin perfil no debe llevar DATOS DEL CLIENTE")
		}
		// Sin ubicación no se dice nada de ubicación.
		if strings.Contains(vol, "YA compartio su ubicacion") {
			t.Error("sin ubicación guardada no debe afirmar que la compartió")
		}
		// Fecha y hora SIEMPRE van: sin la fecha el modelo inventa el día y agenda mal.
		if !strings.Contains(vol, "HORA ACTUAL:") {
			t.Error("siempre debe incluir HORA ACTUAL")
		}
		if !strings.Contains(vol, "HOY ES:") {
			t.Error("siempre debe incluir la fecha (HOY ES); sin ella el modelo inventa el día")
		}
	})

	t.Run("cliente conocido con último pedido y ubicación", func(t *testing.T) {
		store := conversation.NewMemStore()
		store.SetProfile("593888", conversation.Profile{Identificacion: "0105888887", Nombres: "María José"})
		store.SetLastOrder("593888", conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "ayer"})
		store.SetLocation("593888", -2.9, -79.0)
		a := nuevo(store)
		_, vol := a.construirSistema("593888")

		for _, must := range []string{
			"DATOS DEL CLIENTE",
			"0105888887",
			"María José",
			"ÚLTIMO PEDIDO DEL CLIENTE",
			"2 BLANCO", // el resumen sale de describeItems (C1: soporta multicolor)
			"GAS 15KG",
			"YA compartio su ubicacion", // ya tiene ubicación guardada
		} {
			if !strings.Contains(vol, must) {
				t.Errorf("falta el bloque esperado: %q", must)
			}
		}
	})

	t.Run("calificación pendiente aparece", func(t *testing.T) {
		store := conversation.NewMemStore()
		store.SetPendingRating("593777", conversation.PendingRating{PedidoID: 5, Conductor: "Pedro"})
		a := nuevo(store)
		_, vol := a.construirSistema("593777")
		if !strings.Contains(vol, "CALIFICACIÓN PENDIENTE") || !strings.Contains(vol, "Pedro") {
			t.Error("debe incluir la calificación pendiente con el conductor")
		}
	})
}

// El bloque PEDIDO EN CURSO le dice al modelo qué tiene y qué le falta. Sin esto, el modelo
// tenía que deducir del historial en qué punto iba el pedido y volvía a preguntar datos ya
// dados (el caso del 05/09: la clienta dijo la hora tres veces).
func TestPromptMuestraPedidoEnCursoYLoQueFalta(t *testing.T) {
	const phone = "593999000077"
	casos := []struct {
		nombre     string
		ficha      conversation.PedidoEnCurso
		conUbicac  bool
		contiene   []string
		noContiene []string
	}{
		{
			nombre:     "solo color: falta cantidad y ubicación",
			ficha:      conversation.PedidoEnCurso{Color: "BLANCO", Flujo: conversation.FlujoInmediato},
			contiene:   []string{"PEDIDO EN CURSO", "color=BLANCO", "cantidad=FALTA", "FALTA:", "cantidad", "ubicación"},
			noContiene: []string{"FALTA: nada"},
		},
		{
			nombre:    "color + cantidad + ubicación: no falta nada",
			ficha:     conversation.PedidoEnCurso{Color: "AMARILLO", Cantidad: 2, Flujo: conversation.FlujoInmediato},
			conUbicac: true,
			contiene:  []string{"color=AMARILLO", "cantidad=2", "FALTA: nada"},
		},
		{
			nombre:    "programación sin hora: la hora está en FALTA",
			ficha:     conversation.PedidoEnCurso{Color: "BLANCO", Cantidad: 1, Flujo: conversation.FlujoProgramacion},
			conUbicac: true,
			contiene:  []string{"flujo=programacion", "hora de la entrega"},
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			store := conversation.NewMemStore()
			store.SetPedidoEnCurso(phone, c.ficha)
			if c.conUbicac {
				store.SetLocation(phone, -2.9, -79.0)
			}
			a := agentDePrueba(nil, store)
			_, vol := a.construirSistema(phone)

			for _, quiero := range c.contiene {
				if !strings.Contains(vol, quiero) {
					t.Errorf("el prompt no menciona %q:\n%s", quiero, vol)
				}
			}
			for _, noQuiero := range c.noContiene {
				if strings.Contains(vol, noQuiero) {
					t.Errorf("el prompt no debía contener %q:\n%s", noQuiero, vol)
				}
			}
		})
	}
}

// Sin ficha no se inyecta el bloque: un cliente que solo saluda no tiene "pedido en curso".
func TestPromptSinFichaNoMuestraBloque(t *testing.T) {
	a := agentDePrueba(nil, conversation.NewMemStore())
	if _, vol := a.construirSistema("593999000078"); strings.Contains(vol, "PEDIDO EN CURSO") {
		t.Errorf("se inyectó PEDIDO EN CURSO sin ficha:\n%s", vol)
	}
}

// El bloque COBERTURA sale de las zonas reales del backend. Sin zonas, el modelo tiene
// PROHIBIDO afirmar o negar cobertura: pide la ubicación y el sistema verifica.
//
// Desde el 11/09 tampoco lleva la lista completa de parroquias: dársela lo hacía "buscar" ahí
// el barrio del cliente y deducir que no había cobertura cuando no lo encontraba (caso Israel,
// ver cobertura_test.go). Solo van la zona y un par de ejemplos.
func TestRenderCobertura(t *testing.T) {
	zonas := []georoutes.ZonaCobertura{{
		Zona:       "AZUAY",
		Parroquias: []string{"BANOS", "BELLAVISTA", "CAÑARIBAMBA", "SININCAY", "EL VALE"},
	}}
	texto := renderCobertura(zonas)
	for _, quiero := range []string{"AZUAY", "BANOS", "NUNCA respondas que NO llegamos"} {
		if !strings.Contains(texto, quiero) {
			t.Errorf("el bloque de cobertura no menciona %q:\n%s", quiero, texto)
		}
	}

	vacio := renderCobertura(nil)
	if !strings.Contains(vacio, "NO afirmes ni niegues") {
		t.Errorf("sin zonas, el modelo debe tener prohibido inventar cobertura:\n%s", vacio)
	}
	if strings.Contains(vacio, "AZUAY") {
		t.Error("sin zonas del backend no puede aparecer ninguna zona")
	}
}

// fechaEnEspanol debe dar el día de la semana y el mes correctos en español: es lo que evita
// que el modelo invente el día (09/09 dijo "sábado" un miércoles).
func TestFechaEnEspanol(t *testing.T) {
	// Miércoles 9 de septiembre de 2026, en zona de Ecuador.
	d := time.Date(2026, 9, 9, 7, 32, 0, 0, zonaEcuador)
	got := fechaEnEspanol(d)
	if got != "miércoles 9 de septiembre de 2026" {
		t.Errorf("fecha mal formada: %q", got)
	}
	// Un domingo (índice 0 del arreglo de días) y diciembre (índice 11 de meses): los extremos.
	dom := time.Date(2026, 12, 6, 10, 0, 0, 0, zonaEcuador)
	if got := fechaEnEspanol(dom); got != "domingo 6 de diciembre de 2026" {
		t.Errorf("los extremos del mapa fallan: %q", got)
	}
}

// El nombre de WhatsApp tiene que llegarle al modelo AUNQUE el cliente no esté registrado: es
// justo el caso de Guillermo Pacheco (10/09), que aún no tenía cédula cuando el bot lo saludó
// "Brito" tomando esa palabra del texto de su pedido.
func TestPromptLlevaElNombreDeWhatsApp(t *testing.T) {
	const from = "593963646872"
	store := conversation.NewMemStore()
	store.SetPerfilWhatsApp(from, "Guillermo Pacheco")
	ag := agentDePrueba(nil, store)

	_, vol := ag.construirSistema(from)
	if !strings.Contains(vol, "Guillermo Pacheco") {
		t.Errorf("el nombre de WhatsApp no llegó al prompt:\n%s", vol)
	}
	if !strings.Contains(vol, "NUNCA deduzcas su nombre") {
		t.Errorf("falta la regla que impide deducir el nombre del mensaje:\n%s", vol)
	}
}
