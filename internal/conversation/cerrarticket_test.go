// CERRAR UN TICKET EXIGE DECIR QUÉ SE HIZO.
//
// El 26/09 la tabla `tickets` de producción tenía 55 filas: 53 abiertas y 2 cerradas — y las dos
// cerradas SIN nada escrito en `solucion`. De esos dos casos no quedó ni rastro de cómo se
// resolvieron, que es justamente lo que sirve la próxima vez.
//
// Ver specs/tickets-que-nadie-cierra.md.
package conversation

import "testing"

func TestNoSePuedeCerrarUnTicketSinDecirQueSeHizo(t *testing.T) {
	// Los dos backends tienen que comportarse IGUAL: si el de memoria deja pasar lo que sqlite
	// rechaza, los tests mienten sobre lo que hace producción.
	backends := map[string]Store{
		"memoria": NewMemStore(),
	}
	if s, err := NewSQLiteStore(t.TempDir()+"/t.db", 30); err == nil {
		backends["sqlite"] = s
	} else {
		t.Logf("sqlite no disponible en este entorno: %v", err)
	}

	for nombre, store := range backends {
		t.Run(nombre, func(t *testing.T) {
			id := store.CreateTicket("593987295556", "Cliente solicita hablar con una persona", "…")
			if id <= 0 {
				t.Fatal("no se pudo crear el ticket de prueba")
			}

			// Vacío, espacios o saltos de línea: nada de eso dice qué se hizo.
			for _, enBlanco := range []string{"", "   ", "\n", "\t  \n"} {
				if store.CloseTicket(id, enBlanco) {
					t.Errorf("se cerró el ticket con solucion=%q", enBlanco)
				}
			}
			// Y sigue abierto, porque nadie explicó nada.
			if abiertos := store.ListTickets("abierto", 10); len(abiertos) != 1 {
				t.Fatalf("el ticket debía seguir abierto; abiertos=%d", len(abiertos))
			}

			// Con la solución escrita, sí se cierra.
			if !store.CloseTicket(id, "Se le llamó y se le tomó el pedido a mano (#301)") {
				t.Fatal("un ticket CON solución debe poder cerrarse")
			}
			if abiertos := store.ListTickets("abierto", 10); len(abiertos) != 0 {
				t.Errorf("el ticket sigue abierto tras cerrarlo; abiertos=%d", len(abiertos))
			}
		})
	}
}
