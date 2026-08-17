// Package escalation notifica al equipo de soporte cuando el bot escala un caso.
// Antes enviaba un WhatsApp al dueño; ahora cada escalación crea un TICKET (en el store) y
// este paquete manda la notificación por CORREO (SMTP). El ticket se gestiona desde el panel.
package escalation

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"time"

	"wp-llm-gas/internal/config"
)

// SendSupportEmail notifica por correo la creación de un ticket de soporte. Best-effort:
// si el SMTP no está configurado o falla, solo se loguea (el ticket ya quedó guardado).
// Pensada para llamarse en una goroutine (no bloquea el chat).
func SendSupportEmail(cfg config.Config, ticketID int64, phone, motivo, resumen string) {
	if cfg.SMTPHost == "" || cfg.SMTPUser == "" || cfg.SupportEmailTo == "" {
		log.Printf("[soporte] SMTP no configurado; ticket #%d sin correo (revisar SMTP_HOST/SMTP_USER/SUPPORT_EMAIL_TO)", ticketID)
		return
	}

	subject := fmt.Sprintf("Ticket de soporte #%d - %s", ticketID, motivo)
	body := fmt.Sprintf(
		"Se creó un ticket de soporte del bot de WhatsApp.\r\n\r\n"+
			"Ticket:  #%d\r\n"+
			"Cliente: +%s\r\n"+
			"Motivo:  %s\r\n"+
			"Resumen: %s\r\n"+
			"Fecha:   %s\r\n\r\n"+
			"Gestión: panel web -> Soporte (ver el caso, abrir el chat y cerrarlo con su solución).",
		ticketID, phone, motivo, resumen, time.Now().Format("2006-01-02 15:04:05"))

	destinos := []string{}
	for _, d := range strings.Split(cfg.SupportEmailTo, ",") {
		if d = strings.TrimSpace(d); d != "" {
			destinos = append(destinos, d)
		}
	}

	msg := []byte("From: Bot Ubi <" + cfg.SMTPUser + ">\r\n" +
		"To: " + strings.Join(destinos, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + body)

	auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	if err := smtp.SendMail(cfg.SMTPHost+":"+cfg.SMTPPort, auth, cfg.SMTPUser, destinos, msg); err != nil {
		log.Printf("[soporte] error enviando correo del ticket #%d: %v", ticketID, err)
		return
	}
	log.Printf("[soporte] correo del ticket #%d enviado a %s", ticketID, cfg.SupportEmailTo)
}
