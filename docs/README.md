# docs/ — documentos del bot, versionados

Copia versionada de los informes que viven en `docs/` de la raíz del proyecto (que está en el
NFS y **no** es un repositorio git). Aquí se suben los que describen cómo funciona el bot, para
que viajen con el código y cualquiera que clone `wp_llm` los tenga.

| Documento | Para qué |
|---|---|
| [`flujo-espera-repartidor.md`](flujo-espera-repartidor.md) | Qué pasa cuando no hay repartidor: rondas, parámetros, quién decide qué |
| [`cambios-23-24-sep-2026.md`](cambios-23-24-sep-2026.md) | Las cinco correcciones del 23/24-sep, con los casos reales que las motivaron |

La memoria interna del proyecto (decisiones, gotchas, historial) sigue en `memory/` de la raíz.

## Cómo se publica un cambio del bot

Este repo tiene el código fuente, pero lo que corre en el servidor es el binario `wp-llm/bin/bot`
versionado en `ubi-geoware`. Un push aquí, a cualquier rama, no despliega nada: no hay workflows.

1. Trabajar en una rama propia (no en `main`) y pasar `./verificar.sh` en verde.
2. Compilar el binario para el servidor (linux/amd64) y reemplazar `wp-llm/bin/bot` en `ubi-geoware`.
3. En `ubi-geoware`: PR de la rama a `main` (despliega a dev) y luego `main` → `prod` (despliega a prod).
