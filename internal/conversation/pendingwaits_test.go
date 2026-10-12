package conversation

import (
	"path/filepath"
	"testing"
	"time"
)

// Al arrancar se retoman las esperas recientes; una que nadie tocó en horas ya no (su búsqueda
// se cerró hace rato y avisarle ahora solo confunde).
func TestPendingWaitsSoloLasRecientes(t *testing.T) {
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	s := st.(*sqliteStore)
	s.SetPendingWait("593900000001", PendingWait{IDColor: 2, Cantidad: 1, IDBusqueda: 85})
	s.SetPendingWait("593900000002", PendingWait{IDColor: 1, Cantidad: 1})
	if _, err := s.db.Exec(`UPDATE pending_wait SET created_at = ? WHERE phone = ?`,
		time.Now().Add(-3*time.Hour).Unix(), "593900000002"); err != nil {
		t.Fatal(err)
	}
	got := s.PendingWaits(2 * time.Hour)
	if len(got) != 1 || got["593900000001"].IDBusqueda != 85 {
		t.Errorf("se esperaba solo la espera reciente con su búsqueda: %+v", got)
	}
}

func TestPendingWaitsEnMemoria(t *testing.T) {
	s := NewMemStore()
	s.SetPendingWait("593900000001", PendingWait{IDBusqueda: 85})
	if got := s.PendingWaits(time.Hour); len(got) != 1 {
		t.Errorf("en memoria se esperaba 1 espera: %+v", got)
	}
}
