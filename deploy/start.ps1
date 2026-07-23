# start.ps1 — compila y ejecuta el bot de WhatsApp (Go).
# Vive en deploy/; la raíz del proyecto es la carpeta padre.
# Uso (desde la raíz del proyecto):
#   ./deploy/start.ps1          # go run (desarrollo, sin generar binario)
#   ./deploy/start.ps1 -Build   # compila dist/bot.exe y lo ejecuta

param([switch]$Build)

# --- Go portable de este equipo ---
$env:GOROOT = "$env:USERPROFILE\go-portable\go"
$env:Path = "$env:GOROOT\bin;$env:Path"
$env:GOPATH = "$env:USERPROFILE\go"

# Raíz del proyecto = carpeta padre de deploy/
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

if ($Build) {
    Write-Host "Compilando dist/bot.exe..." -ForegroundColor Cyan
    go build -o dist/bot.exe ./cmd/bot
    if ($LASTEXITCODE -ne 0) { Write-Host "Fallo la compilacion." -ForegroundColor Red; exit 1 }
    Write-Host "Ejecutando dist/bot.exe..." -ForegroundColor Green
    ./dist/bot.exe
} else {
    Write-Host "Ejecutando con go run ./cmd/bot ..." -ForegroundColor Green
    go run ./cmd/bot
}
