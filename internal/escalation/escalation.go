// Package escalation deriva la conversación al dueño del negocio enviándole un WhatsApp.
package escalation

import (
	"fmt"

	"wp-llm-gas/internal/config"
	"wp-llm-gas/internal/whatsapp"
)

// NotifyOwner envía un WhatsApp al dueño con el contexto del cliente y devuelve un
// mensaje de resultado listo para usar como tool result del modelo.
func NotifyOwner(cfg config.Config, from, motivo, resumen string) string {
	msg := fmt.Sprintf(
		"🔔 Un cliente requiere tu atención.\n\n"+
			"📱 Cliente: +%s\n📌 Motivo: %s\n📝 Resumen: %s",
		from, motivo, resumen)

	if err := whatsapp.SendText(cfg, cfg.OwnerPhone, msg); err != nil {
		return "No se pudo notificar al dueño automáticamente."
	}
	return "Listo: el dueño fue notificado por WhatsApp."
}
