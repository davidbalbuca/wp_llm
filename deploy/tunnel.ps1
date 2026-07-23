# Expone el servidor local (puerto 3000) con una URL publica HTTPS via Cloudflare Tunnel.
# Copia la URL https://....trycloudflare.com que aparece y registrala como webhook en Meta,
# agregando /webhook al final.  Ej: https://abc-123.trycloudflare.com/webhook
param([int]$Port = 3000)

$cf = "$env:USERPROFILE\cloudflared\cloudflared.exe"
if (-not (Test-Path $cf)) { $cf = "cloudflared" }
& $cf tunnel --url "http://localhost:$Port"
