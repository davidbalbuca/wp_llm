package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wp-llm-gas/internal/config"
)

// Los avisos del backend llegan al bot de SU canal: el de Ubi rechaza los de una bodega, y el de
// bodegas solo atiende sesiones de bodegas activas (nunca la de Ubi).

func aviso(cuerpo string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/internal/order-arrived", strings.NewReader(cuerpo))
}

func TestElBotDeUbiRechazaLosAvisosDeUnaBodega(t *testing.T) {
	ubi := &canalBot{cfg: config.Config{Marca: "Ubi"}}
	elegir := botFijo(ubi)
	w := httptest.NewRecorder()
	if _, ok := elegir(w, aviso(`{"pedido_id":1,"telefono":"593","sesion":"sesion-bodega-sur"}`)); ok || w.Code != http.StatusConflict {
		t.Fatalf("un aviso de bodega lo atendió el bot de Ubi (code %d)", w.Code)
	}
	r := aviso(`{"pedido_id":1,"telefono":"593"}`)
	b, ok := elegir(httptest.NewRecorder(), r)
	if !ok || b != ubi {
		t.Fatal("el aviso normal no llegó al bot de Ubi")
	}
	// El cuerpo sigue entero para el handler.
	resto, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(resto), `"pedido_id":1`) {
		t.Fatalf("se consumió el cuerpo del aviso: %q", resto)
	}
}

func TestElBotDeBodegasEligePorSesionYNuncaLaDeUbi(t *testing.T) {
	rg := nuevoRegistro(config.Config{OpenWASesionUbi: "sesion-ubi"})
	sur := &canalBot{cfg: config.Config{OpenWASesion: "sesion-bodega-sur"}}
	rg.info = map[string]bodegaInfo{
		"sesion-bodega-sur":   {Sesion: "sesion-bodega-sur", BodegaID: 3, Activo: true},
		"sesion-bodega-norte": {Sesion: "sesion-bodega-norte", BodegaID: 4, Activo: false},
		"sesion-ubi":          {Sesion: "sesion-ubi", BodegaID: 9, Activo: true}, // ni aunque el backend la mande
	}
	rg.bots = map[string]*canalBot{"sesion-bodega-sur": sur}
	rg.leido = time.Now()

	if b, ok := rg.elegir(httptest.NewRecorder(), aviso(`{"sesion":"sesion-bodega-sur"}`)); !ok || b != sur {
		t.Fatal("el aviso de la bodega sur no llegó a su bot")
	}
	for _, cuerpo := range []string{`{"sesion":"sesion-ubi"}`, `{"sesion":"sesion-bodega-norte"}`,
		`{"sesion":"otra"}`, `{"pedido_id":1}`} {
		w := httptest.NewRecorder()
		if _, ok := rg.elegir(w, aviso(cuerpo)); ok || w.Code != http.StatusNotFound {
			t.Errorf("%s: se atendió (code %d)", cuerpo, w.Code)
		}
	}
}

func TestLaColaDeUnClienteEsPorCanal(t *testing.T) {
	ubi, sur, norte := config.Config{}, config.Config{OpenWASesion: "sur"}, config.Config{OpenWASesion: "norte"}
	tel := "593999000111"
	if claveCliente(ubi, tel) != tel {
		t.Fatal("en el bot de Ubi la clave sigue siendo el teléfono")
	}
	if lockCliente(claveCliente(sur, tel)) == lockCliente(claveCliente(norte, tel)) {
		t.Fatal("el mismo cliente en dos bodegas comparte la cola")
	}
}
