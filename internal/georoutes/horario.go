package georoutes

import (
	"encoding/json"
	"fmt"
)

// HORARIO DE ATENCIÓN: UNA SOLA FUENTE DE VERDAD (specs/horarios-franjas-y-rubros.md §3).
//
// El problema que resuelve. Hasta el 14/10 el horario vivía en TRES sitios con valores DISTINTOS:
// la tabla HorarioAtencion del backend (la app móvil, 07:00-19:00), el .env de este bot
// (07:00-20:30) y los parámetros de "apagar al repartidor quieto" (noche = 20:00-06:00). Un
// miércoles a las 22:00 la app dejaba agendar y el bot decía que estábamos cerrados: el mismo
// negocio daba dos respuestas opuestas según el canal. Y ampliar la atención hasta las 22:00
// obligaba a tocar tres sitios, uno de ellos con despliegue — con lo que el bot seguiría diciendo
// 19:00 mientras el negocio atiende hasta las 22:00.
//
// La fuente es el backend, que es donde lo edita el panel. Este cliente consume el MISMO endpoint
// que ya usa la app móvil (`/georoutes/horarioAgendado/`, público y sin JWT): no se crea un camino
// paralelo ni se toca nada de lo que la app ya consume en producción.

// DiaHorario es lo que se atiende un día de la semana. `Dia` viene en la numeración de Python
// (lunes=0 ... domingo=6), que es la que usa el backend; se convierte a ISO al consultarlo.
type DiaHorario struct {
	Dia     int    `json:"dia"`
	Nombre  string `json:"nombre"`
	Abierto bool   `json:"abierto"`
	Desde   string `json:"desde"` // "07:00"; vacío cuando está cerrado
	Hasta   string `json:"hasta"` // "22:00"; vacío cuando está cerrado
}

// HorarioSemanal es el horario de los siete días tal como lo publica el backend.
type HorarioSemanal struct {
	Dias []DiaHorario `json:"dias"`
}

// GetHorarioAtencion trae el horario de atención del backend (GET /horarioAgendado/).
//
// Devuelve error si el backend no contesta o si manda algo que no se entiende; quien llama cae a
// su respaldo. Un horario a medias es peor que ninguno: con tres días cargados el bot cerraría el
// negocio los otros cuatro.
func (c *Client) GetHorarioAtencion() (*HorarioSemanal, error) {
	res, err := c.get("/horarioAgendado/")
	if err != nil {
		return nil, err
	}
	var data HorarioSemanal
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, fmt.Errorf("horario no válido del backend: %w", err)
	}
	// Se exigen los SIETE días. El backend los manda siempre (recorre HorarioAtencion.DIAS
	// completo, y los días sin fila van como abierto=false), así que menos de siete significa que
	// algo se truncó por el camino y no que el negocio cierre.
	if len(data.Dias) != 7 {
		return nil, fmt.Errorf("el backend devolvió %d día(s) de horario en vez de 7", len(data.Dias))
	}
	return &data, nil
}
