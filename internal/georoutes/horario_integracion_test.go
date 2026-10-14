package georoutes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// El cliente entiende la RESPUESTA REAL del backend, no una inventada.
//
// El payload de abajo se copió tal cual de `GET /georoutes/horarioAgendado/` del entorno de
// desarrollo (14/10/2026). Vale la pena que sea literal: el envoltorio (`codigo`/`mensaje`/
// `resultado`), los nombres sin tilde que manda Django ("Miercoles") y la numeración de días de
// Python (lunes=0) son detalles que un payload escrito a mano se inventaría distinto, y el bot
// fallaría solo contra el backend de verdad.
//
// Ese miércoles de 00:00 a 23:30 no es un ejemplo bonito: es el dato que había en dev, y es
// exactamente el que provocaba la contradicción con el bot (que cerraba a las 20:30 por su .env).
const respuestaRealDeDev = `{"codigo":1,"mensaje":"Horario obtenido exitosamente","resultado":{
  "dias":[
    {"dia":0,"nombre":"Lunes","abierto":true,"desde":"07:00","hasta":"19:00"},
    {"dia":1,"nombre":"Martes","abierto":true,"desde":"07:00","hasta":"19:00"},
    {"dia":2,"nombre":"Miercoles","abierto":true,"desde":"00:00","hasta":"23:30"},
    {"dia":3,"nombre":"Jueves","abierto":true,"desde":"00:00","hasta":"23:30"},
    {"dia":4,"nombre":"Viernes","abierto":true,"desde":"07:00","hasta":"19:00"},
    {"dia":5,"nombre":"Sabado","abierto":true,"desde":"07:00","hasta":"19:00"},
    {"dia":6,"nombre":"Domingo","abierto":true,"desde":"07:00","hasta":"19:00"}],
  "dias_adelante":7,"anticipacion_minutos":2,"aviso_minutos":60}}`

func clienteContra(t *testing.T, cuerpo string, estado int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/georoutes/horarioAgendado/" {
			t.Errorf("el bot pidió %q; se esperaba /georoutes/horarioAgendado/ (el MISMO endpoint "+
				"que ya consume la app móvil, no uno nuevo)", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(estado)
		_, _ = w.Write([]byte(cuerpo))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL)
}

func TestElClienteEntiendeElHorarioRealDeDev(t *testing.T) {
	h, err := clienteContra(t, respuestaRealDeDev, http.StatusOK).GetHorarioAtencion()
	if err != nil {
		t.Fatalf("no se pudo leer el horario real de dev: %v", err)
	}
	if len(h.Dias) != 7 {
		t.Fatalf("se leyeron %d días, se esperaban 7", len(h.Dias))
	}

	// El lunes, con la numeración de Python que usa el backend.
	lunes := h.Dias[0]
	if lunes.Dia != 0 || !lunes.Abierto || lunes.Desde != "07:00" || lunes.Hasta != "19:00" {
		t.Errorf("lunes mal leído: %+v", lunes)
	}
	// El miércoles largo: el dato que de verdad había en dev.
	miercoles := h.Dias[2]
	if miercoles.Dia != 2 || miercoles.Desde != "00:00" || miercoles.Hasta != "23:30" {
		t.Errorf("miércoles mal leído: %+v", miercoles)
	}
}

// Menos de siete días significa que algo se truncó, NO que el negocio cierre. Si se aceptara un
// horario a medias, el bot daría por cerrados los días que faltan y rechazaría pedidos buenos.
func TestUnHorarioIncompletoSeRechaza(t *testing.T) {
	parcial := `{"codigo":1,"mensaje":"ok","resultado":{"dias":[
	  {"dia":0,"nombre":"Lunes","abierto":true,"desde":"07:00","hasta":"19:00"}]}}`
	if _, err := clienteContra(t, parcial, http.StatusOK).GetHorarioAtencion(); err == nil {
		t.Error("un horario con 1 día se aceptó; tiene que fallar para que el bot caiga al .env")
	}
}

// Un error del backend se propaga como error, para que quien llama use su respaldo en vez de
// quedarse sin horario.
func TestUnErrorDelBackendSePropaga(t *testing.T) {
	roto := `{"codigo":0,"mensaje":"explotó algo"}`
	if _, err := clienteContra(t, roto, http.StatusBadRequest).GetHorarioAtencion(); err == nil {
		t.Error("un 400 del backend no devolvió error")
	}
}
