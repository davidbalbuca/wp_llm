package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/llm"
)

// Prueba de ESTRÉS de concurrencia: muchos clientes a la vez sobre el MISMO *Agent, que es
// como corre en producción (un solo Agent, una goroutine por mensaje).
//
// ALCANCE REAL de este test, medido con mutantes y no supuesto: al reintroducir el estado
// compartido en el Agent, ESTE test sigue en verde (las goroutines son demasiado rápidas para
// solaparse de forma fiable) y quien lo detecta es TestDosClientesSimultaneosNoSePisanElTurno,
// que orquesta el interleaving exacto con canales. Aquí se cubre otra cosa: que el camino
// completo de HandleMessage aguante carga real sin deadlocks, sin errores y sin mezclar
// historiales. No pretende sustituir al de carrera ni a `go test -race`.
//
// `go test -race` NO se puede correr en este contenedor: el detector necesita cgo y no hay
// compilador C (ni permisos para instalarlo). Pendiente en un entorno con gcc:
//
//	CGO_ENABLED=1 go test -race ./...
func TestMuchosClientesSimultaneosNoSeMezclan(t *testing.T) {
	const clientes = 60
	store := conversation.NewMemStore()

	// Cada cliente tiene su propio texto y espera SU respuesta.
	respuestas := make(map[string]string, clientes)
	for i := 0; i < clientes; i++ {
		respuestas[fmt.Sprintf("mensaje del cliente %d", i)] = fmt.Sprintf("respuesta para el cliente %d", i)
	}
	fake := &modeloPorMensaje{respuestas: respuestas}
	ag := agentIncidente(fake, store)

	var wg sync.WaitGroup
	errores := make(chan string, clientes)
	for i := 0; i < clientes; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			from := fmt.Sprintf("59399920%04d", n)
			texto := fmt.Sprintf("mensaje del cliente %d", n)
			res, err := ag.HandleMessage(context.Background(), from, texto)
			if err != nil {
				errores <- fmt.Sprintf("cliente %d: %v", n, err)
				return
			}
			if quiero := respuestas[texto]; res.Texto != quiero {
				errores <- fmt.Sprintf("CRUCE: el cliente %d recibió %q, esperaba %q", n, res.Texto, quiero)
			}
			// Y su historial es SUYO.
			h := store.History(from)
			if len(h) != 2 || h[0].Parts[0].Text != texto {
				errores <- fmt.Sprintf("el historial del cliente %d no es suyo: %d turnos", n, len(h))
			}
		}(i)
	}

	hecho := make(chan struct{})
	go func() { wg.Wait(); close(hecho) }()
	select {
	case <-hecho:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: los turnos no terminaron (¿deadlock con el Agent compartido?)")
	}

	close(errores)
	for e := range errores {
		t.Error(e)
	}
}

// modeloPorMensaje responde según el texto que le llega, para poder detectar cruces entre
// clientes: si un turno recibe la respuesta de otro, el texto no coincide.
type modeloPorMensaje struct {
	respuestas map[string]string
	mu         sync.Mutex
}

func (m *modeloPorMensaje) Generate(ctx context.Context, system llm.System, history []*genai.Content, tools []*genai.Tool) (llm.Response, error) {
	var texto string
	if n := len(history); n > 0 && len(history[n-1].Parts) > 0 {
		texto = history[n-1].Parts[0].Text
	}
	m.mu.Lock()
	respuesta := m.respuestas[texto]
	m.mu.Unlock()
	return llm.Response{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: respuesta}}},
		Text:    respuesta,
	}, nil
}

func (m *modeloPorMensaje) Nombre() string { return "fake" }
func (m *modeloPorMensaje) Modelo() string { return "fake-por-mensaje" }
