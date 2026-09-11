package service

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"taskbackend/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		ProjectName:        "躬行",
		SMTPHost:           "smtp.example.test",
		SMTPPort:           465,
		SMTPUser:           "bot@example.test",
		SMTPPassword:       "pw",
		SMTPFrom:           "bot@example.test",
		SMTPFromName:       "躬行",
		WebhookTimeout:     3 * time.Second,
		SMTPDialTimeout:    3 * time.Second,
		SMTPCommandTimeout: 5 * time.Second,
	}
}

// decodeHeader 按 RFC 2047 解回头部值，解不出来即为编码不合法。
func decodeHeader(t *testing.T, s string) string {
	t.Helper()
	dec, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		t.Fatalf("头部值 %q 无法按 RFC 2047 解码: %v", s, err)
	}
	return dec
}

func TestEncodeHeaderWordChinese(t *testing.T) {
	const subject = "「躬行」任务「修复登录」已逾期"
	out := encodeHeaderWord(subject)
	if out == subject {
		t.Fatal("含非 ASCII 的主题不应原样输出，邮件客户端会显示乱码")
	}
	if !strings.HasPrefix(strings.ToUpper(out), "=?UTF-8?B?") {
		t.Fatalf("应输出 RFC 2047 encoded-word，实际：%q", out)
	}
	if got := decodeHeader(t, out); got != subject {
		t.Fatalf("解码后与原主题不一致：\n want %q\n got  %q", subject, got)
	}
}

func TestEncodeHeaderWordASCIIAndInjection(t *testing.T) {
	// ASCII 主题保持原样，但 CR/LF 必须被剔除，否则可借主题注入额外邮件头
	got := encodeHeaderWord("Task done\r\nX-Fake-Header: 1")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("头部值中残留换行符，存在邮件头注入风险：%q", got)
	}
	if !strings.Contains(got, "Task done") {
		t.Fatalf("主题内容被破坏：%q", got)
	}
}

func TestBuildMessageShape(t *testing.T) {
	cfg := testConfig()
	body, err := buildMessage(cfg, "user@example.test", "任务提醒", "第一行\r\n.第二行以点开头\n第三行")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	msg := string(body)
	for _, want := range []string{
		"From: =?UTF-8?", // 发件人显示名是中文，必须编码
		"To: user@example.test",
		"Subject: =?UTF-8?",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8BIT",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("邮件缺少应有的头部 %q：\n%s", want, msg)
		}
	}
	// RFC 5321：正文里以 "." 开头的行要写成 ".."
	if !strings.Contains(msg, "\r\n..第二行以点开头") {
		t.Errorf("正文未做点转义，会提前截断 DATA 段：\n%s", msg)
	}
	if strings.Count(msg, "\r\n") == 0 {
		t.Error("邮件行分隔必须是 CRLF")
	}
}

func TestNormalizeBodyLineEndings(t *testing.T) {
	if got := normalizeBody("a\nb\rc\r\nd"); got != "a\r\nb\r\nc\r\nd" {
		t.Fatalf("换行未统一为 CRLF，got %q", got)
	}
}

func TestParseSenderAcceptsDisplayForm(t *testing.T) {
	addr, header := parseSender("Bot Name <bot@example.test>", "")
	if addr != "bot@example.test" {
		t.Errorf("MAIL FROM 应为纯邮箱，got %q", addr)
	}
	if !strings.HasSuffix(header, "<bot@example.test>") {
		t.Errorf("From 头应保留显示名，got %q", header)
	}

	// SMTP_FROM 配成「显示名 <邮箱>」时，显示名要回落到它自身
	if _, h2 := parseSender("躬行助手 <bot@example.test>", ""); !strings.HasPrefix(strings.ToUpper(h2), "=?UTF-8?") {
		t.Errorf("中文显示名未编码，got %q", h2)
	}
}

func TestSendWebhookPayloads(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		wantKey string
	}{
		{"dingtalk", "dingtalk", "msgtype"},
		{"wecom", "wecom", "msgtype"},
		{"feishu", "feishu", "msg_type"},
		{"generic", "generic", "title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Errorf("webhook 收到非 JSON 载荷: %v (%s)", err, raw)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			cfg := testConfig()
			cfg.NotifyWebhookURL = srv.URL
			cfg.NotifyWebhookType = tc.typ
			if err := SendWebhook(cfg, "标题", "内容"); err != nil {
				t.Fatalf("SendWebhook: %v", err)
			}
			if _, ok := got[tc.wantKey]; !ok {
				t.Fatalf("载荷字段不符，期望含 %q，实际 %v", tc.wantKey, got)
			}
		})
	}
}

func TestSendWebhookTimeoutIsBounded(t *testing.T) {
	// 服务端挂住不回，客户端必须在 WebhookTimeout 内放弃，而不是拖死 worker
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.NotifyWebhookURL = srv.URL
	cfg.WebhookTimeout = 100 * time.Millisecond

	start := time.Now()
	if err := SendWebhook(cfg, "t", "c"); err == nil {
		t.Fatal("超时应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("超时未生效，耗时 %v", elapsed)
	}
}

func TestNotifierEnqueueAfterStopIsSafe(t *testing.T) {
	cfg := testConfig()
	cfg.NotifyWorkers = 1
	cfg.NotifyQueueSize = 8
	n := NewNotifier(cfg)
	n.Start()
	n.Stop(time.Second)

	// 停止后再入队不得 panic、也不得阻塞（调度器关停顺序不对时会走到这里）
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10; i++ {
			n.Enqueue(TaskNotice{Title: "迟到的通知", Content: "x", EmailTo: "a@example.test"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("停止后 Enqueue 阻塞")
	}
}

// TestNotifierEnqueueDuringStopDoesNotPanic 盯住一个真实崩溃：
// 原先 Enqueue 用 select 把「已停止」和「发送」并列，两个分支同时就绪时 Go 随机选路，
// 选中发送就是 send on closed channel，直接把整个进程带下去。
func TestNotifierEnqueueDuringStopDoesNotPanic(t *testing.T) {
	for round := 0; round < 50; round++ {
		cfg := testConfig()
		cfg.NotifyWorkers = 1
		cfg.NotifyQueueSize = 4
		n := NewNotifier(cfg)
		n.Start()

		var wg sync.WaitGroup
		for producer := 0; producer < 4; producer++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					n.Enqueue(TaskNotice{Title: "t", Content: "c"})
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Stop(3 * time.Second)
		}()
		wg.Wait()
	}
}

func TestNotifierDrainsQueuedItemsOnStop(t *testing.T) {
	var mu sync.Mutex
	var delivered int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		delivered++
		mu.Unlock()
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.NotifyWebhookURL = srv.URL
	cfg.NotifyWorkers = 1
	cfg.NotifyQueueSize = 16
	n := NewNotifier(cfg)
	n.Start()
	for i := 0; i < 5; i++ {
		n.Enqueue(TaskNotice{Title: "t", Content: "c"})
	}
	// Stop 必须等队列排空：否则停机时最后几条通知会直接蒸发
	n.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if delivered != 5 {
		t.Fatalf("Stop 应等待队列排空，实际投递 %d 条", delivered)
	}
}
