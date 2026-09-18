package agent

import (
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// AL CLIENTE SE LE NOMBRA EL DESTINO COMO ÉL LO GUARDÓ.
//
// Pedido del dueño (18/09): "revisar lo del detalle de la última ubicación, que deba decir la
// dirección/calles que están registradas en el backend, o si ya tiene nombre decir eso".
//
// El hueco que esto cierra: el menú "¿te lo enviamos a X?" mostraba SOLO la calle. Un cliente que
// había guardado su ubicación como "Casa" recibía "¿te lo enviamos a Av. Solano 123?" y tenía que
// reconocer su propia dirección escrita por el geocodificador — cuando le había puesto un nombre
// justamente para no tener que hacer eso.

// agenteQueCapturaElMenu arma un agente con las direcciones guardadas del cliente y devuelve un
// puntero al CUERPO del último menú que se mandó.
//
// Se mira el cuerpo del menú y no el valor que devuelve la función porque el cuerpo es lo que de
// verdad LEE el cliente: la función puede devolver lo correcto y el menú salir con otro texto.
// Reutiliza backendConDirecciones (destino_test.go), que ya levanta el backend falso.
func agenteQueCapturaElMenu(t *testing.T, dirs string) (*Agent, conversation.Store, *string) {
	t.Helper()
	var cuerpoMenu string
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)
	ag.gr = backendConDirecciones(t, dirs)
	ag.enviarMenu = func(from, cuerpo string, opciones []string) error {
		cuerpoMenu = cuerpo
		return nil
	}
	return ag, store, &cuerpoMenu
}

const direccionCasa = `[{"id":1,"alias":"Casa","direccion":"Av. Solano 123",
    "latitude":-2.898,"longitude":-79.002}]`

// EL CASO PRINCIPAL: con la ubicación guardada como "Casa", el menú dice Casa Y la calle.
func TestElMenuDeDireccionDiceElNombreQuePusoElCliente(t *testing.T) {
	const from = "593999500001"
	ag, store, cuerpo := agenteQueCapturaElMenu(t, direccionCasa)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	store.SetLocation(from, -2.898, -79.002) // la misma ubicación que tiene guardada como Casa
	store.SetDireccionTexto(from, "Av. Solano 123")

	_, manejado := ag.pedirConfirmacionDireccionLineas(&turno{}, from,
		[]conversation.ItemPedido{{Color: "BLANCO", Cantidad: 2}})
	if !manejado {
		t.Fatal("no se pidió la confirmación de dirección")
	}
	if !strings.Contains(*cuerpo, "Casa") {
		t.Errorf("el menú no dice el NOMBRE que el cliente le puso a su ubicación: %q", *cuerpo)
	}
	if !strings.Contains(*cuerpo, "Av. Solano 123") {
		t.Errorf("el menú no dice la calle registrada en el backend, así que el cliente no puede "+
			"verificar a dónde va su gas: %q", *cuerpo)
	}
}

// Sin nombre guardado, la calle sola sigue sirviendo: es el comportamiento de antes y no se rompe.
func TestSinNombreGuardadoElMenuUsaLaCalle(t *testing.T) {
	const from = "593999500002"
	ag, store, cuerpo := agenteQueCapturaElMenu(t, `[]`)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	store.SetLocation(from, -2.898, -79.002)
	store.SetDireccionTexto(from, "Av. Solano 123")

	if _, manejado := ag.pedirConfirmacionDireccionLineas(&turno{}, from,
		[]conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}}); !manejado {
		t.Fatal("no se pidió la confirmación de dirección")
	}
	if !strings.Contains(*cuerpo, "Av. Solano 123") {
		t.Errorf("sin nombre, el menú debe decir la calle: %q", *cuerpo)
	}
}

// EL NOMBRE SE BUSCA POR COORDENADAS, no se toma el primero de la lista. El cliente puede tener
// Casa y Trabajo: si el pedido va al Trabajo y el menú dice "Casa", confirma un destino y el gas
// va a otro. Es el error más caro del negocio, y el que más tarde se descubre.
func TestElNombreSeBuscaPorCoordenadasNoSeTomaElPrimero(t *testing.T) {
	const from = "593999500003"
	const dosDirecciones = `[
        {"id":1,"alias":"Casa","direccion":"Av. Solano 123","latitude":-2.898,"longitude":-79.002},
        {"id":2,"alias":"Trabajo","direccion":"Av. Loja 456","latitude":-2.920,"longitude":-79.040}
    ]`
	ag, store, cuerpo := agenteQueCapturaElMenu(t, dosDirecciones)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	// El cliente está en el TRABAJO (la segunda de la lista).
	store.SetLocation(from, -2.920, -79.040)
	store.SetDireccionTexto(from, "Av. Loja 456")

	if _, manejado := ag.pedirConfirmacionDireccionLineas(&turno{}, from,
		[]conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}}); !manejado {
		t.Fatal("no se pidió la confirmación de dirección")
	}
	if strings.Contains(*cuerpo, "Casa") {
		t.Errorf("el menú nombró la PRIMERA dirección en vez de la que corresponde a las "+
			"coordenadas: el cliente confirmaría un destino y el gas iría a otro. Menú: %q", *cuerpo)
	}
	if !strings.Contains(*cuerpo, "Trabajo") {
		t.Errorf("el menú no nombró la dirección que corresponde a la ubicación actual: %q", *cuerpo)
	}
}

// El alias INTERNO del backend ('WhatsApp', que el bot pisa en cada pedido) no es un nombre que el
// cliente eligiera: no puede aparecer en el menú.
func TestElAliasInternoNoLlegaAlMenu(t *testing.T) {
	const from = "593999500004"
	const soloInterna = `[{"id":1,"alias":"WhatsApp","direccion":"Av. Solano 123",
        "latitude":-2.898,"longitude":-79.002}]`
	ag, store, cuerpo := agenteQueCapturaElMenu(t, soloInterna)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	store.SetLocation(from, -2.898, -79.002)
	store.SetDireccionTexto(from, "Av. Solano 123")

	if _, manejado := ag.pedirConfirmacionDireccionLineas(&turno{}, from,
		[]conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}}); !manejado {
		t.Fatal("no se pidió la confirmación de dirección")
	}
	if strings.Contains(*cuerpo, "WhatsApp") {
		t.Errorf("el alias interno del backend se le mostró al cliente como destino: %q", *cuerpo)
	}
}

// Y si lo único que hay es el texto de respaldo (las coordenadas disfrazadas de dirección), NO se
// pregunta nada: se le pide la ubicación, como antes. Mostrarle "-2.9, -79.0" no le dice nada.
func TestConSoloElTextoDeRespaldoNoSePreguntaLaDireccion(t *testing.T) {
	const from = "593999500005"
	ag, store, _ := agenteQueCapturaElMenu(t, `[]`)
	store.SetAccount(from, conversation.Account{Username: "u", Password: "p", JWT: "jwt"})
	store.SetLocation(from, -2.898, -79.002)
	store.SetDireccionTexto(from, "Ubicación compartida por WhatsApp (-2.9, -79.0)")

	if _, manejado := ag.pedirConfirmacionDireccionLineas(&turno{}, from,
		[]conversation.ItemPedido{{Color: "BLANCO", Cantidad: 1}}); manejado {
		t.Error("se le pidió confirmar unas coordenadas disfrazadas de dirección: no identifican " +
			"nada y el cliente confirmaría a ciegas")
	}
}

// Y la pieza está CABLEADA: el menú se arma con destinoLegible, no con la calle suelta. Un test
// que solo llame a la función pasa igual aunque el menú siga usando lo de antes.
func TestElMenuDeDireccionUsaDestinoLegible(t *testing.T) {
	if !archivoContiene(t, "direccion.go", "destinoLegible(a.aliasDeLaUbicacionGuardada(from)") {
		t.Error("el menú de dirección no usa destinoLegible: volvería a mostrar la calle sin el " +
			"nombre que el cliente le puso a su ubicación")
	}
}
