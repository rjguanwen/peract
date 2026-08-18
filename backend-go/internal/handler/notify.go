package handler

import (
	"taskbackend/internal/config"
	"taskbackend/internal/service"
)

// sendExternalNotification 外部通知（Webhook + 邮件），失败静默
func sendExternalNotification(title, content, emailTo string, cfg *config.Config) {
	if cfg.NotifyWebhookURL != "" {
		service.SendWebhook(cfg, title, content)
	}
	if emailTo != "" && cfg.SMTPHost != "" {
		service.SendEmail(cfg, emailTo, title, content)
	}
}
