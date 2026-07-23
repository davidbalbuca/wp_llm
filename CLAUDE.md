# wp_llm — Bot de WhatsApp

> **DOCUMENTACIÓN CENTRALIZADA EN LA RAÍZ.** Ver [`/CLAUDE.md`](../CLAUDE.md), [`/memory/`](../memory/), [`/specs/`](../specs/), [`/tasks/`](../tasks/).

## Qué es

Agente de IA (Google Gemini) que atiende clientes por WhatsApp para pedir gas a domicilio. **Registra pedidos en el backend `ubi-geoware` por el flujo real `georoutes`** (mismo que la app móvil), no por canal paralelo. Stack: **Go** (net/http stdlib), mensajería vía WhatsApp Business Cloud API (Meta).

**Estado:** Fase 3 COMPLETADA E2E (14-jul-2026).
- Bot es cliente HTTP de `/georoutes/*` (auth → login → dirección → catálogo → startOrder).
- Account (username+password+user_id+jwt+refresh) persistida en Store (mem + sqlite).
- JWT reutilizado sin re-login en cada pedido.
- `internal/backend` (rapi) eliminado.
- Verificación manual en DEV (T7 🔒 en proceso, depende de David).

## Estructura (Go)

```
cmd/bot/main.go                 # punto de entrada: net/http, wiring
internal/config/config.go       # variables de entorno
internal/whatsapp/whatsapp.go   # webhook verificación, parsing (texto/ubicación), envío Graph API
internal/agent/agent.go         # Gemini + function calling (registrar_pedido)
internal/georoutes/             # cliente HTTP del flujo real (auth, dirección, catálogo, startOrder)
internal/conversation/          # estado por teléfono (Store: memoria o SQLite)
internal/catalog/catalog.go     # datos del negocio (productos, pagos, zonas)
internal/escalation/            # derivación al dueño
```

Dependencias entre paquetes (sin ciclos): `agent` → {config, conversation, georoutes, escalation, catalog}; `escalation` → {config, whatsapp}; `whatsapp`/`georoutes`/`catalog` → config.

## Variables de entorno (`.env`)

```
GOOGLE_API_KEY              # Gemini API key
GEMINI_MODEL                # (default: gemini-3.1-flash-lite)
WHATSAPP_TOKEN              # Graph API token
WHATSAPP_PHONE_NUMBER_ID    # ID del número de WhatsApp
WEBHOOK_VERIFY_TOKEN        # token de verificación del webhook
OWNER_PHONE_NUMBER          # teléfono del dueño (escalación)
PORT                        # puerto del servidor (default: 3000)
BACKEND_URL                 # URL del backend (default: http://127.0.0.1:8000)
DB_PATH                     # (opcional) ruta a SQLite; si no, memoria
```

## Compilar / Correr

```bash
go build -o dist/bot ./cmd/bot           # compilar
go run ./cmd/bot                          # desarrollo
go vet ./...                              # verificar
```

**Detalles de arranque, pruebas, conductor-sim:** ver `/memory/03-correr-local-y-datos-prueba.md`.
