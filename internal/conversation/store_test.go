package conversation

import (
	"path/filepath"
	"testing"
	"time"
)

// --- Ficha del pedido en curso ---

// La ficha guarda lo que el cliente fue eligiendo y se puede limpiar. Se prueba contra los DOS
// backends: si sqlite y memoria divergen, el bot se comporta distinto en dev y en producción.
func TestPedidoEnCurso(t *testing.T) {
	backends := map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}

	for nombre, nuevo := range backends {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123456"

			if _, ok := s.GetPedidoEnCurso(phone); ok {
				t.Fatal("un cliente nuevo no puede tener ficha")
			}

			s.SetPedidoEnCurso(phone, PedidoEnCurso{Color: "BLANCO", Cantidad: 2, Flujo: FlujoInmediato})
			p, ok := s.GetPedidoEnCurso(phone)
			if !ok || p.Color != "BLANCO" || p.Cantidad != 2 || p.Flujo != FlujoInmediato {
				t.Fatalf("la ficha no se guardó: %+v (ok=%v)", p, ok)
			}

			// Se sobreescribe entera: el cliente cambió de opinión y pasó a programar.
			s.SetPedidoEnCurso(phone, PedidoEnCurso{Color: "AMARILLO", Cantidad: 1, Hora: "18:30", Flujo: FlujoProgramacion})
			p, _ = s.GetPedidoEnCurso(phone)
			if p.Color != "AMARILLO" || p.Cantidad != 1 || p.Hora != "18:30" || p.Flujo != FlujoProgramacion {
				t.Fatalf("no se actualizó la ficha: %+v", p)
			}

			s.ClearPedidoEnCurso(phone)
			if _, ok := s.GetPedidoEnCurso(phone); ok {
				t.Error("la ficha sobrevivió al Clear: un mensaje posterior podría re-registrar el pedido")
			}
		})
	}
}

// Una ficha sin datos no aporta nada al prompt ni a los candados.
func TestPedidoEnCursoVacio(t *testing.T) {
	if !(PedidoEnCurso{}).Vacio() {
		t.Error("una ficha sin datos debe ser Vacio()")
	}
	if (PedidoEnCurso{Color: "BLANCO"}).Vacio() {
		t.Error("una ficha con color NO está vacía")
	}
	if (PedidoEnCurso{Hora: "18:30"}).Vacio() {
		t.Error("una ficha con hora NO está vacía")
	}
}

// La ficha caduca con la sesión: si el cliente vuelve 25 horas después, lo que estaba
// eligiendo ya no vale. Se prueba en los DOS backends porque lo implementan distinto (mem
// borra al leer; sqlite purga con un DELETE global) y una divergencia haría que el bot se
// comportara distinto en desarrollo y en producción.
func TestPedidoEnCursoCaducaConLaSesion(t *testing.T) {
	backends := map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}
	for nombre, nuevo := range backends {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123499"
			s.SetPedidoEnCurso(phone, PedidoEnCurso{Color: "BLANCO", Cantidad: 2, Flujo: FlujoInmediato})

			// Recién guardada: vale.
			if _, ok := s.GetPedidoEnCurso(phone); !ok {
				t.Fatal("la ficha recién guardada no se encontró")
			}

			// Envejecida más allá de la sesión: ya no.
			envejecerPedidoEnCurso(t, s, phone, SessionGap+time.Hour)
			if p, ok := s.GetPedidoEnCurso(phone); ok {
				t.Errorf("una ficha de hace más de %s sigue viva (%+v): el bot retomaría un "+
					"pedido de otra conversación", SessionGap, p)
			}
		})
	}
}

// envejecerPedidoEnCurso simula el paso del tiempo sobre la ficha, en cada backend.
func envejecerPedidoEnCurso(t *testing.T, s Store, phone string, edad time.Duration) {
	t.Helper()
	switch st := s.(type) {
	case *memStore:
		st.mu.Lock()
		p := st.pedidoEnCurso[phone]
		p.UpdatedAt = time.Now().Add(-edad)
		st.pedidoEnCurso[phone] = p
		st.mu.Unlock()
	case *sqliteStore:
		if _, err := st.db.Exec(`UPDATE pedido_en_curso SET updated_at = ? WHERE phone = ?`,
			time.Now().Add(-edad).Unix(), phone); err != nil {
			t.Fatalf("envejecer la ficha en sqlite: %v", err)
		}
	default:
		t.Fatalf("backend desconocido: %T", s)
	}
}

// El nombre de WhatsApp se guarda en CADA mensaje, así que no puede pisar la cédula ni el
// nombre legal del cliente (que es el que va al backend con el pedido). Se prueba contra los
// dos backends: si divergen, el bot se comporta distinto en dev y en producción.
func TestPerfilWhatsAppNoPisaLosDatosDelCliente(t *testing.T) {
	backends := map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}

	for nombre, nuevo := range backends {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593963646872"

			// Llega un mensaje antes de que el cliente dé ningún dato.
			s.SetPerfilWhatsApp(phone, "Guillermo Pacheco")
			p, ok := s.GetProfile(phone)
			if !ok || p.PerfilWhatsApp != "Guillermo Pacheco" {
				t.Fatalf("no se guardó el nombre de WhatsApp: ok=%v perfil=%+v", ok, p)
			}

			// Se registra con su cédula y nombre legal.
			s.SetProfile(phone, Profile{Identificacion: "0152648176", Nombres: "Guillermo Pacheco",
				PerfilWhatsApp: "Guillermo Pacheco"})

			// Sigue conversando: cada mensaje reescribe el nombre de WhatsApp.
			s.SetPerfilWhatsApp(phone, "Guille 🔥")
			p, ok = s.GetProfile(phone)
			if !ok {
				t.Fatal("se perdió el perfil")
			}
			if p.Identificacion != "0152648176" {
				t.Errorf("se borró la cédula del cliente: %q", p.Identificacion)
			}
			if p.Nombres != "Guillermo Pacheco" {
				t.Errorf("se pisó el nombre legal que va al backend: %q", p.Nombres)
			}
			if p.PerfilWhatsApp != "Guille 🔥" {
				t.Errorf("no se actualizó el nombre de WhatsApp: %q", p.PerfilWhatsApp)
			}
		})
	}
}
