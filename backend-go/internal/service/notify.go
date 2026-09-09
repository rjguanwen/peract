package service

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
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

// SendEmail 发送邮件；SMTP 未配置或发送失败时返回错误。
func SendEmail(cfg *config.Config, to, subject, body string) error {
	if cfg.SMTPHost == "" || cfg.SMTPUser == "" || cfg.SMTPFrom == "" {
		return errors.New("SMTP 未配置")
	}
	fromAddr, fromHeader := parseSender(cfg.SMTPFrom, cfg.SMTPFromName)

	header := make(textproto.MIMEHeader)
	header.Set("From", fromHeader)
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

	port := cfg.SMTPPort
	if port <= 0 {
		port = 465
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(port))
	host := cfg.SMTPHost

	var conn net.Conn
	var err error
	if port == 465 {
		// 隐式 SSL（SMTPS）
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("初始化 SMTP 会话失败: %w", err)
	}
	defer client.Close()

	// 587/25 等端口如需加密走 STARTTLS（465 已是隐式 TLS 无需再升级）
	if port != 465 && cfg.SMTPUseTLS {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("SMTP STARTTLS 失败: %w", err)
		}
	}
	if cfg.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	return sendData(client, fromAddr, to, buf.Bytes())
}

func sendData(client *smtp.Client, from, to string, msg []byte) error {
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO 失败: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 失败: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}
	return w.Close()
}

// parseSender 解析发件人：返回可用于 MAIL FROM 的纯邮箱与用于 From 头的显示串。
// 兼容 SMTP_FROM 为纯邮箱或「显示名 <邮箱>」两种写法。
func parseSender(from, fromName string) (addr, header string) {
	addr = strings.TrimSpace(from)
	if m, err := mail.ParseAddress(from); err == nil {
		addr = m.Address
		if fromName == "" && m.Name != "" {
			fromName = m.Name
		}
	}
	if fromName != "" {
		header = fromName + " <" + addr + ">"
	} else {
		header = addr
	}
	return addr, header
}
