package service

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"log"
	"net/http"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"

	"taskbackend/internal/config"
)

// SendWebhook 推送 IM Webhook（钉钉/企业微信/飞书），失败静默
func SendWebhook(cfg *config.Config, title, content string) {
	if cfg.NotifyWebhookURL == "" {
		return
	}
	text := title + "\n" + content
	var payload any
	switch cfg.NotifyWebhookType {
	case "dingtalk", "wecom":
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case "feishu":
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
	default:
		payload = map[string]string{"title": title, "content": content}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, cfg.NotifyWebhookURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * 1000_000_000} // 5s
	if resp, err := client.Do(req); err != nil {
		log.Printf("[notify] webhook 推送失败: %v", err)
	} else {
		resp.Body.Close()
	}
}

// SendEmail 发送邮件，失败静默
func SendEmail(cfg *config.Config, to, subject, body string) {
	if cfg.SMTPHost == "" || cfg.SMTPUser == "" || cfg.SMTPFrom == "" {
		return
	}
	header := make(textproto.MIMEHeader)
	header.Set("From", cfg.SMTPFrom)
	header.Set("To", to)
	header.Set("Subject", subject)
	header.Set("Content-Type", "text/plain; charset=UTF-8")
	var buf bytes.Buffer
	buf.WriteString("MIME-Version: 1.0\r\n")
	for k, vs := range header {
		for _, v := range vs {
			buf.WriteString(k + ": " + v + "\r\n")
		}
	}
	buf.WriteString("\r\n")
	buf.WriteString(body)

	addr := cfg.SMTPHost
	if cfg.SMTPPort > 0 {
		addr += ":" + strconv.Itoa(cfg.SMTPPort)
	}
	host := hostOnly(addr)
	var err error
	if cfg.SMTPUseTLS {
		err = smtpSendTLS(addr, host, cfg, to, buf.Bytes())
	} else {
		err = smtpSendPlain(addr, host, cfg, to, buf.Bytes())
	}
	if err != nil {
		log.Printf("[notify] 邮件发送失败: %v", err)
	}
}

func smtpSendTLS(addr, host string, cfg *config.Config, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)); err != nil {
		return err
	}
	return sendData(client, cfg.SMTPFrom, to, msg)
}

func smtpSendPlain(addr, host string, cfg *config.Config, to string, msg []byte) error {
	client, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if cfg.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)); err != nil {
			return err
		}
	}
	return sendData(client, cfg.SMTPFrom, to, msg)
}

func sendData(client *smtp.Client, from, to string, msg []byte) error {
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

func hostOnly(addr string) string {
	if i := strings.IndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}
