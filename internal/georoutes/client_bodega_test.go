package georoutes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// En modo bodega la cuenta del cliente sale de /wppClienteBodega/ con el secreto de bodegas, y NUNCA
// de /wppGetOrCreateClient/ (el del bot de Ubi, que le regeneraría la contraseña a su usuario).
func TestEnModoBodegaLaCuentaNoTocaLaDelBotDeUbi(t *testing.T) {
	var rutas, secretos []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rutas = append(rutas, r.URL.Path)
		secretos = append(secretos, r.Header.Get("X-Channel-Secret"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"codigo":200,"resultado":{"username":"wb3_593999000111_x","password":"p"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.SetBodega("sesion-bodega-sur", "secreto-bodegas")
	if _, err := c.WppGetOrCreateClient("", "Julio", "593999000111"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WppActualizarNombre("Julio", "593999000111"); err != nil {
		t.Fatal(err)
	}
	for i, ruta := range rutas {
		if ruta != "/georoutes/wppClienteBodega/" || secretos[i] != "secreto-bodegas" {
			t.Fatalf("llamada %d: %s (secreto %q)", i, ruta, secretos[i])
		}
	}
}
