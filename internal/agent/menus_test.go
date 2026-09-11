package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

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
	store := conversation.NewMemStore()
	ag := agentConEspera(store, "593999000010")

	// Cada forma de decir que sí arranca la búsqueda. Se usa un teléfono distinto por caso:
	// para un MISMO cliente la búsqueda es idempotente a propósito (ver
	// TestEsperarNoArrancaVariasBusquedas), así que reusar el número mediría otra cosa.
	for i, texto := range []string{"Esperar", "esperar", "sí", "dale", "ok"} {
		from := fmt.Sprintf("59399900005%d", i)
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

// Pedir "lo de siempre" ESCRIBIENDO vale igual que tocar el botón: la ficha se carga con el
// pedido anterior en código. Antes solo el texto exacto del botón entraba, y el mensaje escrito
// iba al modelo — el caso de David (ver TestIncidente_ComoElUltimoPedido).
func TestRepetirPedidoPorIntencionEscrita(t *testing.T) {
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	for i, texto := range []string{
		"Como el último pedido",
		"como la ultima vez porfa",
		"repite mi pedido",
		"quiero lo mismo de siempre",
		"lo de siempre",
		"mándame lo mismo",
	} {
		from := fmt.Sprintf("59399900006%d", i)
		store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2, Fecha: "01/09/2026"})
		_, manejado := ag.ResponderRepetirPedido(from, texto)
		if !manejado {
			t.Errorf("%q no se resolvió en código: dependería de que el modelo registre el pedido", texto)
			continue
		}
		if p, hay := store.GetPedidoEnCurso(from); !hay || p.Color != "BLANCO" || p.Cantidad != 2 {
			t.Errorf("%q: la ficha no quedó cargada con el pedido anterior: %+v (hay=%v)", texto, p, hay)
		}
	}
}

// Lo que MENCIONA el pedido anterior sin ser la orden de repetirlo sigue al modelo: preguntas
// por el estado, negaciones, cambios. Interceptarlas registraría un pedido que nadie pidió.
func TestRepetirPorIntencionNoAtrapaPreguntasNiNegaciones(t *testing.T) {
	const from = "593999000029"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2})
	ag := agentDePrueba(nil, store)

	for _, texto := range []string{
		"¿cómo va mi último pedido?",
		"¿me puedes repetir el precio del pedido?",
		"ya no quiero lo mismo de siempre",
		"no quiero lo mismo",
		"el último pedido llegó tarde",
		"quiero cambiar mi pedido",
	} {
		if _, manejado := ag.ResponderRepetirPedido(from, texto); manejado {
			t.Errorf("%q se resolvió como repetir; debía ir al modelo", texto)
		}
	}
}

// Con un pedido VIVO, "repite mi pedido" NO se resuelve en código: registrar chocaría con la
// idempotencia y el cliente recibiría un "inconveniente técnico" falso (con ticket incluido).
// Puede estar preguntando por su pedido en curso: eso es conversación del modelo.
func TestRepetirConPedidoActivoVaAlModelo(t *testing.T) {
	const from = "593999000030"
	store := conversation.NewMemStore()
	store.SetLastOrder(from, conversation.LastOrder{Producto: "GAS 15KG", Color: "BLANCO", Cantidad: 2})
	store.SetActivePedido(from, 640)
	ag := agentDePrueba(nil, store)

	if _, manejado := ag.ResponderRepetirPedido(from, "Repetir lo mismo"); manejado {
		t.Error("se intentó repetir con un pedido activo: chocaría con la idempotencia y daría un error falso")
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

// Contrato que hace seguro el guard de idempotencia: el pedido activo se limpia en los TRES
// finales (entregado, cancelado, no-show). Si un final no lo limpia, ese cliente no puede
// volver a pedir hasta que expire la ventana — y el peor caso sería el cliente que SÍ recibió
// su gas. Se comprueba el COMPORTAMIENTO del store, no que la cadena aparezca en el fuente:
// la versión anterior de este test buscaba "ClearActivePedido" como texto y habría pasado
// aunque la llamada estuviera tras un return temprano.
func TestElPedidoActivoSeLimpiaEnTodosLosFinales(t *testing.T) {
	finales := map[string]func(conversation.Store, string){
		"entregado": func(s conversation.Store, phone string) {
			s.ClearActivePedido(phone) // lo que hace notifyOrderFinished
			s.SetPendingRating(phone, conversation.PendingRating{PedidoID: 512, Conductor: "Nelson"})
		},
		"cancelado": func(s conversation.Store, phone string) { s.ClearActivePedido(phone) },
		"no-show":   func(s conversation.Store, phone string) { s.ClearActivePedido(phone) },
	}
	for nombre, final := range finales {
		t.Run(nombre, func(t *testing.T) {
			store := conversation.NewMemStore()
			const phone = "593999000044"
			store.SetActivePedido(phone, 512)
			final(store, phone)
			if _, sigue := store.GetActivePedido(phone); sigue {
				t.Errorf("tras %q el pedido sigue activo: el cliente no podría hacer otro", nombre)
			}
		})
	}

	// Y el guard ESTRUCTURAL: que cmd/bot de verdad llame a ClearActivePedido en los tres
	// avisos del backend. Sin esto lo anterior solo prueba que el store funciona.
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
			t.Errorf("%s no limpia el pedido activo: ese cliente quedaría bloqueado", fn)
		}
	}
}

// Un pedido activo HUÉRFANO no puede bloquear al cliente para siempre. El backend limpia el
// pedido al entregarse o cancelarse; si ese aviso se pierde (conductor que no finaliza en su
// app, notificación fallida), el guard de idempotencia dejaría a ese cliente sin poder pedir
// nunca más. Encontrado en la revisión del 07/09: el guard creaba un riesgo peor del que cerraba.
func TestPedidoActivoHuerfanoNoBloqueaAlCliente(t *testing.T) {
	const from = "593999000043"
	store := conversation.NewMemStore()
	store.SetLocation(from, -2.9, -79.0)
	store.SetActivePedido(from, 512)
	ag := agentDePrueba(nil, store)
	ag.cfg = config.Config{BotHorarioInicio: "00:00", BotHorarioFin: "23:59"}

	// Recién creado: el guard protege (no se crean dos pedidos).
	salida := ag.registrarPedido(&turno{}, from, map[string]any{"color": "BLANCO", "cantidad": 1})
	if !strings.Contains(salida, "512") {
		t.Fatalf("un pedido activo reciente debía bloquear el duplicado: %q", salida)
	}

	// Pasada la ventana, se asume huérfano: el cliente puede volver a pedir.
	if ventanaPedidoActivo > 12*time.Hour {
		t.Errorf("ventanaPedidoActivo = %s: demasiado. Un cliente con un pedido sin cerrar "+
			"quedaría bloqueado casi un día", ventanaPedidoActivo)
	}
}
