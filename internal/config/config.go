// Package config lee y valida la configuración del bot desde variables de entorno.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config agrupa toda la configuración del servicio.
type Config struct {
	GoogleAPIKey    string
	GeminiModel     string
	WhatsAppToken   string
	PhoneNumberID   string
	VerifyToken     string
	OwnerPhone      string
	Port            string
	GraphAPIVersion string
	// BackendURL es la URL base del backend GEOWARE (ubi-geoware). El bot consume
	// la API de georoutes (mismo flujo que la app móvil) bajo {BackendURL}/georoutes/.
	BackendURL string
	// ChannelSecret autentica al bot como canal de confianza ante el backend
	// (verificación tipo Camino B). Opcional hasta que David lo habilite.
	ChannelSecret string
	// CatalogUser/CatalogPassword son las credenciales de una cuenta de servicio para
	// leer el catálogo cuando el backend corre con DEBUG=False (los GET exigen JWT).
	// Opcionales: si están vacías, el catálogo se pide sin token (DEV con DEBUG=True).
	CatalogUser     string
	CatalogPassword string
	// DBPath es la ruta del archivo SQLite para persistir el historial.
	// Si está vacía, el bot usa el almacén en memoria (desarrollo).
	DBPath string
	// AuditLogDays es cuántos días se conserva el registro de auditoría (message_log) de las
	// conversaciones, para revisarlas desde la web. Parametrizable; default 15.
	AuditLogDays int
	// HumanTakeoverTimeout es la inactividad tras la cual un chat en control HUMANO vuelve solo
	// al bot (para que un pedido nuevo lo atienda el bot y no quede colgado). Default 3h.
	HumanTakeoverTimeout time.Duration
}

func required(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("Falta la variable de entorno obligatoria: %s", k)
	}
	return v
}

func optional(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func optionalInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Load construye la Config leyendo el entorno. Aborta el arranque si falta una obligatoria.
func Load() Config {
	return Config{
		GoogleAPIKey:    required("GOOGLE_API_KEY"),
		GeminiModel:     optional("GEMINI_MODEL", "gemini-3.1-flash-lite"),
		WhatsAppToken:   required("WHATSAPP_TOKEN"),
		PhoneNumberID:   required("WHATSAPP_PHONE_NUMBER_ID"),
		VerifyToken:     required("WEBHOOK_VERIFY_TOKEN"),
		OwnerPhone:      required("OWNER_PHONE_NUMBER"),
		Port:            optional("PORT", "3000"),
		GraphAPIVersion: "v21.0",
		BackendURL:      strings.TrimRight(optional("BACKEND_URL", "http://127.0.0.1:8000"), "/"),
		ChannelSecret:   os.Getenv("BACKEND_CHANNEL_SECRET"),
		CatalogUser:     os.Getenv("CATALOG_USER"),
		CatalogPassword: os.Getenv("CATALOG_PASSWORD"),
		DBPath:          os.Getenv("DB_PATH"),
		AuditLogDays:    optionalInt("AUDIT_LOG_DAYS", 15),
		HumanTakeoverTimeout: time.Duration(optionalInt("HUMAN_TAKEOVER_TIMEOUT_MIN", 180)) * time.Minute,
	}
}
