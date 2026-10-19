package main

// Bot de BODEGAS (BOT_CANAL=openwa, 18/10): el mismo bot, atendiendo el WhatsApp propio de cada
// bodega. Es un PROCESO APARTE del bot de Ubi (otro contenedor, otro puerto, otras bases, otro
// secreto con el backend) y adentro cada bodega tiene su canalBot: su sesión, su SQLite y su agente.
//
// Qué impide que se cruce con el WhatsApp de Ubi (y entre bodegas):
//   - Entrada: solo webhooks firmados con el secreto de bodegas; nunca la sesión de Ubi
//     (whatsapp.ParseOpenWA); solo sesiones que el backend dice que son de una bodega activa.
//   - Salida: cada bodega manda solo por su sesión (whatsapp.enviarOpenWA); sin credenciales de Meta.
//   - Backend: los avisos traen la `sesion` y van al bot de esa bodega; el secreto es distinto del
//     de Ubi, así que un aviso para Ubi no lo acepta este proceso, ni al revés.
//   - Cuenta del cliente: un usuario por bodega (georoutes.Client.SetBodega), no el del bot de Ubi.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"wp-llm-gas/internal/agent"
	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/whatsapp"
)

// bodegaInfo: lo que el backend dice de una sesión (GET /georoutes/wppBodegas/).
type bodegaInfo struct {
	Sesion   string `json:"sesion"`
	BodegaID int    `json:"bodega_id"`
	Nombre   string `json:"nombre"`
	Activo   bool   `json:"activo"`
}

const refrescoBodegas = time.Minute

type registroBodegas struct {
	base config.Config
	http *http.Client

	mu    sync.Mutex
	info  map[string]bodegaInfo // sesion -> bodega
	bots  map[string]*canalBot  // sesion -> bot ya armado
	leido time.Time
}

func nuevoRegistro(base config.Config) *registroBodegas {
	return &registroBodegas{base: base, http: &http.Client{Timeout: 10 * time.Second},
		info: map[string]bodegaInfo{}, bots: map[string]*canalBot{}}
}

// refrescar trae del backend las sesiones de bodega. Se llama con rg.mu tomado.
func (rg *registroBodegas) refrescar() error {
	req, _ := http.NewRequest(http.MethodGet, rg.base.BackendURL+"/georoutes/wppBodegas/", nil)
	req.Header.Set("X-Channel-Secret", rg.base.ChannelSecret)
	resp, err := rg.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("backend %d: %s", resp.StatusCode, string(b))
	}
	var env struct {
		Resultado []bodegaInfo `json:"resultado"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	info := map[string]bodegaInfo{}
	for _, b := range env.Resultado {
		if b.Sesion != "" && b.Sesion != rg.base.OpenWASesionUbi {
			info[b.Sesion] = b
		}
	}
	rg.info, rg.leido = info, time.Now()
	return nil
}

// bot devuelve el bot de la bodega de esa sesión (armándolo la primera vez), o false si la sesión no
// es de una bodega con el asistente encendido.
func (rg *registroBodegas) bot(sesion string) (*canalBot, bool) {
	if sesion == "" || sesion == rg.base.OpenWASesionUbi {
		return nil, false
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if time.Since(rg.leido) > refrescoBodegas {
		if err := rg.refrescar(); err != nil {
			log.Printf("[bodegas] no se pudo leer las bodegas del backend: %v (sigo con lo último)", err)
		}
	}
	inf, ok := rg.info[sesion]
	if !ok || !inf.Activo || inf.BodegaID <= 0 {
		return nil, false
	}
	if b, ok := rg.bots[sesion]; ok {
		return b, true
	}
	b, err := armarBotDeBodega(rg.base, inf)
	if err != nil {
		log.Printf("[bodegas] no se pudo armar el bot de la bodega %d: %v", inf.BodegaID, err)
		return nil, false
	}
	rg.bots[sesion] = b
	arrancarSegundoPlano(b)
	log.Printf("[bodegas] bot de la bodega %d (%s) listo", inf.BodegaID, inf.Nombre)
	return b, true
}

func (rg *registroBodegas) elegir(w http.ResponseWriter, r *http.Request) (*canalBot, bool) {
	b, ok := rg.bot(sesionDelAviso(r))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
	}
	return b, ok
}

func armarBotDeBodega(base config.Config, inf bodegaInfo) (*canalBot, error) {
	cfg := base
	cfg.OpenWASesion = inf.Sesion
	cfg.BodegaID = inf.BodegaID
	if inf.Nombre != "" {
		cfg.Marca = inf.Nombre
	}
	cfg.DBPath = filepath.Join(base.DBDir, fmt.Sprintf("bodega-%d.db", inf.BodegaID))
	store, err := conversation.NewSQLiteStore(cfg.DBPath, cfg.AuditLogDays)
	if err != nil {
		return nil, err
	}
	gr := georoutes.NewClient(cfg.BackendURL)
	gr.SetServiceAccount(cfg.CatalogUser, cfg.CatalogPassword)
	gr.SetBodega(inf.Sesion, cfg.ChannelSecret)
	ag, err := agent.New(context.Background(), cfg, store, catalog.NewClient(gr, catalogTTL), gr)
	if err != nil {
		return nil, err
	}
	ag.ReprogramarChequeosEntrega()
	go func() {
		if n := ag.RetomarEsperas(); n > 0 {
			log.Printf("[retomar] bodega %d: %d espera(s) de repartidor retomadas", inf.BodegaID, n)
		}
	}()
	return &canalBot{cfg: cfg, store: store, ag: ag, gr: gr}, nil
}

func mainBodegas(cfg config.Config) {
	if cfg.OpenWABaseURL == "" || cfg.OpenWAAPIKey == "" || cfg.OpenWAWebhookSecret == "" || cfg.ChannelSecret == "" {
		log.Fatal("bot de bodegas: faltan OPENWA_BASE_URL, OPENWA_API_KEY, OPENWA_WEBHOOK_SECRET o BACKEND_CHANNEL_SECRET")
	}
	if err := os.MkdirAll(cfg.DBDir, 0o755); err != nil {
		log.Fatalf("bot de bodegas: no se pudo crear %s: %v", cfg.DBDir, err)
	}
	rg := nuevoRegistro(cfg)
	// Al arrancar se arman las bodegas activas: sus entregas agendadas y esperas no pueden esperar a
	// que el cliente vuelva a escribir.
	go func() {
		for i := 0; i < 36; i++ {
			rg.mu.Lock()
			err := rg.refrescar()
			sesiones := make([]string, 0, len(rg.info))
			for s := range rg.info {
				sesiones = append(sesiones, s)
			}
			rg.mu.Unlock()
			if err == nil {
				for _, s := range sesiones {
					rg.bot(s)
				}
				return
			}
			time.Sleep(5 * time.Second)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "canal": config.CanalOpenWA})
	})
	mux.HandleFunc("POST /openwa/webhook", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if !whatsapp.FirmaOpenWAValida(cfg.OpenWAWebhookSecret, body, r.Header.Get("X-OpenWA-Signature")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		sesion, inc, ok := whatsapp.ParseOpenWA(cfg, body)
		if !ok {
			return
		}
		if !whatsapp.AtenderEntrante(cfg, inc) {
			return // modos de prueba (BOT_SOLO_TELEFONOS / BOT_SOLO_NO_CONTACTOS): ni se lee el mensaje
		}
		b, ok := rg.bot(sesion)
		if !ok {
			log.Printf("[bodegas] mensaje de la sesión %s, que no es de una bodega activa; se ignora", sesion)
			return
		}
		go procesarMensaje(b.cfg, b.ag, b.store, b.gr, inc)
	})
	registrarInternos(mux, rg.elegir)

	if cfg.SoloNoContactos {
		log.Printf("Bot de BODEGAS en modo PRUEBA: no atiende a los contactos guardados (BOT_SOLO_NO_CONTACTOS)")
	}
	if len(cfg.SoloTelefonos) > 0 {
		log.Printf("Bot de BODEGAS en modo PRUEBA: solo atiende y escribe a %d número(s) (BOT_SOLO_TELEFONOS)", len(cfg.SoloTelefonos))
	}
	log.Printf("Bot de BODEGAS escuchando en :%s (openwa %s)", cfg.Port, cfg.OpenWABaseURL)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}
