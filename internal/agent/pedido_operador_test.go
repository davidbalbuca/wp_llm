// PEDIDO CREADO DESDE EL PANEL SIN CÉDULA (05/10).
//
// Desde el 04/10 el cliente se registra por su número de WhatsApp, sin cédula. El formulario de
// "Crear pedido" de Conversaciones seguía pidiendo cédula y nombre, y el nombre que escribía el
// operador se ignoraba: con un perfil como "@sd2" el pedido fallaba aunque lo hubiera escrito.
// Las pruebas se cortan en la ubicación (no la hay): basta para ver con qué datos se registró.
package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
	"wp-llm-gas/internal/georoutes"
)

type registroFalso struct {
	llamadas []map[string]any
}

func (r *registroFalso) servidor(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/wppGetOrCreateClient/") {
			t.Errorf("llamada inesperada al backend: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var cuerpo map[string]any
		_ = json.NewDecoder(req.Body).Decode(&cuerpo)
		r.llamadas = append(r.llamadas, cuerpo)
		_, _ = w.Write([]byte(`{"codigo":200,"mensaje":"ok","resultado":{"username":"wa593","password":"p"}}`))
	}))
}

func agenteOperador(t *testing.T, perfilWhatsApp string) (*Agent, *registroFalso, string) {
	reg := &registroFalso{}
	srv := reg.servidor(t)
	t.Cleanup(srv.Close)
	store := conversation.NewMemStore()
	const from = "593984000020"
	if perfilWhatsApp != "" {
		store.SetPerfilWhatsApp(from, perfilWhatsApp)
	}
	return &Agent{store: store, gr: georoutes.NewClient(srv.URL)}, reg, from
}

func pedidoDePrueba(from, cedula, nombres string) PedidoDeOperador {
	return PedidoDeOperador{Phone: from, Identificacion: cedula, Nombres: nombres, IDTipoPago: 1,
		Items: []conversation.PendingWaitItem{{IDCategoria: 1, IDProducto: 1, IDColor: 10, Cantidad: 1}}}
}

func TestOperadorSinCedulaRegistraConElNombreQueEscribio(t *testing.T) {
	a, reg, from := agenteOperador(t, "@sd2")

	res := a.CrearPedidoDeOperador(pedidoDePrueba(from, "", "Rosa Quizhpi"))

	if len(reg.llamadas) != 1 {
		t.Fatalf("debía registrar al cliente una vez; llamadas = %d (resultado: %+v)", len(reg.llamadas), res)
	}
	if got := reg.llamadas[0]["nombres"]; got != "Rosa Quizhpi" {
		t.Errorf("nombre enviado = %v; quería el que escribió el operador", got)
	}
	if got := reg.llamadas[0]["identificacion"]; got != "" {
		t.Errorf("sin cédula no debe inventarse una; enviada = %v", got)
	}
	if p, _ := a.store.GetProfile(from); p.Nombres != "Rosa Quizhpi" || p.PerfilWhatsApp != "@sd2" {
		t.Errorf("perfil guardado = %+v; quería el nombre nuevo sin perder el de WhatsApp", p)
	}
	// Sigue de largo hasta la ubicación: la falta de cédula ya no lo frena.
	if !strings.Contains(res.Motivo, "ubicación") {
		t.Errorf("motivo = %q; quería que se detuviera recién en la ubicación", res.Motivo)
	}
}

func TestOperadorSinNombreUsaElDeWhatsAppSiSirve(t *testing.T) {
	a, reg, from := agenteOperador(t, "Marco Pérez")

	a.CrearPedidoDeOperador(pedidoDePrueba(from, "", ""))

	if len(reg.llamadas) != 1 || reg.llamadas[0]["nombres"] != "Marco Pérez" {
		t.Fatalf("quería registrarlo con el nombre de su WhatsApp; llamadas = %+v", reg.llamadas)
	}
}

func TestOperadorSinNombreNiPerfilUtilPideElNombre(t *testing.T) {
	a, reg, from := agenteOperador(t, "@sd2")

	res := a.CrearPedidoDeOperador(pedidoDePrueba(from, "", ""))

	if res.OK || !strings.Contains(res.Motivo, "falta el nombre") {
		t.Errorf("motivo = %q; quería que pidiera el nombre", res.Motivo)
	}
	if len(reg.llamadas) != 0 {
		t.Errorf("no debía registrar a nadie sin nombre; llamadas = %+v", reg.llamadas)
	}
}

func TestOperadorNombreConNumerosSeRechaza(t *testing.T) {
	a, reg, from := agenteOperador(t, "Marco Pérez")

	res := a.CrearPedidoDeOperador(pedidoDePrueba(from, "", "@sd2"))

	if res.OK || !strings.Contains(res.Motivo, "no parece un nombre") {
		t.Errorf("motivo = %q; quería que rechazara el nombre escrito", res.Motivo)
	}
	if len(reg.llamadas) != 0 {
		t.Errorf("no debía registrar con un nombre inválido; llamadas = %+v", reg.llamadas)
	}
}

func TestOperadorConCuentaCorrigeElNombre(t *testing.T) {
	a, reg, from := agenteOperador(t, "@sd2")
	a.store.SetAccount(from, conversation.Account{Username: "wa593", Password: "p"})

	a.CrearPedidoDeOperador(pedidoDePrueba(from, "", "Rosa Quizhpi"))

	if len(reg.llamadas) != 1 || reg.llamadas[0]["actualizar_nombre"] != true ||
		reg.llamadas[0]["nombres"] != "Rosa Quizhpi" {
		t.Fatalf("quería que corrigiera el nombre en el backend; llamadas = %+v", reg.llamadas)
	}
}

func TestOperadorDejaElNombreRegistradoConCedulaSinTocarlo(t *testing.T) {
	a, reg, from := agenteOperador(t, "")
	// Registrado con cédula: su nombre se usa tal cual aunque no pase el filtro ("Cliente ...").
	a.store.SetProfile(from, conversation.Profile{Identificacion: "0102030405", Nombres: "Cliente Prueba"})
	a.store.SetAccount(from, conversation.Account{Username: "0102030405_x", Password: "p"})

	res := a.CrearPedidoDeOperador(pedidoDePrueba(from, "", "Cliente Prueba"))

	if strings.Contains(res.Motivo, "no parece un nombre") || len(reg.llamadas) != 0 {
		t.Errorf("el nombre prellenado no debía rechazarse ni actualizarse; motivo = %q, llamadas = %+v",
			res.Motivo, reg.llamadas)
	}
}
