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
);

-- Datos personales del cliente (cédula, nombre, correo). Durables: se guardan tras el
-- primer pedido para no volver a pedírselos en pedidos siguientes.
CREATE TABLE IF NOT EXISTS profiles (
    phone          TEXT    PRIMARY KEY,
    identificacion TEXT,
    nombres        TEXT,
    correo         TEXT,
    updated_at     INTEGER NOT NULL
);

-- Último pedido exitoso del cliente. Durable: se guarda tras cada pedido para ofrecerle
-- repetir lo mismo cuando vuelve (producto, color, cantidad y fecha legible).
CREATE TABLE IF NOT EXISTS last_orders (
    phone      TEXT    PRIMARY KEY,
    producto   TEXT,
    color      TEXT,
    cantidad   INTEGER,
    fecha      TEXT,
    updated_at INTEGER NOT NULL
);

-- Marca de última actividad del cliente (unix). Para delimitar sesiones por inactividad.
CREATE TABLE IF NOT EXISTS activity (
    phone      TEXT    PRIMARY KEY,
    last_at    INTEGER NOT NULL
);

-- Pedido en pausa a la espera de verificación OTP. Transitorio: se guarda cuando el bot
-- pide el código y se limpia al validarlo (o al concretar/derivar).
CREATE TABLE IF NOT EXISTS order_drafts (
    phone      TEXT    PRIMARY KEY,
    color      TEXT,
    cantidad   INTEGER,
    updated_at INTEGER NOT NULL
);

-- Cuentas pendientes de verificación OTP. Transitorias: se guardan cuando el bot
-- solicita el código de verificación y se limpian al validarlo.
CREATE TABLE IF NOT EXISTS pending_verif (
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

func (s *sqliteStore) ClearHistory(phone string) {
	if _, err := s.db.Exec(`DELETE FROM turns WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearHistory %s: %v", phone, err)
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

func (s *sqliteStore) SetProfile(phone string, profile Profile) {
	if _, err := s.db.Exec(`
        INSERT INTO profiles(phone, identificacion, nombres, correo, updated_at)
        VALUES(?, ?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            identificacion=excluded.identificacion,
            nombres=excluded.nombres,
            correo=excluded.correo,
            updated_at=excluded.updated_at`,
		phone, profile.Identificacion, profile.Nombres, profile.Correo, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetProfile %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetProfile(phone string) (Profile, bool) {
	var profile Profile
	err := s.db.QueryRow(
		`SELECT identificacion, nombres, correo FROM profiles WHERE phone = ?`, phone).
		Scan(&profile.Identificacion, &profile.Nombres, &profile.Correo)
	if err == sql.ErrNoRows {
		return Profile{}, false
	}
	return profile, true
}

func (s *sqliteStore) SetLastOrder(phone string, order LastOrder) {
	if _, err := s.db.Exec(`
        INSERT INTO last_orders(phone, producto, color, cantidad, fecha, updated_at)
        VALUES(?, ?, ?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            producto=excluded.producto,
            color=excluded.color,
            cantidad=excluded.cantidad,
            fecha=excluded.fecha,
            updated_at=excluded.updated_at`,
		phone, order.Producto, order.Color, order.Cantidad, order.Fecha, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetLastOrder %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetLastOrder(phone string) (LastOrder, bool) {
	var order LastOrder
	err := s.db.QueryRow(
		`SELECT producto, color, cantidad, fecha FROM last_orders WHERE phone = ?`, phone).
		Scan(&order.Producto, &order.Color, &order.Cantidad, &order.Fecha)
	if err == sql.ErrNoRows {
		return LastOrder{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetLastOrder %s: %v", phone, err)
		return LastOrder{}, false
	}
	return order, true
}

func (s *sqliteStore) LastActivity(phone string) (time.Time, bool) {
	var unix int64
	err := s.db.QueryRow(`SELECT last_at FROM activity WHERE phone = ?`, phone).Scan(&unix)
	if err == sql.ErrNoRows {
		return time.Time{}, false
	}
	if err != nil {
		log.Printf("[sqlite] LastActivity %s: %v", phone, err)
		return time.Time{}, false
	}
	return time.Unix(unix, 0), true
}

func (s *sqliteStore) TouchActivity(phone string) {
	if _, err := s.db.Exec(`
        INSERT INTO activity(phone, last_at) VALUES(?, ?)
        ON CONFLICT(phone) DO UPDATE SET last_at=excluded.last_at`,
		phone, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] TouchActivity %s: %v", phone, err)
	}
}

func (s *sqliteStore) SetOrderDraft(phone string, draft OrderDraft) {
	if _, err := s.db.Exec(`
        INSERT INTO order_drafts(phone, color, cantidad, updated_at) VALUES(?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            color=excluded.color, cantidad=excluded.cantidad, updated_at=excluded.updated_at`,
		phone, draft.Color, draft.Cantidad, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetOrderDraft %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetOrderDraft(phone string) (OrderDraft, bool) {
	var draft OrderDraft
	err := s.db.QueryRow(
		`SELECT color, cantidad FROM order_drafts WHERE phone = ?`, phone).
		Scan(&draft.Color, &draft.Cantidad)
	if err == sql.ErrNoRows {
		return OrderDraft{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetOrderDraft %s: %v", phone, err)
		return OrderDraft{}, false
	}
	return draft, true
}

func (s *sqliteStore) ClearOrderDraft(phone string) {
	if _, err := s.db.Exec(`DELETE FROM order_drafts WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearOrderDraft %s: %v", phone, err)
	}
}

func (s *sqliteStore) SetPendingVerification(phone string, account Account) {
	if _, err := s.db.Exec(`
        INSERT INTO pending_verif(phone, username, password, user_id, jwt, refresh, created_at)
        VALUES(?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            username=excluded.username,
            password=excluded.password,
            user_id=excluded.user_id,
            jwt=excluded.jwt,
            refresh=excluded.refresh`,
		phone, account.Username, account.Password, account.UserID, account.JWT, account.Refresh, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetPendingVerification %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetPendingVerification(phone string) (Account, bool) {
	var account Account
	err := s.db.QueryRow(
		`SELECT username, password, user_id, jwt, refresh FROM pending_verif WHERE phone = ?`, phone).
		Scan(&account.Username, &account.Password, &account.UserID, &account.JWT, &account.Refresh)
	if err == sql.ErrNoRows {
		return Account{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetPendingVerification %s: %v", phone, err)
		return Account{}, false
	}
	return account, true
}

func (s *sqliteStore) ClearPendingVerification(phone string) {
	if _, err := s.db.Exec(`DELETE FROM pending_verif WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearPendingVerification %s: %v", phone, err)
	}
}

