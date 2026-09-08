package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// Tras rechazar la zona, el MODELO tiene que enterarse y la ubicación NO puede quedar
// guardada. Reportado en producción el 08/09 (cliente de Ambato): se le dijo "no llegamos a
// esa zona" y dos mensajes después el bot le pidió la ubicación otra vez, porque el rechazo
// solo estaba en la auditoría (LogMessage) y no en la memoria del modelo (AppendModel).
func TestFueraDeCoberturaLoRecuerdaElModeloYNoGuardaLaUbicacion(t *testing.T) {
	gr := backendCobertura(t, "fuera")
	store := conversation.NewMemStore()
	const from = "593999000073"

	// El flujo real guarda la ubicación ANTES de verificar; el chequeo debe deshacerlo.
	store.SetLocation(from, -1.2535, -78.6247) // Ambato
	if !fueraDeCobertura(config.Config{}, store, gr, from, -1.2535, -78.6247) {
		t.Fatal("Ambato debía quedar fuera de cobertura")
	}

	// 1) El modelo lo ve en SU historial (no solo el panel).
	visto := false
	for _, m := range store.History(from) {
		if m.Role == "model" && len(m.Parts) > 0 && strings.Contains(m.Parts[0].Text, "no llegamos") {
			visto = true
		}
	}
	if !visto {
		t.Error("el rechazo de zona no llegó a la memoria del modelo: en el siguiente turno " +
			"volvería a pedir la ubicación como si nada hubiera pasado")
	}

	// 2) La ubicación rechazada no se conserva: si quedara, el bloque UBICACION del prompt
	// diría "ya la tienes" y el modelo podría intentar un pedido con una zona ya descartada.
	if _, hay := store.GetLocation(from); hay {
		t.Error("la ubicación fuera de cobertura quedó guardada como si fuera válida")
	}
}

// El prompt no puede pedirle "el pin" al cliente: la palabra correcta es "ubicación".
func TestElPromptNoUsaLaPalabraPin(t *testing.T) {
	src, err := os.ReadFile("../../internal/agent/prompts/behavior.md")
	if err != nil {
		t.Fatalf("no se pudo leer behavior.md: %v", err)
	}
	texto := string(src)
	for _, linea := range strings.Split(texto, "\n") {
		l := strings.ToLower(linea)
		if !strings.Contains(l, "pin") {
			continue
		}
		// La única mención permitida es la regla que PROHÍBE usarla.
		if strings.Contains(l, "nunca \"pin\"") {
			continue
		}
		t.Errorf("el prompt menciona \"pin\" fuera de la regla que lo prohíbe: %q", strings.TrimSpace(linea))
	}
}
