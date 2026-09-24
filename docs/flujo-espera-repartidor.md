# Qué pasa cuando no hay repartidor

> Para quien tenga que entender, operar o cambiar este flujo.
> Última actualización: 24-sep-2026.

## El caso que lo motivó

Carlos (`593959545411`) escribió el 23-sep a las 17:45. Pidió su gas, dio color, cantidad,
ubicación y cédula. A las 17:48 su pedido quedó registrado, pero sin conductor.

```
17:51  el bot: "¿Deseas esperar?"   [Esperar / Programar / Cancelar]
17:52  Carlos: "Esperar"
17:57  el bot: "no hay ningún repartidor disponible. Intenta más tarde."
```

**Doce minutos y una sola pregunta.** Carlos dijo que sí esperaba —estaba dispuesto a
comprar— y lo despedimos nosotros. No fue un error técnico: el código preguntaba una vez y
la espera duraba 5 minutos.

## Cómo funciona ahora

```
min 0    el cliente pide → el backend registra y empieza a buscar conductor
min 10   sin conductor   → el bot pregunta   [Esperar / Programar / Cancelar]
min 20   sin conductor   → el bot pregunta   [Esperar / Programar / Cancelar]
min 30   se agotó        → disculpas         [Reprogramar / Cancelar]
```

En la última ronda **desaparece "Esperar"** a propósito: quien ya esperó media hora no
necesita que le ofrezcamos más espera, necesita una salida. Y son **dos** opciones, no una:
un menú sin salida no es una opción, es un embudo.

Si cancela después de haber esperado se le despide distinto — se le agradece la paciencia y
**no** se le vuelve a ofrecer agendar. No se arrepintió: se quedó sin gas por algo nuestro.

## Quién decide qué

Esta es la parte que más confusión causa, porque hay dos sistemas y cuatro relojes.

| Quién | Qué decide | Dónde se cambia |
|---|---|---|
| **Backend** | cuánto se BUSCA conductor | panel → Parámetros |
| **Bot** | cada cuánto le PREGUNTA al cliente, y cuántas rondas | `.env` del bot |

**El backend no habla con el cliente.** Solo busca conductores y guarda el estado. El bot
consulta cada 7 segundos en segundo plano —el cliente no ve eso— y cuando el backend dice
"se cumplió el plazo", el bot decide qué preguntarle y con qué botones.

Por eso los plazos viven en el backend: **la app móvil usa las mismas APIs**. Si estuvieran
en el bot, la app se quedaría sin ellos. Y además sobreviven a un reinicio del bot, que pasa
en cada despliegue.

### Valores en producción

| Parámetro | Valor | Dónde |
|---|---|---|
| `BUSQUEDA_INICIAL_SEGUNDOS` | 180 | panel |
| `BUSQUEDA_ESPERA_SEGUNDOS` | **1800** (30 min) | panel |
| `BUSQUEDA_DECISION_SEGUNDOS` | **1800** | panel |
| `BUSQUEDA_REINTENTO_SEGUNDOS` | 20 | panel |
| `BOT_ESPERA_RONDA_MIN` | **10** | `.env` del bot |
| `BOT_CIERRE_INACTIVIDAD_MIN` | **15** | `.env` del bot |

Los tres en negrita se aplicaron el 24-sep (los del panel quedan guardados en la tabla `Parametro` y surten efecto al instante, sin reiniciar nada). El de inactividad tiene que ser **mayor** que la
ronda: si no, el "parece que te ocupaste" le cae al cliente encima de una pregunta que aún
no contestó.

`BUSQUEDA_DECISION_SEGUNDOS` es cuánto mantiene el backend la búsqueda abierta esperando
respuesta. Con rondas de 10 minutos, un "sí" en el minuto 25 tiene que seguir sirviendo — por
eso vale lo mismo que el total.

## Las dos APIs

| | |
|---|---|
| `POST /georoutes/buscarConductor/` | abre la búsqueda. El bot la llama una vez |
| `GET /georoutes/estadoBusquedaConductor/` | "¿ya?". El bot pregunta cada 7 s |

Cada consulta **hace avanzar la búsqueda**: reintenta asignar si toca, avisa a los conductores
si toca, y cierra la etapa si venció. Además hay un barrido (`barrer_busquedas`) que corre
cada minuto para las búsquedas que nadie está consultando.

## Los relojes no pueden competir

Hay un tercer reloj además de los dos de la tabla: **`topeBusqueda`** (90 min, en
`espera_busqueda.go`). Es una red de seguridad del bot —existe por si el backend quedara
devolviendo "buscando" para siempre— y **no** es un plazo del negocio.

Estaba en 30 minutos, exactamente lo mismo que el backend busca hoy. Los dos vencían al mismo
minuto y ganaba el que llegara primero: si ganaba el del bot, el cliente recibía el corte seco
*"no hay repartidor, intenta más tarde"* en lugar de la última ronda con disculpas y
[Reprogramar / Cancelar]. Justo la venta que las rondas existen para rescatar.

> **Regla:** la red de seguridad del bot va muy por encima del total del negocio
> (rondas x duración). Hay un test que falla si alguien sube las rondas y se olvida del techo.

Los demás relojes del bot se revisaron y **no** interfieren:

| Reloj | Valor | Por qué no choca |
|---|---|---|
| `VentanaConversacionSeguida` | 30 min | no limpia si hay espera viva (`GetPendingWait`) |
| `ventanaPedidoActivo` | 6 h | muy por encima |
| `SessionGap` / memoria | 24 h | muy por encima |
| `OFERTA_SEGUNDOS` (backend) | 300 | con cliente esperando manda el reloj de SU búsqueda |

## La regla que hay que respetar al tocar esto

> **Borrar la espera del bot es cerrar la búsqueda del backend.**

Si el bot borra su espera sin avisarle al backend, el backend sigue buscando conductor para
un pedido que el cliente ya no espera. El caso peor: el cliente cambia de dirección, el bot
re-registra el pedido en la nueva, y el backend le asigna un repartidor al pedido viejo — que
sale hacia la dirección anterior.

Había **tres** caminos que lo hacían (rechazar color alterno, cambio de dirección, cierre de
conversación). Hoy todos pasan por `cerrarEsperaYBusqueda` en
`wp_llm/internal/agent/rondasespera.go`, y un test impide que vuelva a aparecer un
`ClearPendingWait` suelto en esos archivos.

## Si hay que cambiar algo

- **"Que espere más/menos tiempo"** → `BUSQUEDA_ESPERA_SEGUNDOS`, en el panel.
- **"Que le pregunte más/menos seguido"** → `BOT_ESPERA_RONDA_MIN`, en el `.env` del bot.
- **"Que le pregunte más veces"** → `rondasConEspera` en `rondasespera.go` (hoy 2 + la final).

Si cambias la ronda del bot, revisa que `BOT_CIERRE_INACTIVIDAD_MIN` siga siendo mayor.

## Cómo verificar en producción

```bash
ssh prod-ubi
docker exec ubi-wp-llm-bot-1 wget -qO- http://localhost:3001/health   # el bot escucha en 3001
curl -H "Host: ws11.geoware.lat" localhost/health                      # a través de nginx
```

Tres cosas que confunden al verificar:

- el bot escucha en **3001**, no 3000
- nginx solo expone el **puerto 80** (el TLS lo pone Cloudflare), así que probar `https://`
  desde dentro del contenedor da "connection refused" y no significa nada
- el dominio es **`ws11.geoware.lat`**; sin ese header `Host`, `/health` responde 404

## Código

| Qué | Dónde |
|---|---|
| Rondas, textos y cierre de la espera | `wp_llm/internal/agent/rondasespera.go` |
| Bucle que consulta al backend | `wp_llm/internal/agent/espera_busqueda.go` |
| Respuestas del menú | `wp_llm/internal/agent/menus.go` |
| Parámetros del backend | `ubi-geoware/core/georoutes/parametros.py` |
| Motor de búsqueda | `ubi-geoware/core/georoutes/selector/busqueda_selectors.py` |

Notas internas con el detalle de las decisiones: `memory/20-rondas-de-espera-repartidor.md`.
