package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wp-llm-gas/internal/conversation"
)

// El enlace de seguimiento es de UN PEDIDO, no del cliente. El 10/09 el bot le confirmó a David
// el pedido #234 y le mandó el enlace del #233 —que él ya había cancelado—: quedó siguiendo un
// pedido muerto. Lo detectó la revisión del informe externo del 11/09.

const (
	enlaceViejo = "https://ubiec.app/seguimiento/tok-233-abc/"
	enlaceVivo  = "https://ubiec.app/seguimiento/tok-234-xyz/"
)

// INCIDENTE 10/09 — el enlace de un pedido CANCELADO no puede salir, aunque el modelo lo copie
// del historial del chat.
func TestIncidente_NoSeMandaElEnlaceDeUnPedidoViejo(t *testing.T) {
	texto := "¡Listo! 🎉 Tu pedido va en camino.\n\n📍 Sigue a tu repartidor en vivo aquí:\n" + enlaceViejo

	limpio, intervino := limpiarEnlaceDeSeguimiento(texto, enlaceVivo)
	if !intervino {
		t.Fatal("el enlace de otro pedido pasó tal cual: el cliente seguiría un pedido que no es el suyo")
	}
	if strings.Contains(limpio, enlaceViejo) {
		t.Errorf("el enlace viejo sigue en el mensaje: %q", limpio)
	}
	// El resto del mensaje se conserva: se quita el enlace, no la confirmación.
	if !strings.Contains(limpio, "va en camino") {
		t.Errorf("se perdió la confirmación del pedido: %q", limpio)
	}
	// Y no queda la invitación colgando sin enlace.
	if strings.Contains(strings.ToLower(limpio), "sigue a tu repartidor") {
		t.Errorf("quedó la invitación sin enlace detrás: %q", limpio)
	}
}

// El enlace VIGENTE sí sale: el candado no puede romper el caso bueno.
func TestElEnlaceDelPedidoVivoSePermite(t *testing.T) {
	texto := "¡Listo! 🎉\n\n📍 Sigue a tu repartidor en vivo aquí:\n" + enlaceVivo

	limpio, intervino := limpiarEnlaceDeSeguimiento(texto, enlaceVivo)
	if intervino {
		t.Fatalf("se bloqueó el enlace correcto: %q", limpio)
	}
	if !strings.Contains(limpio, enlaceVivo) {
		t.Errorf("el enlace del pedido vivo se perdió: %q", limpio)
	}
}

// Sin pedido vivo (todo cancelado/entregado) NINGÚN enlace puede salir.
func TestSinPedidoVivoNoSaleNingunEnlace(t *testing.T) {
	texto := "Tu pedido sigue en camino 😊 " + enlaceVivo

	limpio, intervino := limpiarEnlaceDeSeguimiento(texto, "")
	if !intervino || strings.Contains(limpio, "seguimiento/") {
		t.Errorf("se mandó un enlace sin pedido vivo detrás: %q", limpio)
	}
}

// Un mensaje sin enlaces no se toca: el candado no puede alterar conversación normal.
func TestMensajeSinEnlaceNoSeAltera(t *testing.T) {
	for _, texto := range []string{
		"¡Hola! ¿Qué cilindro necesitas? 😊",
		"Tu pedido va en camino. Te aviso cuando llegue 🚚",
		"Atendemos de 07:00 a 19:00.",
	} {
		limpio, intervino := limpiarEnlaceDeSeguimiento(texto, enlaceVivo)
		if intervino || limpio != texto {
			t.Errorf("se alteró un mensaje sin enlaces: %q -> %q", texto, limpio)
		}
	}
}

// El enlace se guarda con el pedido y MUERE con él. Se prueba contra los DOS backends: si
// sqlite y memoria divergen, el bot se comporta distinto en dev y en producción.
func TestElEnlaceMuereConElPedido(t *testing.T) {
	for nombre, abrir := range storesDePrueba(t) {
		t.Run(nombre, func(t *testing.T) {
			const from = "593999400001"
			store := abrir()

			store.SetActivePedido(from, 234)
			store.SetSeguimientoActivo(from, enlaceVivo)
			if got := store.GetSeguimientoActivo(from); got != enlaceVivo {
				t.Fatalf("el enlace del pedido vivo no se guardó: %q", got)
			}

			// Cancelado (o entregado): ClearActivePedido se lleva el enlace con él.
			store.ClearActivePedido(from)
			if got := store.GetSeguimientoActivo(from); got != "" {
				t.Errorf("el enlace sobrevivió a su pedido cancelado: %q", got)
			}
		})
	}
}

// AGUJERO QUE DESTAPÓ LA MUTACIÓN: un pedido NUEVO invalida el enlace del anterior aunque nadie
// guarde uno nuevo. Pasa de verdad en la espera de conductor: asigna el pedido minutos después
// (SetActivePedido) sin token de seguimiento a mano. Sin esto, el cliente recibiría el enlace
// del pedido viejo como si fuera el de su pedido recién asignado.
func TestUnPedidoNuevoInvalidaElEnlaceDelAnterior(t *testing.T) {
	for nombre, abrir := range storesDePrueba(t) {
		t.Run(nombre, func(t *testing.T) {
			const from = "593999400003"
			store := abrir()

			store.SetActivePedido(from, 233)
			store.SetSeguimientoActivo(from, enlaceViejo)

			// Otro pedido (p. ej. el que asignó la espera de conductor), sin enlace propio.
			store.SetActivePedido(from, 234)
			if got := store.GetSeguimientoActivo(from); got != "" {
				t.Errorf("el enlace del pedido #233 quedó vigente para el #234: %q", got)
			}

			// Y volver a marcar el MISMO pedido no borra su enlace (idempotencia).
			store.SetSeguimientoActivo(from, enlaceVivo)
			store.SetActivePedido(from, 234)
			if got := store.GetSeguimientoActivo(from); got != enlaceVivo {
				t.Errorf("re-marcar el mismo pedido borró su enlace: %q", got)
			}
		})
	}
}

// storesDePrueba devuelve los dos backends reales del bot, para probar contra ambos.
func storesDePrueba(t *testing.T) map[string]func() conversation.Store {
	t.Helper()
	return map[string]func() conversation.Store{
		"mem": func() conversation.Store { return conversation.NewMemStore() },
		"sqlite": func() conversation.Store {
			s, err := conversation.NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"), 15)
			if err != nil {
				t.Fatalf("abrir sqlite: %v", err)
			}
			return s
		},
	}
}

// EL CANDADO CABLEADO EN EL TURNO REAL. Los tests de la función suelta no bastan: si nadie la
// llama desde HandleMessage, el enlace viejo sale igual. Este test reproduce el turno completo
// con un modelo que copia del historial el enlace del pedido cancelado, como hizo el 10/09.
func TestIncidente_ElTurnoNoDejaSalirElEnlaceViejo(t *testing.T) {
	const from = "593999400004"
	store := conversation.NewMemStore()
	// El cliente tiene VIVO el pedido #234 (con su propio enlace) y pregunta por él. El modelo
	// contesta con verdad —su pedido existe, así que el candado del fantasma no interviene— pero
	// copia del historial el enlace del #233, que ya canceló. Es el turno del 10/09.
	store.SetActivePedido(from, 234)
	store.SetSeguimientoActivo(from, enlaceVivo)
	fake := &modeloQueDice{respuestas: []string{
		"Tu pedido va en camino 🚚\n\n📍 Sigue a tu repartidor en vivo aquí:\n" + enlaceViejo,
	}}
	ag := agentIncidente(fake, store)

	res, err := ag.HandleMessage(context.Background(), from, "¿dónde va mi pedido?")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if strings.Contains(res.Texto, enlaceViejo) {
		t.Fatalf("el turno entregó el enlace del pedido cancelado #233: %q", res.Texto)
	}
	// La respuesta correcta del modelo se conserva: se quita el enlace, no el mensaje.
	if !strings.Contains(res.Texto, "camino") {
		t.Errorf("se pisó la respuesta legítima del modelo: %q", res.Texto)
	}
}

// El candado completo sobre el Agent: con el pedido cancelado, la respuesta sale sin enlace.
func TestRevisarEnlaceUsaElEstadoDurable(t *testing.T) {
	const from = "593999400002"
	store := conversation.NewMemStore()
	ag := agentDePrueba(nil, store)

	store.SetActivePedido(from, 234)
	store.SetSeguimientoActivo(from, enlaceVivo)

	// Mientras vive, su propio enlace pasa.
	if got := ag.revisarEnlaceDeSeguimiento(from, "En camino "+enlaceVivo); !strings.Contains(got, enlaceVivo) {
		t.Errorf("se bloqueó el enlace vigente: %q", got)
	}
	// El de otro pedido, no.
	if got := ag.revisarEnlaceDeSeguimiento(from, "En camino "+enlaceViejo); strings.Contains(got, enlaceViejo) {
		t.Errorf("pasó el enlace de otro pedido: %q", got)
	}
	// Cancelado: ya no pasa ninguno.
	store.ClearActivePedido(from)
	if got := ag.revisarEnlaceDeSeguimiento(from, "En camino "+enlaceVivo); strings.Contains(got, "seguimiento/") {
		t.Errorf("pasó un enlace con el pedido ya cancelado: %q", got)
	}
}
