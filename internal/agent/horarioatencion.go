package agent

import (
	"fmt"
	"strings"
	"time"
)

// EL HORARIO LO MANDA EL BACKEND (specs/horarios-franjas-y-rubros.md §3).
//
// Antes del 14/10 este bot decidía el horario con BOT_HORARIO_INICIO/FIN y BOT_DIAS_LABORABLES de
// su .env, y la app móvil lo decidía con la tabla HorarioAtencion del backend. Eran DOS fuentes
// con valores distintos (bot 07:00-20:30, app 07:00-19:00) y se contradecían en producción: un
// miércoles a las 22:00 la app dejaba agendar y el bot decía que estábamos cerrados.
//
// Eso vuelve a pasar en cuanto el horario cambie: si el negocio amplía hasta las 22:00 y el valor
// vive en el .env del bot, el bot sigue diciendo 19:00 hasta que alguien recuerde redesplegarlo.
// Por eso la fuente es UNA: la tabla que edita el panel, que ya publica
// `/georoutes/horarioAgendado/` y que ya consume la app. No se tocó ese endpoint ni se creó otro.
//
// El .env se queda como RESPALDO para cuando el backend no contesta. Un bot que no sabe el
// horario no puede quedarse mudo ni, peor, aceptar pedidos a las tres de la mañana.
//
// Todo lo de este archivo es SOLO "de qué hora a qué hora y qué días". Lo que el bot HACE fuera
// de horario (ofrecer programar, el saludo nocturno, el candado de registrar_pedido) no cambió.

// ventanaDeAtencion es lo que se atiende un día concreto, ya en minutos desde medianoche para
// poder comparar sin volver a parsear. (No se llama "franja": ese nombre ya lo usa
// `franjaDelDia`, que es otra cosa — si el cliente dijo "las 7" de la mañana o de la tarde.)
type ventanaDeAtencion struct {
	abierto bool
	desde   int // minutos desde las 00:00
	hasta   int
}

// horarioDelDia dice qué se atiende el día de `t`, mirando PRIMERO el backend.
//
// Devuelve también de dónde salió el dato: hace falta para los logs y para los tests, porque
// "cerrado" por horario del panel y "cerrado" porque el backend no contestó son dos situaciones
// distintas que conviene no confundir al depurar.
func (a *Agent) horarioDelDia(t time.Time) (ventanaDeAtencion, string) {
	// `a.catalog` nil es un Agent armado a mano (tests de las reglas de horario, que no necesitan
	// catálogo). Sin esta guarda, consultar el horario lo hace entrar en panic: lo encontró
	// TestDiaLaborableYHorario al pasar esta función a leer del backend.
	if a.catalog == nil {
		return a.horarioDeRespaldo(t), "respaldo (.env)"
	}
	if ctx, ok := a.catalog.Get(); ok && ctx != nil && ctx.Horario != nil {
		// El backend numera los días como Python (lunes=0 ... domingo=6); Go pone el domingo en 0.
		pyDia := (int(t.Weekday()) + 6) % 7
		for _, d := range ctx.Horario.Dias {
			if d.Dia != pyDia {
				continue
			}
			if !d.Abierto {
				return ventanaDeAtencion{abierto: false}, "backend"
			}
			desde, hasta := parseHoraHHMM(d.Desde), parseHoraHHMM(d.Hasta)
			// Horas ilegibles: se cae al respaldo en vez de tratarlas como 00:00, que dejaría el
			// día cerrado entero sin que nadie lo hubiera pedido.
			if desde < 0 || hasta < 0 {
				break
			}
			return ventanaDeAtencion{abierto: true, desde: desde, hasta: hasta}, "backend"
		}
	}
	return a.horarioDeRespaldo(t), "respaldo (.env)"
}

// horarioDeRespaldo es el comportamiento de siempre: BOT_HORARIO_* y BOT_DIAS_LABORABLES. Solo se
// usa cuando el backend no pudo decir el horario.
func (a *Agent) horarioDeRespaldo(t time.Time) ventanaDeAtencion {
	if !a.esDiaLaborableDelEnv(t) {
		return ventanaDeAtencion{abierto: false}
	}
	// Con el cierre de ESE día (BOT_HORARIO_FIN_POR_DIA: el domingo cierra antes), igual que antes
	// de que el horario viniera del backend.
	desde, hasta := parseHoraHHMM(a.cfg.BotHorarioInicio), parseHoraHHMM(a.cfg.HorarioFin(t.Weekday()))
	if desde < 0 || hasta < 0 {
		// Configuración inválida. Se mantiene el negocio ABIERTO (un error de configuración no
		// puede cerrarlo, decisión que ya estaba) pero con las horas marcadas como DESCONOCIDAS,
		// no como 00:00-24:00.
		//
		// La diferencia importa: con 0..24*60 los sitios que comparan horas creen tener un
		// horario bueno y se ponen a ofrecer horas inventadas. Lo cazó
		// TestConHorarioInvalidoNoSeOfreceMenu, que exige que con el horario mal puesto NO se
		// ofrezca menú — mejor que el cliente escriba la hora que ofrecerle uno imposible.
		return ventanaDeAtencion{abierto: true, desde: -1, hasta: -1}
	}
	return ventanaDeAtencion{abierto: true, desde: desde, hasta: hasta}
}

// horarioDeHoy es el texto "07:00 a 22:00" del día de `t`, para decírselo al cliente.
//
// Existe porque los 13 sitios que antes interpolaban `cfg.BotHorarioInicio` y `cfg.BotHorarioFin`
// decían el horario del .env, no el que de verdad se atiende. Si el panel amplía a las 22:00,
// esto dice 22:00 sin tocar el bot.
func (a *Agent) horarioDeHoy(t time.Time) (string, string) {
	f, _ := a.horarioDelDia(t)
	if !f.abierto {
		// Un día cerrado no tiene horas. Quien llama ya avisa aparte de que hoy no se trabaja; se
		// devuelve el horario de un día que SÍ abra para no dejar al cliente sin referencia.
		if ref, ok := a.primerDiaAbierto(t); ok {
			return minutosAHora(ref.desde), minutosAHora(ref.hasta)
		}
		return a.cfg.BotHorarioInicio, a.cfg.BotHorarioFin
	}
	return minutosAHora(f.desde), minutosAHora(f.hasta)
}

// minutosDeHoy son el inicio y el fin del horario del día de `t`, en minutos desde medianoche.
// Para los sitios que COMPARAN una hora contra el horario, en vez de decírsela al cliente.
//
// Devuelve (-1, -1) cuando no hay horario aplicable, que es lo que ya esperaban esos sitios de
// `parseHoraHHMM` con una configuración inválida: cada uno decide si eso significa "no filtrar"
// (encurso) o "no ofrecer menú" (horasmenu). Se conserva ese contrato para no cambiar de paso
// decisiones que no son de esta tarea.
func (a *Agent) minutosDeHoy(t time.Time) (int, int) {
	f, _ := a.horarioDelDia(t)
	// Abierto pero con horas desconocidas: se devuelve "no sé" tal cual, para que nadie ofrezca
	// horas que no puede sostener.
	if f.abierto && (f.desde < 0 || f.hasta < 0) {
		return -1, -1
	}
	if !f.abierto {
		// Día cerrado: se toma el horario del próximo día que abra, porque quien compara una hora
		// suele estar agendando para más adelante (un domingo preguntando por el lunes).
		if ref, ok := a.primerDiaAbierto(t); ok {
			return ref.desde, ref.hasta
		}
		return -1, -1
	}
	return f.desde, f.hasta
}

// primerDiaAbierto busca, a partir de mañana, el primer día que se atienda. Sirve para poder
// decir "atendemos de 07:00 a 22:00" un domingo, cuando hoy no hay horas que mostrar.
func (a *Agent) primerDiaAbierto(t time.Time) (ventanaDeAtencion, bool) {
	for i := 1; i <= 7; i++ {
		if f, _ := a.horarioDelDia(t.AddDate(0, 0, i)); f.abierto {
			return f, true
		}
	}
	return ventanaDeAtencion{}, false
}

// diasAbiertos son los días que se trabaja, en numeración ISO (lunes=1 ... domingo=7), que es
// como los lee la gente y como ya los manejaba el resto del bot.
func (a *Agent) diasAbiertos(t time.Time) map[int]bool {
	dias := map[int]bool{}
	// Se recorre una semana completa desde el lunes para no depender del día que sea hoy.
	lunes := t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
	for i := 0; i < 7; i++ {
		dia := lunes.AddDate(0, 0, i)
		if f, _ := a.horarioDelDia(dia); f.abierto {
			iso := int(dia.Weekday())
			if iso == 0 {
				iso = 7
			}
			dias[iso] = true
		}
	}
	return dias
}

// minutosAHora pasa 1320 a "22:00".
func minutosAHora(min int) string {
	if min < 0 {
		return ""
	}
	if min >= 24*60 {
		min = 24*60 - 1
	}
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}

// textoDiasAbiertos arma "lunes a sábado" (o la lista, si hay huecos) con los días que de verdad
// se atienden según el backend. Reemplaza a textoDiasLaborables, que leía el .env.
func (a *Agent) textoDiasAbiertos(t time.Time) string {
	dias := a.diasAbiertos(t)
	var nombres []string
	for i := 1; i <= 7; i++ {
		if dias[i] {
			nombres = append(nombres, diasEnEspanol[i])
		}
	}
	if len(nombres) == 0 {
		return ""
	}
	if len(nombres) == 1 {
		return nombres[0]
	}
	// Seguidos: "lunes a sábado". Con huecos se enumeran TODOS separados por comas, que es el
	// formato que ya tenía el bot: un "lunes a domingo" con el miércoles cerrado prometería un día
	// que no se atiende.
	if sonSeguidos(dias) {
		return nombres[0] + " a " + nombres[len(nombres)-1]
	}
	return strings.Join(nombres, ", ")
}

// sonSeguidos dice si los días forman un rango sin huecos.
func sonSeguidos(dias map[int]bool) bool {
	primero, ultimo := -1, -1
	for i := 1; i <= 7; i++ {
		if dias[i] {
			if primero < 0 {
				primero = i
			}
			ultimo = i
		}
	}
	if primero < 0 {
		return false
	}
	for i := primero; i <= ultimo; i++ {
		if !dias[i] {
			return false
		}
	}
	return true
}
