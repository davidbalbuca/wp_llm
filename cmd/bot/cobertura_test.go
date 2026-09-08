package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// backendCobertura levanta un backend falso que responde a /checkCoverage/ según el modo:
// "dentro", "fuera", o "error". Lo demás devuelve 404.
func backendCobertura(t *testing.T, modo string) *georoutes.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/georoutes/checkCoverage/":
			switch modo {
			case "dentro":
				w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"cubierto":true,"sector":"EL SAGRARIO","zona":"AZUAY"}}`))
			case "fuera":
				w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"cubierto":false,"sector":null,"zona":null}}`))
			default:
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"codigo":-1,"mensaje":"error interno"}`))
			}
		case "/georoutes/getCoverageZones/":
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"hay_cobertura":true,"zonas":[{"zona":"AZUAY","parroquias":["BANOS","BELLAVISTA","SININCAY"]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return georoutes.NewClient(srv.URL)
}

// REGLA DURA de la spec: si NUESTRO chequeo falla (backend caído, timeout), el flujo SIGUE.
// Un problema nuestro no puede dejar sin pedir a un cliente que sí tiene cobertura. Este es
// el mutante que ningún otro test detectaba: fueraDeCobertura devolviendo true en el error.
func TestFalloDelChequeoNoBloqueaAlCliente(t *testing.T) {
	gr := backendCobertura(t, "error")
	store := conversation.NewMemStore()

	if fueraDeCobertura(config.Config{}, store, gr, "593999000070", -2.89, -79.0) {
		t.Fatal("el chequeo de cobertura FALLÓ y aun así se bloqueó al cliente: " +
			"un error nuestro lo dejaría sin poder pedir gas")
	}
}

// Dentro de cobertura: el flujo sigue sin mensaje extra.
func TestDentroDeCoberturaElFlujoSigue(t *testing.T) {
	gr := backendCobertura(t, "dentro")
	store := conversation.NewMemStore()

	if fueraDeCobertura(config.Config{}, store, gr, "593999000071", -2.898, -79.002) {
		t.Fatal("un cliente DENTRO de cobertura fue tratado como fuera")
	}
}

// Fuera de cobertura: el turno se corta (true) y queda la marca en la auditoría. El envío
// del WhatsApp falla (config vacía) y no importa: lo que se mide es la decisión.
func TestFueraDeCoberturaCortaElTurno(t *testing.T) {
	gr := backendCobertura(t, "fuera")
	store := conversation.NewMemStore()
	const from = "593999000072"

	if !fueraDeCobertura(config.Config{}, store, gr, from, -0.18, -78.47) {
		t.Fatal("un cliente FUERA de cobertura siguió el flujo: pediría todos los datos " +
			"para fallar al final, que es lo que esta feature elimina")
	}
	// La auditoría lo registra (el panel lo muestra; el backend ya grabó la demanda).
	marcado := false
	for _, m := range store.GetConversation(from, 10) {
		if m.Role == "system" && len(m.Content) > 0 {
			marcado = true
		}
	}
	if !marcado {
		t.Error("no quedó rastro en la auditoría de que el cliente está fuera de cobertura")
	}
}
