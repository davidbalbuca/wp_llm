package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/genai"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
	"wp-llm-gas/internal/llm"
)

// El bot tiene UN SOLO *Agent (cmd/bot/main.go) y abre una goroutine por mensaje entrante.
// Cuando el estado del turno vivía en ese struct compartido, el turno de un cliente pisaba el
// del otro: la escalación de uno se perdía porque el mensaje del otro reseteaba el campo, y los
// candados de forzar.go decidían con datos de otra conversación.
//
// Este test ejercita HandleMessage DE VERDAD sobre el mismo *Agent, con los dos clientes DENTRO
// del agente a la vez, y mide una señal del turno que sale al llamador (Resultado.Escalo).
// Se verificó que FALLA si el estado vuelve al Agent compartido.

// proveedorCruzado es un llm.Provider falso: al cliente marcado en escala le hace llamar la
// herramienta escalar_al_dueno (que marca el turno) en su primer round; en el round final
// retiene a ambos hasta que los dos están dentro, forzando el solapamiento que en producción
// depende del azar.
type proveedorCruzado struct {
	escala map[string]bool   // primer mensaje del cliente -> su turno escala
	textos map[string]string // primer mensaje del cliente -> respuesta final
	rondas map[string]int
	mu     sync.Mutex

	// Coordinación del interleaving EXACTO que producía el bug:
	//   1) María llama escalar_al_dueno  -> marca su turno
	//   2) Juan entra a HandleMessage    -> con el bug, resetea ese mismo turno
	//   3) María termina y lee su Escalo -> con el bug, ya está en false
	mariaEscalo chan struct{} // se cierra tras el paso 1
	juanEntro   chan struct{} // se cierra tras el paso 2
	quienEsJuan string
}

func (p *proveedorCruzado) Generate(ctx context.Context, system llm.System, history []*genai.Content, tools []*genai.Tool) (llm.Response, error) {
	// El primer turno de usuario del historial identifica de quién es esta conversación.
	var quien string
	for _, c := range history {
		if c.Role == "user" && len(c.Parts) > 0 && c.Parts[0].Text != "" {
			quien = c.Parts[0].Text
			break
		}
	}

	p.mu.Lock()
	ronda := p.rondas[quien]
	p.rondas[quien] = ronda + 1
	debeEscalar := p.escala[quien] && ronda == 0
	texto := p.textos[quien]
	p.mu.Unlock()

	// Juan ya está DENTRO del agente (su HandleMessage corrió el reset del turno). Se avisa
	// aquí, no antes: este punto está garantizado después del reset.
	if quien == p.quienEsJuan && ronda == 0 {
		<-p.mariaEscalo // deja que María marque su escalación primero
		close(p.juanEntro)
	}

	if debeEscalar {
		// Round 1: llama la herramienta, que marca la escalación en SU turno. No se sincroniza
		// aquí a propósito: la marca tiene que ocurrir ANTES de que el otro cliente entre.
		args := map[string]any{"motivo": "prueba", "resumen": "prueba"}
		defer close(p.mariaEscalo) // paso 1 hecho: el turno de María quedó marcado
		return llm.Response{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{Name: "escalar_al_dueno", Args: args}},
			}},
			Calls: []*genai.FunctionCall{{Name: "escalar_al_dueno", Args: args}},
		}, nil
	}

	// Round final de María: espera a que Juan haya entrado (y, con el bug, reseteado el turno
	// compartido) antes de devolver su texto. Ahí es donde la marca se perdía.
	if quien != p.quienEsJuan {
		<-p.juanEntro
	}

	return llm.Response{
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: texto}}},
		Text:    texto,
	}, nil
}

func (p *proveedorCruzado) Nombre() string { return "fake" }
func (p *proveedorCruzado) Modelo() string { return "fake-cruzado" }

func agentDePrueba(modelo llm.Provider, store conversation.Store) *Agent {
	// Backend inexistente: el catálogo falla y el prompt sale sin él. Da igual para este test,
	// que mide de quién es el turno, no qué se responde.
	muerto := georoutes.NewClient("http://127.0.0.1:1")
	return &Agent{
		cfg:     config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"},
		modelo:  modelo,
		store:   store,
		catalog: catalog.NewClient(muerto, time.Minute),
		gr:      muerto,
	}
}

// Dos clientes atendidos SIMULTÁNEAMENTE por el mismo *Agent: la escalación de uno no se
// pierde ni se le atribuye al otro. Con el estado en el Agent compartido, el reset del segundo
// HandleMessage borraba la marca del primero y cmd/bot no creaba el ticket.
func TestDosClientesSimultaneosNoSePisanElTurno(t *testing.T) {
	const maria, juan = "593999000001", "593999000002"
	const textoMaria, textoJuan = "mi gas nunca llegó", "cuánto cuesta el de 15 kilos"

	fake := &proveedorCruzado{
		mariaEscalo: make(chan struct{}),
		juanEntro:   make(chan struct{}),
		quienEsJuan: textoJuan,
		escala:      map[string]bool{textoMaria: true}, // solo María escala
		textos: map[string]string{
			textoMaria: "Ya avisé al equipo, María 🙏",
			textoJuan:  "Cuesta $3.50, Juan 😊",
		},
		rondas: map[string]int{},
	}
	ag := agentDePrueba(fake, conversation.NewMemStore())

	type salida struct {
		quien string
		res   Resultado
		err   error
	}
	out := make(chan salida, 2)
	for _, c := range []struct{ from, texto string }{{maria, textoMaria}, {juan, textoJuan}} {
		go func(from, texto string) {
			res, err := ag.HandleMessage(context.Background(), from, texto)
			out <- salida{from, res, err}
		}(c.from, c.texto)
	}

	got := map[string]Resultado{}
	for i := 0; i < 2; i++ {
		select {
		case s := <-out:
			if s.err != nil {
				t.Fatalf("%s: error inesperado: %v", s.quien, s.err)
			}
			got[s.quien] = s.res
		case <-time.After(5 * time.Second):
			t.Fatal("timeout: los turnos no terminaron (¿deadlock?)")
		}
	}

	if !got[maria].Escalo {
		t.Error("la escalación de María se PERDIÓ: el turno de Juan la borró. En producción " +
			"eso es un ticket que nunca se crea y un cliente al que nadie llama")
	}
	if got[juan].Escalo {
		t.Error("a Juan se le atribuyó la escalación de María: se le borraría su verificación pendiente")
	}
	if got[maria].Texto != fake.textos[textoMaria] || got[juan].Texto != fake.textos[textoJuan] {
		t.Errorf("cruce de respuestas: María=%q Juan=%q", got[maria].Texto, got[juan].Texto)
	}
}

// Contrato: un turno nuevo nace limpio. Si alguien devuelve estos campos al Agent, este test
// deja de compilar y el de arriba empieza a fallar.
func TestElTurnoLlevaElEstadoDelMensaje(t *testing.T) {
	tn := &turno{}
	tn.menuSent = true
	tn.lastMenuText = "¿Qué color?"
	tn.ultimoPedido = resultadoPedido{ok: true, IDPedido: 512}
	tn.cancelo, tn.programo, tn.escalado = true, true, true

	otro := &turno{}
	if otro.menuSent || otro.ultimoPedido.ok || otro.cancelo || otro.programo || otro.escalado {
		t.Fatal("un turno nuevo nace con estado de otro turno")
	}
}
