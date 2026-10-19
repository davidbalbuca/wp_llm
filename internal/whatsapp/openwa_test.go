package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/config"
)

// El WhatsApp de una bodega y el de Ubi no se cruzan (18/10): lo de una bodega sale SOLO por su
// sesión de openwa, nunca por Meta ni por la sesión de Ubi; y lo de Ubi nunca sale por openwa.

type llamada struct {
	ruta string
	body map[string]any
}

func servidorFalso(t *testing.T) (*httptest.Server, *[]llamada) {
	t.Helper()
	var llamadas []llamada
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		llamadas = append(llamadas, llamada{ruta: r.URL.Path, body: b})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &llamadas
}

func cfgBodega(openwa, meta string) config.Config {
	return config.Config{Canal: config.CanalOpenWA, OpenWABaseURL: openwa, OpenWAAPIKey: "k",
		OpenWASesionUbi: "sesion-ubi", OpenWASesion: "sesion-bodega-sur", Marca: "Bodega Sur",
		WhatsAppAPIBase: meta, GraphAPIVersion: "v21.0", PhoneNumberID: "1"}
}

func TestLaBodegaMandaSoloPorSuSesionYNuncaPorMeta(t *testing.T) {
	openwa, porOpenWA := servidorFalso(t)
	meta, porMeta := servidorFalso(t)
	cfg := cfgBodega(openwa.URL, meta.URL)
	if err := SendText(cfg, "593999000111", "Gracias por confiar en Ubi 🙌"); err != nil {
		t.Fatal(err)
	}
	if err := SendMenu(cfg, "593999000111", "¿La misma dirección?", []string{"Sí", "Otra"}); err != nil {
		t.Fatal(err)
	}
	if len(*porMeta) != 0 {
		t.Fatalf("la bodega mandó por Meta (el número de Ubi): %v", *porMeta)
	}
	if len(*porOpenWA) != 2 {
		t.Fatalf("esperaba 2 envíos por openwa, hubo %d", len(*porOpenWA))
	}
	for _, l := range *porOpenWA {
		if l.ruta != "/api/sessions/sesion-bodega-sur/messages/send-text" {
			t.Fatalf("salió por otra sesión: %s", l.ruta)
		}
		if l.body["chatId"] != "593999000111@c.us" {
			t.Fatalf("chatId: %v", l.body["chatId"])
		}
	}
	if txt := (*porOpenWA)[0].body["text"].(string); strings.Contains(txt, "Ubi") || !strings.Contains(txt, "Bodega Sur") {
		t.Fatalf("al cliente de la bodega le llegó la marca de Ubi: %q", txt)
	}
}

func TestSinSesionOConLaDeUbiNoSeMandaNada(t *testing.T) {
	openwa, porOpenWA := servidorFalso(t)
	for _, sesion := range []string{"", "sesion-ubi"} {
		cfg := cfgBodega(openwa.URL, "")
		cfg.OpenWASesion = sesion
		if err := SendText(cfg, "593999000111", "hola"); err != ErrSesionAjena {
			t.Fatalf("sesión %q: esperaba ErrSesionAjena, fue %v", sesion, err)
		}
	}
	if len(*porOpenWA) != 0 {
		t.Fatalf("salió algo sin sesión de bodega: %v", *porOpenWA)
	}
}

func TestElBotDeUbiNuncaMandaPorOpenWA(t *testing.T) {
	openwa, porOpenWA := servidorFalso(t)
	meta, porMeta := servidorFalso(t)
	cfg := config.Config{Canal: config.CanalMeta, OpenWABaseURL: openwa.URL, WhatsAppAPIBase: meta.URL, WhatsAppToken: "t",
		GraphAPIVersion: "v21.0", PhoneNumberID: "1", Marca: "Ubi"}
	if err := SendText(cfg, "593999000111", "Gracias por confiar en Ubi"); err != nil {
		t.Fatal(err)
	}
	if len(*porOpenWA) != 0 || len(*porMeta) != 1 {
		t.Fatalf("openwa=%d meta=%d", len(*porOpenWA), len(*porMeta))
	}
	// Y una config con sesión de bodega no sale por Meta aunque diga canal meta.
	cfg.OpenWASesion = "sesion-bodega-sur"
	if err := SendText(cfg, "593999000111", "hola"); err == nil {
		t.Fatal("una config con sesión de bodega salió por Meta")
	}
	if len(*porMeta) != 1 {
		t.Fatalf("hubo %d envíos por Meta", len(*porMeta))
	}
}

func TestLaMarcaNoTocaUbicacionNiEnlaces(t *testing.T) {
	cfg := config.Config{Marca: "Bodega Sur"}
	got := ConMarca(cfg, "UbiGas te ayuda. Comparte tu *Ubicación*. https://ubi.com.ec Ubi.")
	want := "Bodega Sur te ayuda. Comparte tu *Ubicación*. https://ubi.com.ec Bodega Sur."
	if got != want {
		t.Fatalf("got %q", got)
	}
	if ConMarca(config.Config{Marca: "Ubi"}, "Ubi") != "Ubi" {
		t.Fatal("en el bot de Ubi no debe cambiar nada")
	}
}

func TestLaRespuestaAlMenuEsDeLaBodegaQueLoMando(t *testing.T) {
	openwa, _ := servidorFalso(t)
	sur := cfgBodega(openwa.URL, "")
	norte := sur
	norte.OpenWASesion = "sesion-bodega-norte"
	if err := SendMenu(sur, "593999000111", "¿Cuál?", []string{"Amarillo", "Azul"}); err != nil {
		t.Fatal(err)
	}
	if got := TraducirRespuestaMenu(norte, "593999000111", "2"); got != "2" {
		t.Fatalf("el menú de una bodega contestó en la otra: %q", got)
	}
	if got := TraducirRespuestaMenu(sur, "593999000111", "2️⃣"); got != "Azul" {
		t.Fatalf("got %q", got)
	}
	if got := TraducirRespuestaMenu(sur, "593999000111", "2"); got != "2" {
		t.Fatalf("el menú ya se usó y volvió a traducir: %q", got)
	}
	if got := TraducirRespuestaMenu(config.Config{Canal: config.CanalMeta}, "593999000111", "1"); got != "1" {
		t.Fatalf("en Meta no se traduce: %q", got)
	}
}

func webhook(sesion string, data map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"event": "message.received", "sessionId": sesion, "data": data})
	return b
}

func TestLaEntradaDescartaLaSesionDeUbiYLoQueNoEsDeUnCliente(t *testing.T) {
	cfg := cfgBodega("", "")
	normal := map[string]any{"from": "593999000111@c.us", "body": "hola", "type": "text"}
	if s, inc, ok := ParseOpenWA(cfg, webhook("sesion-bodega-sur", normal)); !ok || s != "sesion-bodega-sur" ||
		inc.From != "593999000111" || inc.Text != "hola" {
		t.Fatalf("mensaje normal: %v %q %+v", ok, s, inc)
	}
	casos := map[string][]byte{
		"sesión de Ubi": webhook("sesion-ubi", normal),
		"sin sesión":    webhook("", normal),
		"lo mandamos":   webhook("sesion-bodega-sur", map[string]any{"from": "593999000111@c.us", "fromMe": true, "type": "text"}),
		"grupo":         webhook("sesion-bodega-sur", map[string]any{"from": "123@g.us", "isGroup": true, "type": "text"}),
		"estado":        webhook("sesion-bodega-sur", map[string]any{"from": "593999000111@c.us", "isStatusBroadcast": true}),
		"lid sin tel":   webhook("sesion-bodega-sur", map[string]any{"from": "2697841@lid", "isLidSender": true, "type": "text"}),
	}
	for nombre, body := range casos {
		if _, _, ok := ParseOpenWA(cfg, body); ok {
			t.Errorf("%s: se aceptó", nombre)
		}
	}
	lid := map[string]any{"from": "2697841@lid", "isLidSender": true, "senderPhone": "593999000222", "type": "location",
		"location": map[string]any{"latitude": -2.9, "longitude": -79.0}, "contact": map[string]any{"pushName": "Ana"}}
	_, inc, ok := ParseOpenWA(cfg, webhook("sesion-bodega-sur", lid))
	if !ok || inc.From != "593999000222" || !inc.HasLocation || inc.PerfilNombre != "Ana" {
		t.Fatalf("LID con ubicación: %v %+v", ok, inc)
	}
}

func TestLaFirmaDelWebhookEsObligatoria(t *testing.T) {
	body := []byte(`{"x":1}`)
	m := hmac.New(sha256.New, []byte("secreto-bodegas"))
	m.Write(body)
	firma := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !FirmaOpenWAValida("secreto-bodegas", body, firma) {
		t.Fatal("firma buena rechazada")
	}
	if FirmaOpenWAValida("secreto-ubi", body, firma) || FirmaOpenWAValida("", body, firma) ||
		FirmaOpenWAValida("secreto-bodegas", body, "") {
		t.Fatal("se aceptó una firma que no es del secreto de bodegas")
	}
}

func TestConListaDePruebaSoloSeAtiendeYSeEscribeAEsosNumeros(t *testing.T) {
	openwa, porOpenWA := servidorFalso(t)
	cfg := cfgBodega(openwa.URL, "")
	cfg.SoloTelefonos = map[string]bool{"593999000111": true}
	if err := SendText(cfg, "593999000222", "hola"); err != ErrNoPermitido {
		t.Fatalf("a un número fuera de la lista: %v", err)
	}
	if err := SendText(cfg, "593999000111", "hola"); err != nil {
		t.Fatal(err)
	}
	if len(*porOpenWA) != 1 || !Permitido(cfg, "593999000111") || Permitido(cfg, "593999000222") {
		t.Fatalf("envíos: %d", len(*porOpenWA))
	}
	if !Permitido(cfgBodega("", ""), "593999000222") {
		t.Fatal("sin lista se atiende a todos")
	}
}
