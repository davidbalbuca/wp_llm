package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// Fase C1 — pedido de VARIOS colores (specs/direcciones-y-pedido-multicolor.md).
//
// El caso que esto arregla (David, 10/09): pidió "Blanco y amarillo", la ficha solo guardó el
// último color, el modelo llamó registrar_pedido DOS veces, la idempotencia bloqueó la segunda
// y el bot le confirmó "1 BLANCO + 1 AMARILLO" habiendo registrado solo BLANCO. El backend
// siempre soportó varias líneas por pedido; era el bot el que no sabía pedirlas.

// backendQueRegistra levanta un backend falso que acepta el flujo completo del pedido
// (login, wppOrder, getDirectionsClient) y CAPTURA los productos que recibió wppOrder.
// Devuelve el agente, su store y un puntero a los payloads capturados (uno por llamada).
func backendQueRegistra(t *testing.T) (*Agent, conversation.Store, *[][]map[string]any) {
	t.Helper()
	var capturas [][]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "login"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"access":"jwt","refresh":"r"}}`))
		case strings.Contains(r.URL.Path, "getDirectionsClient"):
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":[]}`))
		case strings.Contains(r.URL.Path, "wppOrder"):
			var body struct {
				Productos []map[string]any `json:"productos"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturas = append(capturas, body.Productos)
			w.Write([]byte(`{"codigo":0,"mensaje":"ok","resultado":{"idpedido":900,"conductorasignado":"Nelson","placa":"ABC123","total":9.5}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)
	// El catálogo de incidentes no trae formas de pago y registrar exige al menos una.
	ag.catalog = catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, IDCategoria: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"}, {ID: 12, Nombre: "AZUL"},
		}}},
		Payments: []georoutes.Payment{{ID: 1, Nombre: "Efectivo"}},
	})
	ag.gr = georoutes.NewClient(srv.URL)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}
	return ag, store, &capturas
}

// clienteListoParaPedir deja el estado mínimo para que registrarPedido llegue al backend:
// ubicación fresca, perfil y cuenta.
func clienteListoParaPedir(store conversation.Store, from string) {
	store.SetLocation(from, -2.9, -79.0)
	store.SetProfile(from, conversation.Profile{Identificacion: "0105566777", Nombres: "David Espinoza"})
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p"})
}

// INCIDENTE 10/09 (David) — DOS COLORES, UN SOLO PEDIDO. La tool con 'items' produce UNA
// llamada a wppOrder con las DOS líneas; nunca dos llamadas.
func TestIncidente_MulticolorUnSoloPedido(t *testing.T) {
	const from = "593999200001"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)

	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{
		"items": []any{
			map[string]any{"color": "BLANCO", "cantidad": float64(1)},
			map[string]any{"color": "AMARILLO", "cantidad": float64(1)},
		},
	})

	if len(*capturas) != 1 {
		t.Fatalf("wppOrder se llamó %d veces; el multicolor debe ser UN solo pedido", len(*capturas))
	}
	if lineas := (*capturas)[0]; len(lineas) != 2 {
		t.Fatalf("el pedido llegó con %d líneas al backend; se pidieron 2: %v", len(lineas), lineas)
	}
	if !tn.ultimoPedido.ok {
		t.Fatalf("el pedido no quedó registrado: %q", salida)
	}
	// La confirmación al modelo lleva las DOS líneas: lo que se le dice al cliente es verdad.
	if !strings.Contains(salida, "BLANCO") || !strings.Contains(salida, "AMARILLO") {
		t.Errorf("la confirmación no detalla ambos colores: %q", salida)
	}
	// Y el último pedido guarda la lista completa, para poder repetirlo entero.
	last, _ := store.GetLastOrder(from)
	if len(last.Items) != 2 {
		t.Errorf("LastOrder no guardó las 2 líneas: %+v", last)
	}
}

// NO-REGRESIÓN (C1.4): un pedido de UN color se comporta EXACTAMENTE igual que siempre:
// una llamada, una línea, y LastOrder sin lista (items vacío, campos clásicos).
func TestMulticolorNoRegresionaElPedidoDeUnColor(t *testing.T) {
	const from = "593999200002"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)

	tn := &turno{}
	ag.runTool(tn, from, "registrar_pedido", map[string]any{"color": "BLANCO", "cantidad": float64(2)})

	if len(*capturas) != 1 || len((*capturas)[0]) != 1 {
		t.Fatalf("un pedido de un color debe ser una llamada con una línea: %v", *capturas)
	}
	if !tn.ultimoPedido.ok || tn.ultimoPedido.Color != "BLANCO" || tn.ultimoPedido.Cantidad != 2 {
		t.Fatalf("el resultado clásico cambió: %+v", tn.ultimoPedido)
	}
	last, _ := store.GetLastOrder(from)
	if last.Color != "BLANCO" || last.Cantidad != 2 || len(last.Items) != 0 {
		t.Errorf("LastOrder de un color debe ir en los campos clásicos sin lista: %+v", last)
	}
}

// REGLA DURA (C1.3): si UN color de la lista no existe, NO se registra NADA. Un pedido a
// medias es peor que preguntar.
func TestMulticolorConColorInexistenteNoRegistraNada(t *testing.T) {
	const from = "593999200003"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)

	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{
		"items": []any{
			map[string]any{"color": "BLANCO", "cantidad": float64(1)},
			map[string]any{"color": "FUCSIA", "cantidad": float64(1)},
		},
	})

	if len(*capturas) != 0 {
		t.Fatalf("se registró un pedido con un color inexistente: %v", *capturas)
	}
	if tn.ultimoPedido.ok || !strings.Contains(salida, "NO se registró nada") {
		t.Errorf("debía rechazarse completo y pedir el color: %q", salida)
	}
}

// REGLA DURA (C1.3): una línea sin cantidad no se registra; se pregunta cuántos de ESE color.
func TestMulticolorConLineaSinCantidadPregunta(t *testing.T) {
	const from = "593999200004"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)

	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{
		"items": []any{
			map[string]any{"color": "BLANCO", "cantidad": float64(1)},
			map[string]any{"color": "AMARILLO", "cantidad": float64(0)},
		},
	})

	if len(*capturas) != 0 {
		t.Fatalf("se registró un pedido con una línea sin cantidad: %v", *capturas)
	}
	if !strings.Contains(salida, "AMARILLO") {
		t.Errorf("hay que preguntar la cantidad del color que falta: %q", salida)
	}
}

// TOPES (C1.3): más de 3 colores o más de 10 cilindros escala a una persona, sin registrar.
func TestMulticolorGrandeEscalaAUnaPersona(t *testing.T) {
	const from = "593999200005"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)

	tn := &turno{}
	salida := ag.runTool(tn, from, "registrar_pedido", map[string]any{
		"items": []any{
			map[string]any{"color": "BLANCO", "cantidad": float64(6)},
			map[string]any{"color": "AMARILLO", "cantidad": float64(6)},
		},
	})

	if len(*capturas) != 0 {
		t.Fatalf("un pedido sobre el tope se registró igual: %v", *capturas)
	}
	if !tn.escalado || !strings.Contains(salida, "muy grande") {
		t.Errorf("un pedido de 12 cilindros debía escalar: escalado=%v %q", tn.escalado, salida)
	}
}

// LA FICHA — "Blanco y amarillo" en UN mensaje abre DOS líneas (antes ganaba el último color y
// la primera elección se perdía en silencio: el origen del incidente).
func TestFichaAnotaVariosColoresDeUnMensaje(t *testing.T) {
	const from = "593999200006"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "Blanco y amarillo")

	p, hay := store.GetPedidoEnCurso(from)
	if !hay {
		t.Fatal("la ficha no se escribió")
	}
	lineas := p.Lineas()
	if len(lineas) != 2 {
		t.Fatalf("dos colores en un mensaje deben abrir dos líneas: %+v", lineas)
	}
	if lineas[0].Color != "BLANCO" || lineas[1].Color != "AMARILLO" {
		t.Errorf("las líneas no respetan el orden en que el cliente los dijo: %+v", lineas)
	}

	// Las cantidades después, una por color: "1 de blanco" va a SU línea.
	ag.anotarDelMensaje(from, "1 de blanco")
	ag.anotarDelMensaje(from, "1 amarillo")
	p, _ = store.GetPedidoEnCurso(from)
	if !p.Completo() {
		t.Fatalf("con ambas cantidades la ficha debía estar completa: %+v", p.Lineas())
	}
}

// Un color solo, sin marca de suma, sigue siendo el cambio de opinión de siempre: pisa, no suma.
func TestFichaUnColorSigueSiendoCambioDeOpinion(t *testing.T) {
	const from = "593999200007"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "mejor amarillo")

	p, _ := store.GetPedidoEnCurso(from)
	if lineas := p.Lineas(); len(lineas) != 1 || lineas[0].Color != "AMARILLO" {
		t.Errorf("\"mejor amarillo\" es un cambio, no una suma: %+v", p.Lineas())
	}
}

// "también uno amarillo" con la línea en curso COMPLETA sí suma una línea nueva.
func TestFichaTambienSumaOtraLinea(t *testing.T) {
	const from = "593999200008"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "2")
	ag.anotarDelMensaje(from, "también uno amarillo")

	p, _ := store.GetPedidoEnCurso(from)
	lineas := p.Lineas()
	if len(lineas) != 2 {
		t.Fatalf("\"también amarillo\" con el blanco completo debía abrir otra línea: %+v", lineas)
	}
	if lineas[0].Color != "BLANCO" || lineas[0].Cantidad != 2 || lineas[1].Color != "AMARILLO" {
		t.Errorf("la línea cerrada se alteró: %+v", lineas)
	}
}

// Preguntar por un color NO lo agrega al pedido: "¿y el amarillo cuánto cuesta?" es una consulta.
func TestFichaPreguntaPorColorNoAbreLinea(t *testing.T) {
	const from = "593999200009"
	store := conversation.NewMemStore()
	ag := agentIncidente(nil, store)

	ag.anotarDelMensaje(from, "Blanco")
	ag.anotarDelMensaje(from, "¿y el amarillo cuánto cuesta?")

	p, _ := store.GetPedidoEnCurso(from)
	if lineas := p.Lineas(); len(lineas) != 1 || lineas[0].Color != "BLANCO" {
		t.Errorf("una pregunta por un color no puede sumarlo al pedido: %+v", p.Lineas())
	}
}

// EL PROMPT — con varias líneas el modelo ve el pedido completo, línea por línea, y la orden
// de llamar la tool UNA sola vez con items.
func TestPromptMuestraElPedidoMulticolor(t *testing.T) {
	const from = "593999200010"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{
		Color: "AMARILLO", Cantidad: 0, Flujo: conversation.FlujoInmediato,
		Items: []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}},
	})
	ag := agentIncidente(nil, store)

	_, vol := ag.construirSistema(from)
	for _, quiero := range []string{"varios colores", "color=BLANCO", "color=AMARILLO", "UNA sola vez", "cantidad de cilindros AMARILLO"} {
		if !strings.Contains(vol, quiero) {
			t.Errorf("el prompt multicolor no menciona %q:\n%s", quiero, vol)
		}
	}
}

// EL RESCATE — el candado del fantasma registra el pedido COMPLETO desde la ficha multicolor,
// no solo una línea.
func TestRescateRegistraElMulticolorCompleto(t *testing.T) {
	const from = "593999200011"
	ag, store, capturas := backendQueRegistra(t)
	clienteListoParaPedir(store, from)
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{
		Color: "AMARILLO", Cantidad: 1, Flujo: conversation.FlujoInmediato,
		Items: []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}},
	})

	tn := &turno{}
	msg, ok := ag.forzarRegistroSiHaceFalta(tn, from)
	if !ok || !tn.ultimoPedido.ok {
		t.Fatalf("el rescate no registró: %q", msg)
	}
	if len(*capturas) != 1 || len((*capturas)[0]) != 2 {
		t.Fatalf("el rescate debía mandar las 2 líneas en una llamada: %v", *capturas)
	}
	if !strings.Contains(msg, "BLANCO") || !strings.Contains(msg, "AMARILLO") {
		t.Errorf("el mensaje del rescate no detalla ambos colores: %q", msg)
	}
}

// REPETIR — un último pedido multicolor se repite ENTERO, no solo su primera línea.
func TestRepetirUnPedidoMulticolor(t *testing.T) {
	const from = "593999200012"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{
		Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 1, Fecha: "10/09/2026",
		Items: []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}, {Color: "AMARILLO", Cantidad: 1}},
	})
	ag := agentDePrueba(nil, store)

	reply, manejado := ag.ResponderRepetirPedido(from, "Repetir lo mismo")
	if !manejado {
		t.Fatal("repetir no se resolvió en código")
	}
	p, _ := store.GetPedidoEnCurso(from)
	if lineas := p.Lineas(); len(lineas) != 2 || !p.Completo() {
		t.Fatalf("la ficha no cargó el pedido completo: %+v", p.Lineas())
	}
	if !strings.Contains(reply, "BLANCO") || !strings.Contains(reply, "AMARILLO") {
		t.Errorf("el mensaje no nombra ambos colores: %q", reply)
	}
}

// LA PROGRAMACIÓN sigue siendo de UN color: con una ficha multicolor, inferirPedido devuelve
// ok=false y el rescate de programación pregunta en vez de agendar la mitad del pedido.
func TestProgramacionNoAgendaMedioMulticolor(t *testing.T) {
	const from = "593999200013"
	store := conversation.NewMemStore()
	store.SetPedidoEnCurso(from, conversation.PedidoEnCurso{
		Color: "AMARILLO", Cantidad: 1, Flujo: conversation.FlujoProgramacion, Hora: "18:30",
		Items: []conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}},
	})
	ag := agentDePrueba(nil, store)

	if _, _, ok := ag.inferirPedido(from); ok {
		t.Error("un multicolor no puede reducirse a un color para agendar: se perdería la otra línea")
	}
}
