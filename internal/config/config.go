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
	// LLMProvider elige quien atiende las conversaciones: "gemini" (el de siempre, default)
	// o "anthropic". Se cambia con una linea del .env y se vuelve atras igual de rapido; la
	// configuracion del otro proveedor se queda donde esta, sin borrarse.
	LLMProvider string
	// Anthropic (Claude). Solo se usan si LLMProvider es "anthropic".
	AnthropicAPIKey    string
	AnthropicModel     string
	AnthropicMaxTokens int
	// AnthropicCacheTTL: "5m" (default) o "1h". Cuanto vive el prompt cacheado. Con poco
	// trafico conviene "1h" (sobrevive entre conversaciones, aunque escribirla cueste mas);
	// con conversaciones seguidas alcanza con los 5 minutos.
	AnthropicCacheTTL string

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
	// SMTP para notificar por CORREO los tickets de soporte (escalaciones). Si falta host o
	// destinatario, la notificación por correo queda apagada (el ticket igual se crea).
	SMTPHost       string
	SMTPPort       string
	SMTPUser       string
	SMTPPassword   string
	SupportEmailTo string
	// HumanTakeoverTimeout es la inactividad tras la cual un chat en control HUMANO vuelve solo
	// al bot (para que un pedido nuevo lo atienda el bot y no quede colgado). Default 15 min.
	HumanTakeoverTimeout time.Duration
	// CierreInactividad es el silencio tras el cual el bot se despide de una conversacion que
	// quedo a medias. CierreVentanaMax es el techo: pasado ese tiempo ya no se dice nada, para
	// no salir a despedirse de los chats viejos al reiniciar el servicio.
	CierreInactividad time.Duration
	CierreVentanaMax  time.Duration
	// Telegram: grupo de alertas de operación para la etapa de test productivo (avisos de inicio
	// de conversación y de fallos). Si falta el token o el chat, la función queda APAGADA y el
	// bot sigue igual: los avisos son un observador, nunca parte del flujo del cliente.
	// TelegramAvisarInicio permite dejar solo los errores (los verdes son los ruidosos).
	TelegramBotToken     string
	TelegramChatID       string
	TelegramAvisarInicio bool
	// Horario laboral de entregas (hora Ecuador, formato HH:MM). Fuera de este horario el bot
	// NO registra pedidos: ofrece PROGRAMAR la entrega para una hora dentro del horario.
	BotHorarioInicio string
	BotHorarioFin    string
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
	// Se exige la clave del proveedor ELEGIDO, no la de los dos: asi se puede probar Anthropic
	// sin borrar lo de Gemini, y volver a Gemini sin tener que conseguir la otra clave.
	proveedor := strings.ToLower(strings.TrimSpace(optional("LLM_PROVIDER", "gemini")))
	googleKey := os.Getenv("GOOGLE_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	switch proveedor {
	case "gemini":
		if googleKey == "" {
			log.Fatalf("Falta la variable de entorno obligatoria: GOOGLE_API_KEY")
		}
	case "anthropic":
		if anthropicKey == "" {
			log.Fatalf("LLM_PROVIDER=anthropic pero falta ANTHROPIC_API_KEY")
		}
	default:
		log.Fatalf("LLM_PROVIDER no reconocido: %q (usa \"gemini\" o \"anthropic\")", proveedor)
	}

	return Config{
		LLMProvider:          proveedor,
		AnthropicAPIKey:      anthropicKey,
		AnthropicModel:       optional("ANTHROPIC_MODEL", "claude-haiku-4-5-20251001"),
		AnthropicMaxTokens:   optionalInt("ANTHROPIC_MAX_TOKENS", 1024),
		AnthropicCacheTTL:    optional("ANTHROPIC_CACHE_TTL", "5m"),
		GoogleAPIKey:         googleKey,
		GeminiModel:          optional("GEMINI_MODEL", "gemini-3.1-flash-lite"),
		WhatsAppToken:        required("WHATSAPP_TOKEN"),
		PhoneNumberID:        required("WHATSAPP_PHONE_NUMBER_ID"),
		VerifyToken:          required("WEBHOOK_VERIFY_TOKEN"),
		OwnerPhone:           required("OWNER_PHONE_NUMBER"),
		Port:                 optional("PORT", "3000"),
		GraphAPIVersion:      "v21.0",
		BackendURL:           strings.TrimRight(optional("BACKEND_URL", "http://127.0.0.1:8000"), "/"),
		ChannelSecret:        os.Getenv("BACKEND_CHANNEL_SECRET"),
		CatalogUser:          os.Getenv("CATALOG_USER"),
		CatalogPassword:      os.Getenv("CATALOG_PASSWORD"),
		DBPath:               os.Getenv("DB_PATH"),
		AuditLogDays:         optionalInt("AUDIT_LOG_DAYS", 15),
		HumanTakeoverTimeout: time.Duration(optionalInt("HUMAN_TAKEOVER_TIMEOUT_MIN", 15)) * time.Minute,
		CierreInactividad:    time.Duration(optionalInt("BOT_CIERRE_INACTIVIDAD_MIN", 7)) * time.Minute,
		CierreVentanaMax:     time.Duration(optionalInt("BOT_CIERRE_VENTANA_MAX_MIN", 60)) * time.Minute,
		BotHorarioInicio:     optional("BOT_HORARIO_INICIO", "07:00"),
		BotHorarioFin:        optional("BOT_HORARIO_FIN", "19:00"),
		SMTPHost:             os.Getenv("SMTP_HOST"),
		SMTPPort:             optional("SMTP_PORT", "587"),
		SMTPUser:             os.Getenv("SMTP_USER"),
		SMTPPassword:         os.Getenv("SMTP_PASSWORD"),
		SupportEmailTo:       os.Getenv("SUPPORT_EMAIL_TO"),
		TelegramBotToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:       os.Getenv("TELEGRAM_CHAT_ID"),
		TelegramAvisarInicio: optionalBool("TELEGRAM_AVISAR_INICIO", true),
	}
}

// optionalBool lee una bandera del entorno. Cuenta como false "0", "false" y "no" (sin importar
// mayúsculas); cualquier otro valor no vacío es true.
func optionalBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	if v == "" {
		return def
	}
	return v != "0" && v != "false" && v != "no"
}
