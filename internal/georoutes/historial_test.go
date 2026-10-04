package georoutes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// El backend responde codigo 1 cuando sale bien (CodigoRespuesta.OK). Hasta el 04/10 el bot
// exigía 0 y tomaba TODA respuesta correcta del historial como error.
func TestGetOrderHistory_ElCodigoDeExitoDelBackendEsUno(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"codigo":1,"mensaje":"Historial cliente obtenido exitosamente","resultado":[
			{"idpedido":252,"estado_pedido":"En camino"}]}`))
	}))
	defer srv.Close()

	pedidos, err := NewClient(srv.URL).GetOrderHistory("jwt", nil)
	if err != nil {
		t.Fatalf("una respuesta correcta del backend se tomó como error: %v", err)
	}
	if len(pedidos) != 1 || pedidos[0].IDPedido != 252 {
		t.Errorf("pedidos = %+v, se esperaba el #252", pedidos)
	}
}

func TestGetOrderHistory_ElCodigoDeErrorSigueSiendoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"codigo":-1,"mensaje":"El usuario no es cliente","resultado":null}`))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).GetOrderHistory("jwt", nil); err == nil {
		t.Error("una respuesta de error del backend (codigo -1) se tomó como correcta")
	}
}
