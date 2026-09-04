package conversation

import (
	"path/filepath"
	"testing"
	"time"
)

// El bug de prod (04/09): MarcarAvisoInicio hace INSERT con thread_id=0 para registrar el aviso
// antes de que exista el hilo. Si GetTelegramThread devolvía true para esa fila envenenada,
// hiloDe creía que el hilo ya estaba creado y mandaba todo al General en vez de al hilo del
// cliente. GetTelegramThread debe devolver ok=false mientras el thread_id sea 0.
func TestTelegramThreadNoEnvenenadoPorAvisoInicio(t *testing.T) {
	// Se prueba contra SQLite (donde estaba el bug) y contra memoria, para que ambos backends
	// respeten el mismo invariante.
	stores := map[string]func() Store{
		"sqlite": func() Store {
			s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
		"mem": func() Store { return NewMemStore() },
	}

	for nombre, nuevo := range stores {
		t.Run(nombre, func(t *testing.T) {
			s := nuevo()
			phone := "593999888777"

			// 1) Llega el cliente: AvisarInicio marca el aviso, creando la fila con thread_id=0.
			if !s.MarcarAvisoInicio(phone, time.Hour) {
				t.Fatal("el primer aviso de la sesión debía pasar")
			}

			// 2) hiloDe consulta el hilo: NO debe existir todavía (thread_id=0 no es un hilo real).
			if id, ok := s.GetTelegramThread(phone); ok {
				t.Fatalf("con thread_id=0 debía devolver ok=false, devolvió id=%d ok=true", id)
			}

			// 3) Se crea el hilo real y se guarda.
			s.SetTelegramThread(phone, 42)

			// 4) Ahora sí existe y se reutiliza.
			id, ok := s.GetTelegramThread(phone)
			if !ok || id != 42 {
				t.Fatalf("tras crear el hilo debía devolver (42, true), devolvió (%d, %v)", id, ok)
			}
		})
	}
}
