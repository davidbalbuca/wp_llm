#!/usr/bin/env bash
# Verificación completa del bot. Es lo que debe pasar en verde antes de cualquier deploy.
#
# El detector de carreras (-race) necesita cgo y un compilador C. En el contenedor de
# desarrollo no hay gcc ni permisos para instalarlo, así que ese paso se SALTA con un aviso
# claro en vez de fingir que pasó. En cualquier entorno con gcc corre solo.
set -uo pipefail
cd "$(dirname "$0")"
fallo=0

echo "== gofmt =="
sin_formato=$(gofmt -l cmd internal 2>/dev/null)
if [ -n "$sin_formato" ]; then echo "sin formatear:"; echo "$sin_formato"; fallo=1; else echo "ok"; fi

echo "== go build =="
go build ./... || fallo=1

echo "== go vet =="
go vet ./... || fallo=1

echo "== go test =="
go test ./... || fallo=1

echo "== go test -race =="
if command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then
    CGO_ENABLED=1 go test -race ./... || fallo=1
else
    echo "SALTADO: no hay compilador C (el detector de carreras necesita cgo)."
    echo "         Correr en un entorno con gcc:  CGO_ENABLED=1 go test -race ./..."
fi

echo
if [ "$fallo" -eq 0 ]; then echo "TODO VERDE"; else echo "HAY FALLOS (ver arriba)"; fi
exit "$fallo"
