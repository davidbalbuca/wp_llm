package conversation

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/genai"
	_ "modernc.org/sqlite" // driver SQLite en Go puro (sin CGO)
)

// convTTL es cuánto se conserva una conversación inactiva. Al leer se ignoran los turnos
// más viejos y una limpieza perezosa los borra en cada escritura.
const convTTL = 24 * time.Hour

// sqliteStore implementa Store sobre un archivo SQLite. El estado persiste en disco
// (sobrevive reinicios) sin necesidad de un servidor aparte. Apto para una instancia;
// para varias máquinas compartiendo estado haría falta Redis o Postgres.
type sqliteStore struct {
	db       *sql.DB
	auditTTL time.Duration // retención del message_log (auditoría de conversaciones)
}

// NewSQLiteStore abre (o crea) la base en dbPath, activa WAL y prepara el esquema. `auditDays`
// es cuántos días se conserva el registro de auditoría (message_log).
func NewSQLiteStore(dbPath string, auditDays int) (Store, error) {
	if auditDays <= 0 {
		auditDays = 15
	}
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
);

-- Pedidos entregados pendientes de calificación del conductor. Transitorios: se guardan
-- cuando el backend avisa que el conductor finalizó y se limpian cuando el cliente califica.
CREATE TABLE IF NOT EXISTS pending_rating (
    phone      TEXT    PRIMARY KEY,
    pedido_id  INTEGER NOT NULL,
    conductor  TEXT,
    created_at INTEGER NOT NULL
);

-- Mapa pedido_id -> teléfono de WhatsApp con el que se hizo el pedido. Sirve para contactar
-- al cliente por el número correcto cuando el backend avisa que se entregó (calificación).
CREATE TABLE IF NOT EXISTS order_phone (
    pedido_id  INTEGER PRIMARY KEY,
    phone      TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
-- Pedido ACTIVO por teléfono (el último creado). Sirve para cancelarlo si el cliente lo pide.
CREATE TABLE IF NOT EXISTS active_pedido (
    phone      TEXT    PRIMARY KEY,
    pedido_id  INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
-- Pedido esperando conductor (el cliente eligió esperar ~5 min). Se guarda como JSON para
-- reintentar la asignación; se limpia al asignar, cancelar o expirar.
CREATE TABLE IF NOT EXISTS pending_wait (
    phone      TEXT    PRIMARY KEY,
    data       TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

-- Registro de AUDITORÍA de conversaciones (durable). Guarda TODO lo dicho en un chat (mensajes del
-- cliente, del bot, notificaciones del sistema y respuestas manuales). NO lo borra ClearHistory; se
-- purga solo por retención (AUDIT_LOG_DAYS). Es aparte del historial del bot (tabla turns).
CREATE TABLE IF NOT EXISTS message_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    phone      TEXT    NOT NULL,
    role       TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_msglog_phone ON message_log(phone, id);
CREATE INDEX IF NOT EXISTS idx_msglog_created ON message_log(created_at);

-- Control del chat: modo "bot" (responde el bot) o "human" (control manual desde la web).
CREATE TABLE IF NOT EXISTS chat_control (
    phone      TEXT    PRIMARY KEY,
    mode       TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Entregas PROGRAMADAS (cliente escribió fuera de horario y agendó una hora). El scheduler
-- las revisa y a la hora propuesta escribe al cliente para confirmar el pedido.
CREATE TABLE IF NOT EXISTS scheduled_orders (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    phone           TEXT    NOT NULL,
    identificacion  TEXT,
    nombres         TEXT,
    idcategoria     INTEGER,
    idproducto      INTEGER,
    idcolor         INTEGER,
    cantidad        INTEGER,
    idtipopago      INTEGER,
    producto_nombre TEXT,
    color_nombre    TEXT,
    latitude        REAL,
    longitude       REAL,
    hora_propuesta  INTEGER NOT NULL,
    estado          TEXT    NOT NULL DEFAULT 'pendiente',
    confirm_sent_at INTEGER,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sched_estado ON scheduled_orders(estado, hora_propuesta);

-- Tickets de SOPORTE (escalaciones del bot). Durables: NO se purgan (histórico de casos).
CREATE TABLE IF NOT EXISTS tickets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    phone      TEXT    NOT NULL,
    motivo     TEXT,
    resumen    TEXT,
    estado     TEXT    NOT NULL DEFAULT 'abierto',
    solucion   TEXT,
    created_at INTEGER NOT NULL,
    closed_at  INTEGER
);
CREATE INDEX IF NOT EXISTS idx_tickets_estado ON tickets(estado, id);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteStore{db: db, auditTTL: time.Duration(auditDays) * 24 * time.Hour}, nil
}

// LogMessage añade una línea al registro de auditoría y purga (perezosamente) lo más viejo que la
// retención. Roles: "user" (cliente), "model" (bot), "system" (notificación), "human" (manual).
func (s *sqliteStore) LogMessage(phone, role, content string) {
	if _, err := s.db.Exec(
		`INSERT INTO message_log(phone, role, content, created_at) VALUES(?, ?, ?, ?)`,
		phone, role, content, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] LogMessage %s: %v", phone, err)
		return
	}
	minTime := time.Now().Add(-s.auditTTL).Unix()
	if _, err := s.db.Exec(`DELETE FROM message_log WHERE created_at < ?`, minTime); err != nil {
		log.Printf("[sqlite] purga message_log: %v", err)
	}
}

// GetConversation devuelve las últimas `limit` líneas de auditoría de un chat, en orden cronológico.
func (s *sqliteStore) GetConversation(phone string, limit int) []LoggedMessage {
	if limit <= 0 {
		limit = 300
	}
	rows, err := s.db.Query(`
        SELECT role, content, created_at FROM (
            SELECT id, role, content, created_at FROM message_log
            WHERE phone = ? ORDER BY id DESC LIMIT ?
        ) ORDER BY id ASC`, phone, limit)
	if err != nil {
		log.Printf("[sqlite] GetConversation %s: %v", phone, err)
		return nil
	}
	defer rows.Close()
	var out []LoggedMessage
	for rows.Next() {
		var m LoggedMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ListConversations lista los chats con actividad reciente (último mensaje) + su modo.
func (s *sqliteStore) ListConversations(limit int) []ConversationSummary {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
        SELECT m.phone, m.content, m.created_at FROM message_log m
        JOIN (SELECT phone, MAX(id) AS mid FROM message_log GROUP BY phone) t ON t.mid = m.id
        ORDER BY m.created_at DESC LIMIT ?`, limit)
	if err != nil {
		log.Printf("[sqlite] ListConversations: %v", err)
		return nil
	}
	var out []ConversationSummary
	for rows.Next() {
		var c ConversationSummary
		if err := rows.Scan(&c.Phone, &c.LastMessage, &c.LastAt); err != nil {
			continue
		}
		out = append(out, c)
	}
	rows.Close()
	// El modo y las banderas se consultan aparte (evita anidar queries mientras se itera el
	// cursor). Son consultas por indice sobre tablas chicas: no pesan.
	for i := range out {
		out[i].Mode = s.GetChatMode(out[i].Phone)
		out[i].Programado = s.tienePendiente(`
            SELECT 1 FROM scheduled_orders WHERE phone = ? AND estado IN ('pendiente','confirmando') LIMIT 1`,
			out[i].Phone)
		out[i].EnEspera = s.tienePendiente(`
            SELECT 1 FROM pending_wait WHERE phone = ? LIMIT 1`, out[i].Phone)
	}
	return out
}

// tienePendiente devuelve true si la consulta (que debe seleccionar 1 columna) trae alguna fila.
// Best-effort: ante cualquier error responde false, para no romper el listado del panel.
func (s *sqliteStore) tienePendiente(query, phone string) bool {
	var x int
	if err := s.db.QueryRow(query, phone).Scan(&x); err != nil {
		return false
	}
	return true
}

func (s *sqliteStore) CreateScheduled(o ScheduledOrder) int64 {
	res, err := s.db.Exec(`
        INSERT INTO scheduled_orders(phone, identificacion, nombres, idcategoria, idproducto,
            idcolor, cantidad, idtipopago, producto_nombre, color_nombre, latitude, longitude,
            hora_propuesta, estado, created_at)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.Phone, o.Identificacion, o.Nombres, o.IDCategoria, o.IDProducto, o.IDColor,
		o.Cantidad, o.IDTipoPago, o.ProductoNombre, o.ColorNombre, o.Latitude, o.Longitude,
		o.HoraPropuesta, SchedulePendiente, time.Now().Unix())
	if err != nil {
		log.Printf("[sqlite] CreateScheduled %s: %v", o.Phone, err)
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

func (s *sqliteStore) scanScheduled(rows *sql.Rows) []ScheduledOrder {
	var out []ScheduledOrder
	for rows.Next() {
		var o ScheduledOrder
		if err := rows.Scan(&o.ID, &o.Phone, &o.Identificacion, &o.Nombres, &o.IDCategoria,
			&o.IDProducto, &o.IDColor, &o.Cantidad, &o.IDTipoPago, &o.ProductoNombre,
			&o.ColorNombre, &o.Latitude, &o.Longitude, &o.HoraPropuesta, &o.Estado,
			&o.ConfirmSentAt, &o.CreatedAt); err != nil {
			continue
		}
		out = append(out, o)
	}
	return out
}

const scheduledCols = `id, phone, COALESCE(identificacion,''), COALESCE(nombres,''),
    COALESCE(idcategoria,0), COALESCE(idproducto,0), COALESCE(idcolor,0), COALESCE(cantidad,0),
    COALESCE(idtipopago,0), COALESCE(producto_nombre,''), COALESCE(color_nombre,''),
    COALESCE(latitude,0), COALESCE(longitude,0), hora_propuesta, estado,
    COALESCE(confirm_sent_at,0), created_at`

func (s *sqliteStore) DueScheduled(now int64) []ScheduledOrder {
	rows, err := s.db.Query(`SELECT `+scheduledCols+` FROM scheduled_orders
        WHERE estado = ? AND hora_propuesta <= ? ORDER BY hora_propuesta`, SchedulePendiente, now)
	if err != nil {
		log.Printf("[sqlite] DueScheduled: %v", err)
		return nil
	}
	defer rows.Close()
	return s.scanScheduled(rows)
}

func (s *sqliteStore) GetConfirmingSchedule(phone string) (ScheduledOrder, bool) {
	rows, err := s.db.Query(`SELECT `+scheduledCols+` FROM scheduled_orders
        WHERE phone = ? AND estado = ? ORDER BY id DESC LIMIT 1`, phone, ScheduleConfirmando)
	if err != nil {
		log.Printf("[sqlite] GetConfirmingSchedule %s: %v", phone, err)
		return ScheduledOrder{}, false
	}
	defer rows.Close()
	out := s.scanScheduled(rows)
	if len(out) == 0 {
		return ScheduledOrder{}, false
	}
	return out[0], true
}

func (s *sqliteStore) SetScheduledEstado(id int64, estado string) {
	if _, err := s.db.Exec(`UPDATE scheduled_orders SET estado = ? WHERE id = ?`, estado, id); err != nil {
		log.Printf("[sqlite] SetScheduledEstado %d: %v", id, err)
	}
}

func (s *sqliteStore) MarkConfirmSent(id int64, ts int64) {
	if _, err := s.db.Exec(`UPDATE scheduled_orders SET estado = ?, confirm_sent_at = ? WHERE id = ?`,
		ScheduleConfirmando, ts, id); err != nil {
		log.Printf("[sqlite] MarkConfirmSent %d: %v", id, err)
	}
}

func (s *sqliteStore) ExpireConfirming(olderThan int64) {
	if _, err := s.db.Exec(`UPDATE scheduled_orders SET estado = ? WHERE estado = ? AND confirm_sent_at < ?`,
		ScheduleExpirado, ScheduleConfirmando, olderThan); err != nil {
		log.Printf("[sqlite] ExpireConfirming: %v", err)
	}
}

func (s *sqliteStore) LastClientMessageAt(phone string) (int64, bool) {
	var ts sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(created_at) FROM message_log WHERE phone = ? AND role = 'user'`,
		phone).Scan(&ts)
	if err != nil || !ts.Valid {
		return 0, false
	}
	return ts.Int64, true
}

func (s *sqliteStore) CountScheduled(estado string) int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scheduled_orders WHERE estado = ?`, estado).Scan(&n); err != nil {
		log.Printf("[sqlite] CountScheduled: %v", err)
		return 0
	}
	return n
}

func (s *sqliteStore) CreateTicket(phone, motivo, resumen string) int64 {
	res, err := s.db.Exec(
		`INSERT INTO tickets(phone, motivo, resumen, estado, created_at) VALUES(?, ?, ?, ?, ?)`,
		phone, motivo, resumen, TicketAbierto, time.Now().Unix())
	if err != nil {
		log.Printf("[sqlite] CreateTicket %s: %v", phone, err)
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

func (s *sqliteStore) ListTickets(estado string, limit int) []Ticket {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT id, phone, COALESCE(motivo,''), COALESCE(resumen,''), estado, COALESCE(solucion,''),
                 created_at, COALESCE(closed_at,0) FROM tickets`
	args := []any{}
	if estado == TicketAbierto || estado == TicketCerrado {
		q += ` WHERE estado = ?`
		args = append(args, estado)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		log.Printf("[sqlite] ListTickets: %v", err)
		return nil
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.Phone, &t.Motivo, &t.Resumen, &t.Estado, &t.Solucion,
			&t.CreatedAt, &t.ClosedAt); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (s *sqliteStore) CloseTicket(id int64, solucion string) bool {
	res, err := s.db.Exec(
		`UPDATE tickets SET estado = ?, solucion = ?, closed_at = ? WHERE id = ? AND estado = ?`,
		TicketCerrado, solucion, time.Now().Unix(), id, TicketAbierto)
	if err != nil {
		log.Printf("[sqlite] CloseTicket %d: %v", id, err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *sqliteStore) GetChatMode(phone string) string {
	var mode string
	err := s.db.QueryRow(`SELECT mode FROM chat_control WHERE phone = ?`, phone).Scan(&mode)
	if err != nil || mode == "" {
		return ChatModeBot
	}
	return mode
}

func (s *sqliteStore) SetChatMode(phone, mode string) {
	if mode != ChatModeHuman {
		mode = ChatModeBot
	}
	if _, err := s.db.Exec(`
        INSERT INTO chat_control(phone, mode, updated_at) VALUES(?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET mode=excluded.mode, updated_at=excluded.updated_at`,
		phone, mode, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetChatMode %s: %v", phone, err)
	}
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

func (s *sqliteStore) SetPendingRating(phone string, rating PendingRating) {
	if _, err := s.db.Exec(`
        INSERT INTO pending_rating(phone, pedido_id, conductor, created_at) VALUES(?, ?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET
            pedido_id=excluded.pedido_id, conductor=excluded.conductor, created_at=excluded.created_at`,
		phone, rating.PedidoID, rating.Conductor, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetPendingRating %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetPendingRating(phone string) (PendingRating, bool) {
	// Caducidad (RatingTTL): un pendiente viejo se ignora (y se purga perezosamente) para que
	// el bot no pida la calificación en cada conversación por los siglos de los siglos.
	minTime := time.Now().Add(-RatingTTL).Unix()
	if _, err := s.db.Exec(`DELETE FROM pending_rating WHERE created_at < ?`, minTime); err != nil {
		log.Printf("[sqlite] purga pending_rating: %v", err)
	}
	var rating PendingRating
	err := s.db.QueryRow(
		`SELECT pedido_id, conductor FROM pending_rating WHERE phone = ? AND created_at >= ?`,
		phone, minTime).
		Scan(&rating.PedidoID, &rating.Conductor)
	if err == sql.ErrNoRows {
		return PendingRating{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetPendingRating %s: %v", phone, err)
		return PendingRating{}, false
	}
	return rating, true
}

func (s *sqliteStore) ClearPendingRating(phone string) {
	if _, err := s.db.Exec(`DELETE FROM pending_rating WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearPendingRating %s: %v", phone, err)
	}
}

func (s *sqliteStore) SetOrderPhone(pedidoID int, phone string) {
	if _, err := s.db.Exec(`
        INSERT INTO order_phone(pedido_id, phone, created_at) VALUES(?, ?, ?)
        ON CONFLICT(pedido_id) DO UPDATE SET phone=excluded.phone, created_at=excluded.created_at`,
		pedidoID, phone, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetOrderPhone %d: %v", pedidoID, err)
	}
}

func (s *sqliteStore) GetOrderPhone(pedidoID int) (string, bool) {
	var phone string
	err := s.db.QueryRow(`SELECT phone FROM order_phone WHERE pedido_id = ?`, pedidoID).Scan(&phone)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		log.Printf("[sqlite] GetOrderPhone %d: %v", pedidoID, err)
		return "", false
	}
	return phone, true
}

func (s *sqliteStore) SetActivePedido(phone string, pedidoID int) {
	if _, err := s.db.Exec(`
        INSERT INTO active_pedido(phone, pedido_id, created_at) VALUES(?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET pedido_id=excluded.pedido_id, created_at=excluded.created_at`,
		phone, pedidoID, time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetActivePedido %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetActivePedido(phone string) (int, bool) {
	var id int
	err := s.db.QueryRow(`SELECT pedido_id FROM active_pedido WHERE phone = ?`, phone).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		log.Printf("[sqlite] GetActivePedido %s: %v", phone, err)
		return 0, false
	}
	return id, true
}

func (s *sqliteStore) ClearActivePedido(phone string) {
	if _, err := s.db.Exec(`DELETE FROM active_pedido WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearActivePedido %s: %v", phone, err)
	}
}

func (s *sqliteStore) SetPendingWait(phone string, w PendingWait) {
	data, err := json.Marshal(w)
	if err != nil {
		log.Printf("[sqlite] SetPendingWait marshal %s: %v", phone, err)
		return
	}
	if _, err := s.db.Exec(`
        INSERT INTO pending_wait(phone, data, created_at) VALUES(?, ?, ?)
        ON CONFLICT(phone) DO UPDATE SET data=excluded.data, created_at=excluded.created_at`,
		phone, string(data), time.Now().Unix()); err != nil {
		log.Printf("[sqlite] SetPendingWait %s: %v", phone, err)
	}
}

func (s *sqliteStore) GetPendingWait(phone string) (PendingWait, bool) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM pending_wait WHERE phone = ?`, phone).Scan(&data)
	if err == sql.ErrNoRows {
		return PendingWait{}, false
	}
	if err != nil {
		log.Printf("[sqlite] GetPendingWait %s: %v", phone, err)
		return PendingWait{}, false
	}
	var w PendingWait
	if err := json.Unmarshal([]byte(data), &w); err != nil {
		log.Printf("[sqlite] GetPendingWait unmarshal %s: %v", phone, err)
		return PendingWait{}, false
	}
	return w, true
}

func (s *sqliteStore) ClearPendingWait(phone string) {
	if _, err := s.db.Exec(`DELETE FROM pending_wait WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearPendingWait %s: %v", phone, err)
	}
}

func (s *sqliteStore) ClearPendingVerification(phone string) {
	if _, err := s.db.Exec(`DELETE FROM pending_verif WHERE phone = ?`, phone); err != nil {
		log.Printf("[sqlite] ClearPendingVerification %s: %v", phone, err)
	}
}
