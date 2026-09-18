package conversation

import (
	"path/filepath"
	"testing"
)

// EL CONSENTIMIENTO SOBREVIVE A UN REINICIO.
//
// Es la prueba de que un cliente autorizó (o negó) el tratamiento de sus datos, así que no puede
// vivir en memoria como los demás estados "pendientes": si se perdiera al reiniciar el bot, a
// quien ya respondió se le volvería a preguntar, y —peor— alguien que negó el permiso volvería a
// entrar al flujo como si nunca hubiera dicho nada.
//
// Se prueba contra los DOS backends: si sqlite y memoria divergen, el bot se comporta distinto en
// dev y en producción, que es justo donde no se puede descubrir un fallo así.
func backendsDeConsentimiento(t *testing.T) map[string]func() Store {
	t.Helper()
	return map[string]func() Store{
		"mem": func() Store { return NewMemStore() },
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}
}

func TestConsentimiento(t *testing.T) {
	for nombre, nuevo := range backendsDeConsentimiento(t) {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123456"

			// LOS TRES ESTADOS SON DISTINTOS. "No ha respondido" no puede confundirse con "dijo
			// no": al primero hay que preguntarle, al segundo no, y tratarlo como negativa lo
			// dejaría bloqueado sin haber dicho nada.
			if _, ok := s.GetConsentimiento(phone); ok {
				t.Fatal("un cliente nuevo no puede tener consentimiento registrado")
			}

			s.SetConsentimiento(phone, Consentimiento{Acepta: true})
			c, ok := s.GetConsentimiento(phone)
			if !ok || !c.Acepta {
				t.Fatalf("la aceptación no se guardó: %+v (ok=%v)", c, ok)
			}
			if c.Fecha.IsZero() {
				t.Error("se guardó sin fecha, y la fecha es parte del registro legal")
			}
			if c.Sincronizado {
				t.Error("nació marcado como sincronizado sin haberse enviado al backend")
			}

			// La negativa se guarda como tal, no como ausencia.
			s.SetConsentimiento(phone, Consentimiento{Acepta: false})
			c, ok = s.GetConsentimiento(phone)
			if !ok {
				t.Fatal("la negativa desapareció: sería indistinguible de no haber respondido")
			}
			if c.Acepta {
				t.Error("la negativa se guardó como aceptación")
			}

			// Y la marca de sincronización se conserva.
			s.SetConsentimiento(phone, Consentimiento{Acepta: true, Sincronizado: true})
			if c, _ := s.GetConsentimiento(phone); !c.Sincronizado {
				t.Error("no se conservó la marca de sincronizado: se reenviaría en cada pedido")
			}
		})
	}
}

// La ESPERA del menú también es durable: el bot puede reiniciarse entre el menú y el "Sí". Sin
// esto, esa respuesta se iría al modelo en vez de resolverse en código.
func TestConsentimientoPendiente(t *testing.T) {
	for nombre, nuevo := range backendsDeConsentimiento(t) {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123457"

			if s.ConsentimientoPendiente(phone) {
				t.Fatal("un cliente nuevo no puede tener el menú pendiente")
			}
			s.SetConsentimientoPendiente(phone)
			if !s.ConsentimientoPendiente(phone) {
				t.Fatal("no se registró la espera del menú")
			}
			s.ClearConsentimientoPendiente(phone)
			if s.ConsentimientoPendiente(phone) {
				t.Error("la espera siguió en pie tras limpiarla: un 'no' posterior en una " +
					"conversación normal se leería como negativa de consentimiento")
			}
		})
	}
}

// Un cliente con el menú pendiente NO tiene consentimiento todavía: son dos cosas separadas a
// propósito. Si fueran una sola columna (acepta = NULL), un NULL leído como 0 marcaría como
// NEGADO a quien no ha dicho nada, y quedaría bloqueado para siempre.
func TestLaEsperaYLaRespuestaSonIndependientes(t *testing.T) {
	for nombre, nuevo := range backendsDeConsentimiento(t) {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123458"

			s.SetConsentimientoPendiente(phone)
			if _, ok := s.GetConsentimiento(phone); ok {
				t.Error("mandar el menú dejó registrada una respuesta que el cliente no ha dado")
			}
		})
	}
}

// Y sobre todo: el consentimiento NO se va con la limpieza del historial. La conversación arranca
// de cero, pero a quien ya respondió no se le vuelve a preguntar.
func TestElConsentimientoNoSeBorraConElHistorial(t *testing.T) {
	for nombre, nuevo := range backendsDeConsentimiento(t) {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			const phone = "593999123459"

			s.SetConsentimiento(phone, Consentimiento{Acepta: false})
			s.AppendUser(phone, "quiero gas")
			s.ClearHistory(phone)

			c, ok := s.GetConsentimiento(phone)
			if !ok {
				t.Fatal("limpiar el historial se llevó el consentimiento: al cliente que negó el " +
					"permiso se le volvería a preguntar, y volvería a entrar al flujo")
			}
			if c.Acepta {
				t.Error("la negativa se convirtió en aceptación al limpiar el historial")
			}
		})
	}
}
