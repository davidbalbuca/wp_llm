package conversation

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // driver SQLite en Go puro (sin CGO)
	"google.golang.org/genai"
)

// convTTL es cuánto se conserva una conversación inactiva. Al leer se ignoran los turnos
// más viejos y una limpieza perezosa los borra en cada escritura.
const convTTL = 24 * time.Hour

// sqliteStore implementa Store sobre un archivo SQLite. El estado persiste en disco
// (sobrevive reinicios) sin necesidad de un servidor aparte. Apto para una instancia;
// para varias máquinas compartiendo estado haría falta Redis o Postgres.
type sqliteStore struct {
	db *sql.DB
}

// NewSQLiteStore abre (o crea) la base en dbPath, activa WAL y prepara el esquema.
func NewSQLiteStore(dbPath string) (Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	// _busy_timeout evita errores "database is locked" bajo escrituras concurrentes.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	schema := `
CREATE TABLE IF NOT EXISTS turns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    phone      TEXT    NOT NULL,
    role       TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_turns_phone ON turns(phone, id);

CREATE TABLE IF NOT EXISTS locations (
    phone      TEXT    PRIMARY KEY,
    latitude   REAL    NOT NULL,
    longitude  REAL    NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Credenciales y tokens georoutes por cliente. Son durables (no expiran como el historial):
-- la cuenta del cliente en el backend es permanente. Los JWT se cachean para reutilización.
CREATE TABLE IF NOT EXISTS accounts (
    phone      TEXT    PRIMARY KEY,
    username   TEXT    NOT NULL,
    password   TEXT    NOT NULL,
    user_id    INTEGER,
    jwt        TEXT,
    refresh    TEXT,
    created_at INTEGER NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) History(phone string) []*genai.Content {
	minTime := time.Now().Add(-convTTL).Unix()
	// Últimos maxTurns turnos dentro del TTL, devueltos en orden cronológico.
	rows, err := s.db.Query(`
        SELECT role, content FROM (
            SELECT id, role, content FROM turns
            WHERE phone = ? AND created_at >= ?
            ORDER BY id DESC LIMIT ?
        ) ORDER BY id ASC`, phone, minTime, maxTurns)
	if err != nil {
		log.Printf("[sqlite] History %s: %v", phone, err)
		return nil
	}
	defer rows.Close()

	var out []*genai.Content
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			log.Printf("[sqlite] scan turno: %v", err)
			continue
		}
		out = append(out, turn{Role: role, Text: content}.toContent())
	}
	return out
}

func (s *sqliteStore) appendTurn(phone, role, text string) {
	now := time.Now().Unix()
	if _, err := s.db.Exec(
		`INSERT INTO turns(phone, role, content, created_at) VALUES(?, ?, ?, ?)`,
		phone, role, text, now); err != nil {
		log.Printf("[sqlite] insert turno %s: %v", phone, err)
		return
	}
	// Limpieza perezosa: borra turnos vencidos y los que exceden maxTurns para este cliente.
	minTime := time.Now().Add(-convTTL).Unix()
	if _, err := s.db.Exec(`
        DELETE FROM turns
        WHERE phone = ? AND (
            created_at < ?
            OR id NOT IN (SELECT id FROM turns WHERE phone = ? ORDER BY id DESC LIMIT ?)
        )`, phone, minTime, phone, maxTurns); err != nil {
		log.Printf("[sqlite] limpieza turnos %s: %v", phone, err)
	}
}

func (s *sqliteStore) AppendUser(phone, text string)  { s.appendTurn(phone, "user", text) }
func (s *sqliteStore) AppendModel(phone, text string) { s.appendTurn(phone, "model", text) }

func (s *sqliteStore) SetLocation(phone string, lat, lng float64) {
	if _, err := s.db.Exec(`
        INSERT INTO locations(phone, latitude, longitude, updated_at) VALUES(?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET latitude=excluded.latitude,
            longitude=excluded.longitude, updated_at=excluded.updated_at`,
		phone, lat, lng, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetLocation %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetLocation(phone string) (Location, bool) {
	minTime := time.Now().Add(-convTTL).Unix()
	var loc Location
	err := s.db.QueryRow(
		`SELECT latitude, longitude FROM locations WHERE phone = ? AND updated_at >= ?`,
		phone, minTime).Scan(&loc.Latitude, &loc.Longitude)
	if err == sql.ErrNoRows {
		return Location{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetLocation %s: %v", phone, err)
		return Location{}, false
	}
	return loc, true
}

func (s *sqliteStore) SetAccount(phone string, account Account) {
	if _, err := s.db.Exec(`
        INSERT INTO accounts(phone, username, password, user_id, jwt, refresh, created_at)
        VALUES(?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            username=excluded.username,
            password=excluded.password,
            user_id=excluded.user_id,
            jwt=excluded.jwt,
            refresh=excluded.refresh`,
		phone, account.Username, account.Password, account.UserID, account.JWT, account.Refresh, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetAccount %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetAccount(phone string) (Account, bool) {
	var account Account
	err := s.db.QueryRow(
		`SELECT username, password, user_id, jwt, refresh FROM accounts WHERE phone = ?`, phone).
		Scan(&account.Username, &account.Password, &account.UserID, &account.JWT, &account.Refresh)
	if err == sql.ErrNoRows {
		return Account{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetAccount %s: %v", phone, err)
		return Account{}, false
	}
	return account, true
}
