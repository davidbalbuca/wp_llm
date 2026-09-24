# Cinco correcciones del bot — 23/24-sep-2026

> Qué se cambió, por qué, y cómo probarlo. Todo ya está en producción.
> Cada arreglo nació de una conversación real; están los teléfonos por si hay que revisarlas.

## Resumen

| # | Qué pasaba | Caso real |
|---|---|---|
| 1 | El bot prometía algo y se callaba para siempre | Doris, 21-sep |
| 2 | El bot contradecía a una persona del equipo | Doris, 22-sep |
| 3 | Intentos de usar el bot para otra cosa, sin registro | Sergy, 23-sep |
| 4 | Links de Google Maps que no se entendían | 4 clientes |
| 5 | Se perdía al cliente por preguntarle una sola vez | Carlos, 23-sep |

---

## 1. El bot prometía y se callaba

**Qué pasó** (Doris, `593958615651`, 21-sep 20:13). Dio todo: color, cantidad, ubicación,
cédula. El bot contestó *"Un momento mientras verifico tu información en el sistema…"* y no
volvió a hablar. Quince minutos de silencio hasta que una persona la rescató a mano. **Nunca
hubo pedido.**

**Por qué.** El modelo escribió esa frase pero no ejecutó la verificación. En WhatsApp un turno
solo arranca cuando escribe el cliente — y Doris estaba esperando, como se le pidió.

**Qué se hizo.** No otro parche de frases: una regla sobre la **forma** del turno. Si el bot no
hizo nada y tampoco le devolvió el turno al cliente, se le pregunta por el dato que falta
("¿Cuántos cilindros te envío?"). Cubre cualquier manera de decirlo, no una lista de frases.

**Cómo probarlo.** Hacer un pedido completo hasta la cédula. El bot nunca debe quedarse callado
esperando: siempre cierra con una pregunta o con un menú.

---

## 2. El bot contradecía a una persona del equipo

**Qué pasó** (Doris, 22-sep 19:19):

```
19:18  HUMANO: "el conductor está cerca de tu domicilio, compártenos tu ubicación"
19:19  Doris:  "Estoy esperando"
19:19  BOT:    "no hay repartidores disponibles… 1) Programar  2) Cancelar"
```

El repartidor estaba en su puerta y el bot le ofreció cancelar.

**Por qué.** El panel tenía dos botones separados: uno manda el mensaje, otro apaga el bot.
Mandar el mensaje **no** apagaba el bot. Medido en la base real: de **26** teléfonos atendidos
por una persona, **25** seguían con el bot contestando encima.

**Qué se hizo.** Escribirle al cliente desde el panel **es** tomar la conversación. Ya no hay
que acordarse de apretar nada. Los avisos automáticos (entrega hecha) no apagan el bot — si lo
hicieran, el cliente quedaría sin atención justo después de recibir su gas.

**Cómo probarlo.** Escribir a un cliente desde el panel y confirmar que el bot deja de
responder. Vuelve solo tras 15 min de silencio del cliente.

---

## 3. Intentos de usar el bot para otra cosa

**Qué pasó** (Sergy, `593968109493`, 23-sep): *"necesito resolver un algoritmo"*, *"quiero
aprender el ordenamiento en burbuja"* y, quince minutos después, `-- select * from users`.

El bot los rechazó bien. **El problema es que nadie se enteró** — sin ticket, sin aviso, sin
registro. Si mañana alguien encuentra la frase que sí funciona, nos enteramos por el daño.

**Qué se hace ahora.** Ticket + aviso inmediato a soporte, el mensaje completo queda guardado,
y el chat pasa a un operador. Al cliente se le dice que lo atenderá una persona, **sin
acusarlo**: puede ser alguien que escribió algo raro, y al que sí está sondeando no le conviene
saber qué detectamos.

**El cuidado que se puso.** El riesgo no es dejar pasar a un atacante, es marcar a un cliente
real: se le cortaría el pedido y quedaría fichado. Probado contra los **812 mensajes reales** de
la base: 4 marcados, **0 falsos positivos**. Los comandos sueltos (`/clear`, `/model`) no
cuentan — resultaron ser del propio equipo probando el bot.

---

## 4. Links de Google Maps

**Qué pasaba.** Cuatro clientes compartieron su ubicación con un link de Maps y el bot
respondió *"no pude abrir tu enlace"*.

**Eran dos problemas.** Los del 15, 21 y 23-sep ya estaban arreglados en el código — **faltaba
desplegarlo**. El otro era un formato que nadie había visto: cuando el cliente busca un LUGAR
en Maps ("Gapal") en vez de mandar su pin, la URL trae el nombre y no coordenadas.

**Qué se hizo.** Se geocodifica el nombre, **rechazando lo impreciso**. Esto es lo delicado:
preguntarle a Google por "Cuenca" devuelve el centro de la ciudad con cara de dato válido, y
mandaría al repartidor al Parque Calderón con un pedido que dice tener dirección correcta. Solo
se acepta un punto de menos de 2 km; si no, se pide el pin como antes.

Requiere `KEY_GOOGLE_MAPS` en el `.env` del bot (ya cargada; es la misma del backend, **no** la
de Gemini, que no sirve para geocodificar).

---

## 5. Rondas de espera

Ver **[`flujo-espera-repartidor.md`](flujo-espera-repartidor.md)** — tiene el flujo completo, la
tabla de parámetros y cómo cambiarlos.

En corto: el cliente ya no recibe **una** pregunta sino hasta **tres**, y en la última se le
pide disculpas y quedan [Reprogramar / Cancelar]. El backend busca 30 minutos; el bot pregunta
cada 10.

**Y un bug que salió de ahí:** tres caminos borraban la espera del bot sin avisarle al backend,
que seguía buscando conductor para un pedido que el cliente ya no esperaba. El peor dejaba al
backend mandando un repartidor a la dirección **anterior** después de un cambio de dirección.

---

## Qué se cambió en producción

**`.env` del bot** (`~/ubi/wp-llm/.env`, con respaldo previo):

```
KEY_GOOGLE_MAPS=…              (nueva) geocoding de Maps
BOT_ESPERA_RONDA_MIN=10        (nueva) cada cuánto pregunta el bot
BOT_CIERRE_INACTIVIDAD_MIN=15  antes 7 — tiene que ser MAYOR que la ronda
```

**Panel → Parámetros:**

```
BUSQUEDA_ESPERA_SEGUNDOS     300 → 1800
BUSQUEDA_DECISION_SEGUNDOS   600 → 1800
```

**Código:** `wp_llm` en `f7ee8f6`, binario desplegado vía `ubi-geoware` (`main` y `prod`).

## Verificación hecha

- Suite completa en verde
- **50 mutantes** inyectados a mano en las guardas nuevas: **todos mueren** (un test que no
  falla cuando rompes el código que dice proteger no sirve de nada)
- Detector de abuso validado contra los 812 mensajes reales de producción
- Links de Maps probados contra los 5 links reales que habían fallado
- En prod: health 200, webhook responde, 0 errores desde el despliegue

## Lo que NO se tocó

El flujo de pedidos de `georoutes` (regla #7 del proyecto). Los cambios del backend fueron
**solo dos valores de configuración**, ningún cambio de código ni de lógica.
