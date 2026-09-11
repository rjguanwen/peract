package service

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"taskbackend/internal/config"
)

// ---------- HTTP 连接复用 ----------

var (
	transportOnce sync.Once
	sharedTr      *http.Transport
)

// sharedTransport 返回进程级复用的 Transport，避免每次通知都新建连接池。
func sharedTransport() *http.Transport {
	transportOnce.Do(func() {
		sharedTr = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	})
	return sharedTr
}

// SendWebhook 推送 IM Webhook（钉钉/企业微信/飞书），失败仅记日志。
func SendWebhook(cfg *config.Config, title, content string) error {
	if cfg.NotifyWebhookURL == "" {
		return nil
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
		return fmt.Errorf("编码 webhook 载荷: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, cfg.NotifyWebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造 webhook 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	timeout := cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout, Transport: sharedTransport()}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[notify] webhook 推送失败: %v", err)
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("webhook 返回 HTTP %d", resp.StatusCode)
		log.Printf("[notify] %v", err)
		return err
	}
	return nil
}

// SendEmail 发送邮件；SMTP 未配置或发送失败时返回错误。
// 所有网络操作均带超时，调用方不应在持有数据库连接的事务里调用它。
func SendEmail(cfg *config.Config, to, subject, body string) error {
	if !cfg.SMTPConfigured() {
		return errors.New("SMTP 未配置")
	}
	if strings.TrimSpace(to) == "" {
		return errors.New("收件人为空")
	}
	dialTimeout := cfg.SMTPDialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	sessionTimeout := cfg.SMTPCommandTimeout
	if sessionTimeout <= 0 {
		sessionTimeout = 20 * time.Second
	}

	port := cfg.SMTPPort
	if port <= 0 {
		port = 465
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(port))
	host := cfg.SMTPHost

	dialer := &net.Dialer{Timeout: dialTimeout}
	var conn net.Conn
	var err error
	if isImplicitTLSPort(port) {
		// 隐式 SSL（SMTPS）
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	// 会话级兜底超时：任一阶段卡住都会在 sessionTimeout 后断开，不会永久阻塞
	_ = conn.SetDeadline(time.Now().Add(sessionTimeout))
	defer conn.Close()

	implicitTLS := isImplicitTLSPort(port)
	if err := deliver(conn, cfg, host, implicitTLS, to, subject, body); err != nil {
		return err
	}
	log.Printf("[notify] 邮件已发送至 %s", to)
	return nil
}

func deliver(conn net.Conn, cfg *config.Config, host string, implicitTLS bool, to, subject, body string) error {
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("初始化 SMTP 会话失败: %w", err)
	}
	defer client.Close()

	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("SMTP EHLO 失败: %w", err)
	}
	// 465 已是隐式 TLS，无需再升级；其余端口按配置走 STARTTLS
	if !implicitTLS && cfg.SMTPUseTLS {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("SMTP STARTTLS 失败: %w", err)
		}
	}
	if cfg.SMTPUser != "" {
		// 不用 smtp.PlainAuth：它会因为隐式 TLS 下 client.TLS() 为 nil 而拒绝发凭据
		auth := &plainAuth{username: cfg.SMTPUser, password: cfg.SMTPPassword, host: host}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}

	msg, err := buildMessage(cfg, to, subject, body)
	if err != nil {
		return err
	}
	fromAddr, _ := parseSender(cfg.SMTPFrom, cfg.SMTPFromName)
	if err := client.Mail(fromAddr); err != nil {
		return fmt.Errorf("MAIL FROM 失败: %w", err)
	}
	// 收件人必须重新解析，防止 "a@x.com, b@x.com" 之类的注入式入参
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("收件人地址不合法: %w", err)
	}
	if err := client.Rcpt(rcpt.Address); err != nil {
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

// buildMessage 组装 RFC 5322 邮件，中文主题做 RFC 2047 编码，正文规范为 CRLF 并做点转义。
func buildMessage(cfg *config.Config, to, subject, body string) ([]byte, error) {
	_, fromHeader := parseSender(cfg.SMTPFrom, cfg.SMTPFromName)

	var buf bytes.Buffer
	write := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	write("From", fromHeader)
	write("To", encodeAddressHeader(to))
	write("Subject", encodeHeaderWord(subject))
	write("Date", time.Now().Format(time.RFC1123Z))
	write("MIME-Version", "1.0")
	write("Content-Type", "text/plain; charset=UTF-8")
	write("Content-Transfer-Encoding", "8BIT")
	buf.WriteString("\r\n")
	buf.WriteString(normalizeBody(body))
	return buf.Bytes(), nil
}

// normalizeBody 统一换行为 CRLF，并按 RFC 5321 对以 "." 开头的行做点转义。
func normalizeBody(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, ".") {
			lines[i] = "." + line
		}
	}
	return strings.Join(lines, "\r\n")
}

// encodeHeaderWord 将含非 ASCII 的主题编码为 RFC 2047 word，避免中文主题乱码或被拒。
func encodeHeaderWord(s string) string {
	if s == "" {
		return ""
	}
	if isASCII(s) {
		return sanitizeHeaderValue(s)
	}
	return mime.BEncoding.Encode("UTF-8", s)
}

func encodeAddress(s string) string { return encodeHeaderWord(s) }

// encodeAddressHeader 校验并规范化 To 头，仅接受单个合法地址。
func encodeAddressHeader(to string) string {
	if addr, err := mail.ParseAddress(to); err == nil {
		if addr.Name != "" {
			return encodeHeaderWord(addr.Name) + " <" + addr.Address + ">"
		}
		return addr.Address
	}
	return to
}

// sanitizeHeaderValue 剔除可能用于响应头注入的 CR/LF。
func sanitizeHeaderValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isImplicitTLSPort(port int) bool { return port == 465 }

// plainAuth 是自带明文 AUTH LOGIN 实现的 smtp.Auth：
// 标准库 smtp.PlainAuth 在未检测到 TLS 状态时会拒绝发送凭据，
// 而隐式 TLS(465) 会话中 client.TLS() 恒为 nil，因此必须自行实现。
type plainAuth struct {
	username string
	password string
	host     string
}

func (a *plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if server.Name != a.host {
		// 服务器名不符时不发送凭据，避免口令泄露给错误主机
		return "", nil, fmt.Errorf("SMTP 主机不匹配：期望 %s，实际 %s", a.host, server.Name)
	}
	return "PLAIN", nil, nil
}

func (a *plainAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// PLAIN: \0<username>\0<password>
	return []byte("\x00" + a.username + "\x00" + a.password), nil
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
	fromName = sanitizeHeaderValue(fromName)
	if fromName != "" {
		header = encodeAddress(fromName) + " <" + addr + ">"
	} else {
		header = addr
	}
	return addr, header
}
