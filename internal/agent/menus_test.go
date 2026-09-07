package agent

import (
	"os"
	"strings"
	"testing"

	"wp-llm-gas/internal/catalog"
	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

// agentConEspera arma un Agent con un pedido EN ESPERA de repartidor, que es la precondición
// del menú "¿Deseas esperar?". El backend apunta a una URL muerta a propósito: estos tests
// miden QUÉ decide el interceptor, no qué responde el backend.
func agentConEspera(store conversation.Store, from string) *Agent {
	store.SetPendingWait(from, conversation.PendingWait{
		IDCategoria: 1, IDProducto: 7, IDColor: 3, Cantidad: 2, IDTipoPago: 1,
		ProductoNombre: "GAS 15KG", ColorNombre: "Blanco",
		Identificacion: "0104816269", Nombres: "María Elena",
	})
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: "07:00", BotHorarioFin: "19:00"}
	return ag
}

// "Esperar" arranca la búsqueda de repartidor SIN pasar por el modelo. Si esto dependiera del
// modelo y no llamara a la herramienta, el cliente se quedaría esperando un gas que nadie busca.
func TestMenuEsperaAceptar(t *testing.T) {
	const from = "593999000010"
	store := conversation.NewMemStore()
	ag := agentConEspera(store, from)

	for _, texto := range []string{"Esperar", "esperar", "sí", "dale", "ok"} {
		store.SetPendingWait(from, conversation.PendingWait{
			IDCategoria: 1, IDProducto: 7, IDColor: 3, Cantidad: 2, IDTipoPago: 1,
			ProductoNombre: "GAS 15KG", ColorNombre: "Blanco",
		})
		antes := ag.esperasArrancadas.Load()
		reply, manejado := ag.ResponderMenuEspera(from, texto)
		if !manejado {
			t.Fatalf("%q no se resolvió en código: el cliente quedaría a merced de que el modelo "+
				"llame a esperar_conductor", texto)
		}
		// Lo que importa no es el texto: es que la BÚSQUEDA de repartidor haya arrancado de
		// verdad. Un bot que contesta "ya estoy buscando" sin buscar es el bug que esto previene.
		if ag.esperasArrancadas.Load() == antes {
			t.Fatalf("%q: se le dijo al cliente que se busca repartidor pero startWaitForDriver "+
				"NO arrancó: se quedaría esperando un gas que nadie busca", texto)
		}
		if !strings.Contains(reply, "buscando un repartidor") {
			t.Errorf("%q: la respuesta no le dice al cliente que se está buscando: %q", texto, reply)
		}
	}
}

// "Cancelar" cierra la espera en código y ofrece programar (el pedido queda como no asignado).
func TestMenuEsperaCancelar(t *testing.T) {
	const from = "593999000011"
	store := conversation.NewMemStore()
	ag := agentConEspera(store, from)

	reply, manejado := ag.ResponderMenuEspera(from, "Cancelar")
	if !manejado {
		t.Fatal("\"Cancelar\" no se resolvió en código")
	}
	if _, sigue := store.GetPendingWait(from); sigue {
		t.Error("la espera sigue viva tras cancelar: el bot seguiría buscando un repartidor que el cliente ya no quiere")
	}
	if !strings.Contains(reply, "07:00") || !strings.Contains(reply, "19:00") {
		t.Errorf("no se le ofreció el horario para programar: %q", reply)
	}
}

// Lo que NO es una opción del menú va al modelo: una pregunta, un cambio de tema, un matiz.
func TestMenuEsperaDejaPasarLoQueNoEsOpcion(t *testing.T) {
	const from = "593999000012"
	store := conversation.NewMemStore()
	ag := agentConEspera(store, from)

	for _, texto := range []string{
		"¿cuánto cuesta?",
		"esperar pero hasta las 6",
		"Programar",
		"mejor mándame dos",
		"",
	} {
		if _, manejado := ag.ResponderMenuEspera(from, texto); manejado {
			t.Errorf("%q se resolvió en código; debía ir al modelo (no es una opción del menú)", texto)
		}
	}
}

// Sin pedido en espera, el interceptor no se mete: un "sí" cualquiera de la conversación no
// puede disparar una búsqueda de repartidor.
func TestMenuEsperaNoActuaSinEsperaPendiente(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	if _, manejado := ag.ResponderMenuEspera("593999000013", "Esperar"); manejado {
		t.Error("se resolvió un menú de espera que nunca se mostró")
	}
}

// Un número del 1 al 5 con un pedido por calificar se registra en código.
func TestCalificacionNumeroSuelto(t *testing.T) {
	const from = "593999000014"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	for _, texto := range []string{"5", "1", " 4 "} {
		store.SetPendingRating(from, conversation.PendingRating{PedidoID: 512, Conductor: "Nelson"})
		_, manejado := ag.ResponderCalificacion(from, texto)
		if !manejado {
			t.Errorf("%q no se resolvió en código: la calificación dependería de que el modelo llame la tool", texto)
		}
		if _, sigue := store.GetPendingRating(from); sigue {
			t.Errorf("%q: el pendiente de calificación quedó vivo; se le volvería a pedir en cada conversación", texto)
		}
	}
}

// Con comentario, o fuera de rango, decide el modelo: ahí hay algo que vale la pena guardar
// o aclarar, y el interceptor no debe tragárselo.
func TestCalificacionConComentarioVaAlModelo(t *testing.T) {
	const from = "593999000015"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	for _, texto := range []string{"5 muy amable", "le pongo 4 pero llegó tarde", "0", "6", "excelente", ""} {
		store.SetPendingRating(from, conversation.PendingRating{PedidoID: 512, Conductor: "Nelson"})
		if _, manejado := ag.ResponderCalificacion(from, texto); manejado {
			t.Errorf("%q se resolvió en código; debía ir al modelo", texto)
		}
	}
}

// Sin calificación pendiente, un número suelto es cualquier cosa (una cantidad, por ejemplo)
// y NO puede interpretarse como una nota al repartidor.
func TestCalificacionNoActuaSinPendiente(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	if _, manejado := ag.ResponderCalificacion("593999000016", "2"); manejado {
		t.Error("un \"2\" sin calificación pendiente se tomó como nota; podría ser la cantidad de un pedido")
	}
}

// "Repetir lo mismo" sin ubicación: carga la ficha con el pedido anterior y pide la ubicación.
// El dato NO puede depender de que el modelo recuerde qué pidió el cliente la última vez.
func TestRepetirPedidoCargaLaFicha(t *testing.T) {
	const from = "593999000020"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "01/09/2026"})
	ag := agentDePrueba(nil, store)

	reply, manejado := ag.ResponderRepetirPedido(from, "Repetir lo mismo")
	if !manejado {
		t.Fatal("\"Repetir lo mismo\" no se resolvió en código")
	}
	p, hay := store.GetPedidoEnCurso(from)
	if !hay || p.Color != "BLANCO" || p.Cantidad != 2 {
		t.Fatalf("el pedido anterior no quedó en la ficha: %+v (hay=%v)", p, hay)
	}
	if !strings.Contains(reply, "ubicación") {
		t.Errorf("sin ubicación guardada hay que pedirla: %q", reply)
	}
}

// Sin último pedido no hay nada que repetir: va al modelo, no se inventa un pedido.
func TestRepetirPedidoSinPedidoAnteriorVaAlModelo(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	if _, manejado := ag.ResponderRepetirPedido("593999000021", "Repetir lo mismo"); manejado {
		t.Error("se resolvió un \"repetir\" sin pedido anterior: el bot inventaría un pedido")
	}
}

// "Cambiar el pedido" y cualquier otro texto siguen al modelo: ahí empieza una conversación.
func TestRepetirPedidoDejaPasarLoQueNoEsLaOpcion(t *testing.T) {
	const from = "593999000022"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2})
	ag := agentDePrueba(nil, store)

	for _, texto := range []string{"Cambiar el pedido", "repetir pero 3", "hola", ""} {
		if _, manejado := ag.ResponderRepetirPedido(from, texto); manejado {
			t.Errorf("%q se resolvió en código; debía ir al modelo", texto)
		}
	}
}

// El color que el cliente elige en el menú queda en la ficha sin pasar por el modelo (T0.5.4:
// lo resuelve anotarDelMensaje, que corre en cada turno antes de llamar al modelo).
func TestColorDelMenuQuedaEnLaFicha(t *testing.T) {
	const from = "593999000023"
	store := conversation.NewMemStore()
	cat := catalog.NewStaticForTest(&catalog.Context{
		Products: []georoutes.Product{{IDProducto: 1, Nombre: "GAS 15KG", Colores: []georoutes.Color{
			{ID: 10, Nombre: "BLANCO"}, {ID: 11, Nombre: "AMARILLO"},
		}}},
	})
	ag := agentDePrueba(nil, store)
	ag.catalog = cat

	ag.anotarDelMensaje(from, "Amarillo") // el cliente tocó el botón del menú de colores

	p, hay := store.GetPedidoEnCurso(from)
	if !hay || p.Color != "AMARILLO" {
		t.Fatalf("el color del menú no quedó guardado: %+v (hay=%v)", p, hay)
	}
}

// IDEMPOTENCIA: con un pedido activo NO se crea otro. El modelo llamando dos veces a la
// herramienta, o un mensaje repetido, dejaba al cliente con dos cilindros y dos conductores.
func TestNoSeRegistraDosVecesConPedidoActivo(t *testing.T) {
	const from = "593999000040"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	store.SetActivePedido(from, 512) // ya tiene un pedido en curso
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}

	salida := ag.registrarPedido(&turno{}, from, map[string]any{"color": "BLANCO", "cantidad": 1})
	if !strings.Contains(salida, "512") || !strings.Contains(salida, "NO se creó otro") {
		t.Errorf("se intentó crear un segundo pedido teniendo el #512 activo: %q", salida)
	}
}

// Contrato que hace seguro el guard anterior: el pedido activo se limpia en los TRES finales
// (entregado, cancelado, no-show). Si un final no lo limpia, ese cliente no puede volver a
// pedir nunca — y el peor caso sería el cliente que SÍ recibió su gas.
func TestElPedidoActivoSeLimpiaEnTodosLosFinales(t *testing.T) {
	src, err := os.ReadFile("../../cmd/bot/main.go")
	if err != nil {
		t.Fatalf("no se pudo leer main.go: %v", err)
	}
	cuerpo := string(src)
	for _, fn := range []string{"notifyOrderFinished", "notifyOrderCancelled", "notifyOrderNoShow"} {
		ini := strings.Index(cuerpo, "func "+fn+"(")
		if ini < 0 {
			t.Errorf("no se encontró %s en main.go", fn)
			continue
		}
		trozo := cuerpo[ini:]
		if fin := strings.Index(trozo[10:], "\nfunc "); fin > 0 {
			trozo = trozo[:fin+10]
		}
		if !strings.Contains(trozo, "ClearActivePedido") {
			t.Errorf("%s no limpia el pedido activo: ese cliente no podría hacer otro pedido nunca", fn)
		}
	}
}
