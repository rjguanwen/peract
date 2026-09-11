package handler

import (
	"bytes"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
)

// 本文件盯的是安全语义：限流、去枚举、答案哈希、令牌一次性与口令版本号。
// 这些行为「返回 200 且看着没问题」时最容易静默失守，必须用断言钉死。

func TestLoginRateLimitLocksWithRetryAfter(t *testing.T) {
	e := newTestEnvWith(t, func(cfg *config.Config) {
		cfg.LoginFailLimit = 2
		cfg.LoginFailWindow = time.Minute
	})
	e.addAdmin("root")

	wrong := url.Values{"username": {"root"}, "password": {"nope"}}
	e.postForm("/api/v1/auth/login", wrong, "").Require(t, http.StatusBadRequest)
	e.postForm("/api/v1/auth/login", wrong, "").Require(t, http.StatusBadRequest)

	locked := e.postForm("/api/v1/auth/login", wrong, "").Require(t, http.StatusTooManyRequests)
	// 前端要靠 Retry-After 决定倒计时，缺了这个头只能瞎猜
	if locked.Header.Get("Retry-After") == "" {
		t.Fatal("429 未携带 Retry-After 头")
	}
	// 锁定期内连正确口令也必须拒掉，否则限流形同虚设
	e.postForm("/api/v1/auth/login", urlValues("username", "root", "password", "pass1234"), "").
		Require(t, http.StatusTooManyRequests)
	// 计数按「来源 + 用户名」，不应误伤同来源的其他账号
	e.postForm("/api/v1/auth/login", urlValues("username", "someone", "password", "x"), "").
		Require(t, http.StatusBadRequest)
}

func TestPasswordRecoveryShapeIsUniform(t *testing.T) {
	e := newTestEnv(t)
	e.addMember("member")
	disabled := e.addMember("disabled")
	if err := e.db.Model(&model.User{}).Where("id = ?", disabled.ID).
		Update("is_active", false).Error; err != nil {
		t.Fatalf("停用账号: %v", err)
	}

	unknown := e.get("/api/v1/auth/forgot?email=ghost@test.local", "").Require(t, http.StatusOK)
	off := e.get("/api/v1/auth/forgot?email=disabled@test.local", "").Require(t, http.StatusOK)
	// 未注册与已停用必须完全同形同值，接口不能当枚举器
	if string(unknown.Body) != string(off.Body) {
		t.Fatalf("未注册与已停用响应不同：\n%s\n%s", unknown.Body, off.Body)
	}
	for _, res := range []call{unknown, off} {
		m := res.Map(t)
		for _, key := range []string{"exists", "password_hint", "security_question", "has_security", "answer_legacy"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("找回响应缺少字段 %q：%v", key, m)
			}
		}
		if Bool(t, m, "exists") {
			t.Fatalf("停用账号不应被确认存在：%v", m)
		}
	}

	on := e.get("/api/v1/auth/forgot?email=member@test.local", "").Require(t, http.StatusOK).Map(t)
	if !Bool(t, on, "exists") {
		t.Fatal("在册成员应被确认存在")
	}
	// exists 是给前端决定「走哪条找回路径」的，它之外不再多给任何线索
	if Bool(t, on, "has_security") || Bool(t, on, "answer_legacy") {
		t.Fatalf("未设置安全答案的成员不应出现答案相关标志：%v", on)
	}
}

func TestForgotSendKeepsExistencePrivateWhenSMTPReady(t *testing.T) {
	e := newTestEnvWith(t, func(cfg *config.Config) {
		// SMTP 就绪才是常态；开发模式（未配置）会直接吐重置链接，那本就只限本地
		cfg.SMTPHost = "smtp.test.local"
		cfg.SMTPUser = "bot@test.local"
		cfg.SMTPFrom = "bot@test.local"
	})
	e.addMember("member")

	known := e.post("/api/v1/auth/forgot/send", map[string]any{"email": "member@test.local"}, "").
		Require(t, http.StatusOK)
	ghost := e.post("/api/v1/auth/forgot/send", map[string]any{"email": "ghost@test.local"}, "").
		Require(t, http.StatusOK)
	if string(known.Body) != string(ghost.Body) {
		t.Fatalf("发信接口泄露了邮箱是否在册：\n%s\n%s", known.Body, ghost.Body)
	}
	if !Bool(t, known.Map(t), "sent") {
		t.Fatal("在册邮箱应回 sent=true")
	}
}

func TestForgotSendDoesNotLeakResetLinkInProduction(t *testing.T) {
	// 「生产环境 + 漏配 SMTP」是最常见的上线状态：这时既发不出信，
	// 也绝不能走开发便利把带有效令牌的重置链接回给调用方——那等于
	// 任何知道邮箱的人都能直接改他人密码。
	e := newTestEnvWith(t, func(cfg *config.Config) { cfg.Env = "production" })
	e.addMember("member")

	var bodies [][]byte
	for _, email := range []string{"member@test.local", "ghost@test.local"} {
		res := e.post("/api/v1/auth/forgot/send", map[string]any{"email": email}, "")
		if res.Status != http.StatusServiceUnavailable {
			t.Fatalf("%s 应回 503，实际 %d：%s", email, res.Status, res.Body)
		}
		if bytes.Contains(res.Body, []byte("token=")) || bytes.Contains(res.Body, []byte("reset_url")) {
			t.Fatalf("503 响应里漏出了重置凭证：%s", res.Body)
		}
		bodies = append(bodies, res.Body)
	}
	// 两条必须一字不差，否则 503 本身又成了一次存在性探测
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("生产未配 SMTP 时响应出卖了邮箱在册情况：\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestSecurityAnswerIsHashedAndChecked(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	token := e.login("member", "pass1234")

	e.put("/api/v1/auth/security", map[string]any{
		"securityQuestion": "母校叫什么？",
		"securityAnswer":   "  第一中学  ",
	}, token).Require(t, http.StatusOK)

	var stored model.User
	if err := e.db.First(&stored, member.ID).Error; err != nil {
		t.Fatalf("回读用户: %v", err)
	}
	if !strings.HasPrefix(stored.SecurityAnswer, "$2") {
		t.Fatalf("安全答案未哈希入库，got %q", stored.SecurityAnswer)
	}
	if strings.Contains(stored.SecurityAnswer, "第一中学") {
		t.Fatal("安全答案明文入库")
	}

	// 只改问题不改答案会造成题答错配，必须拒
	e.put("/api/v1/auth/security", map[string]any{"securityQuestion": "换个问题"}, token).
		Require(t, http.StatusBadRequest)

	info := e.get("/api/v1/auth/security", token).Require(t, http.StatusOK).Map(t)
	if !Bool(t, info, "has_security") || Bool(t, info, "answer_legacy") {
		t.Fatalf("安全设置回显不对：%v", info)
	}
	if _, leaked := info["security_answer"]; leaked {
		t.Fatal("安全设置接口回传了答案")
	}

	// 首尾空白与大小写差异不应影响校验（答案已归一化）
	e.post("/api/v1/auth/forgot/reset", map[string]any{
		"email": "member@test.local", "answer": "第一中学", "newPassword": "brandnew1",
	}, "").Require(t, http.StatusOK)
	e.login("member", "brandnew1")
}

func TestResetByAnswerCannotEnumerateAndLegacyIsLocked(t *testing.T) {
	e := newTestEnv(t)
	e.addMember("member")
	token := e.login("member", "pass1234")
	e.put("/api/v1/auth/security", map[string]any{
		"securityQuestion": "母校叫什么？", "securityAnswer": "第一中学",
	}, token).Require(t, http.StatusOK)

	wrongAnswer := e.post("/api/v1/auth/forgot/reset", map[string]any{
		"email": "member@test.local", "answer": "乱猜", "newPassword": "hackerpw1",
	}, "").Require(t, http.StatusBadRequest).Detail(t)
	unknownEmail := e.post("/api/v1/auth/forgot/reset", map[string]any{
		"email": "ghost@test.local", "answer": "乱猜", "newPassword": "hackerpw1",
	}, "").Require(t, http.StatusBadRequest).Detail(t)
	if wrongAnswer != unknownEmail {
		t.Fatalf("答案错与邮箱不存在的提示不同，可被用于枚举： %q vs %q", wrongAnswer, unknownEmail)
	}

	// 存量明文答案：既不参与校验，也要明确 423 引导重设，而不是让人反复撞「答案不正确」
	legacy := e.addMember("legacy")
	e.addAdmin("root")
	if err := e.db.Model(&model.User{}).Where("id = ?", legacy.ID).Updates(map[string]any{
		"security_question": "母校叫什么？", "security_answer": "老明文答案",
	}).Error; err != nil {
		t.Fatalf("写入存量明文答案: %v", err)
	}

	recovery := e.get("/api/v1/auth/forgot?email=legacy@test.local", "").Require(t, http.StatusOK).Map(t)
	if !Bool(t, recovery, "answer_legacy") || Bool(t, recovery, "has_security") {
		t.Fatalf("未识别出存量明文答案：%v", recovery)
	}
	if q := Str(t, recovery, "security_question"); q != "" {
		t.Fatalf("存量明文答案的账号不该继续吐题目引导用户去撞：%q", q)
	}

	locked := e.post("/api/v1/auth/forgot/reset", map[string]any{
		"email": "legacy@test.local", "answer": "老明文答案", "newPassword": "hackerpw1",
	}, "").Require(t, http.StatusLocked)
	if !strings.Contains(locked.Detail(t), "明文") {
		t.Fatalf("423 提示未说明原因：%q", locked.Detail(t))
	}
	// 明文答案不得成为改密凭据
	if got := e.postForm("/api/v1/auth/login", urlValues("username", "legacy", "password", "hackerpw1"), "").Status; got == http.StatusOK {
		t.Fatal("用存量明文答案改密成功了，等于明文当凭证")
	}
}

func TestResetTokenIsSingleUse(t *testing.T) {
	e := newTestEnv(t)
	e.addMember("member")

	res := e.post("/api/v1/auth/forgot/send", map[string]any{"email": "member@test.local"}, "").
		Require(t, http.StatusOK)
	// 未配置 SMTP 时后端直发链接，这是开发通道的既有约定
	dev := res.Map(t)
	if !Bool(t, dev, "dev") {
		t.Fatalf("未配置 SMTP 时应回开发模式重置链接：%v", dev)
	}
	raw := Str(t, dev, "reset_url")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("重置链接不可解析: %v", err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatalf("重置链接里没有 token：%q", raw)
	}

	e.post("/api/v1/auth/reset", map[string]any{"token": token, "newPassword": "first123456"}, "").
		Require(t, http.StatusOK)
	// 第二次使用同一令牌：JWT 本身还在有效期内，一次性只能靠服务端黑名单
	e.post("/api/v1/auth/reset", map[string]any{"token": token, "newPassword": "second123456"}, "").
		Require(t, http.StatusBadRequest)
	e.login("member", "first123456")
	if got := e.postForm("/api/v1/auth/login", urlValues("username", "member", "password", "second123456"), "").Status; got == http.StatusOK {
		t.Fatal("被重放的令牌改了密码")
	}
	// 访问令牌不该能当重置令牌用，反之亦然
	e.post("/api/v1/auth/reset", map[string]any{"token": e.login("member", "first123456"), "newPassword": "third123456"}, "").
		Require(t, http.StatusBadRequest)
}

func TestPasswordChangeInvalidatesOlderSessions(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	session := e.login("member", "pass1234")

	// 改密前签发、且不在黑名单里的另一条会话（模拟被窃取的旧令牌）
	stale := e.signToken(member.ID, time.Now().Add(-time.Hour))
	e.get("/api/v1/auth/me", stale).Require(t, http.StatusOK)

	e.put("/api/v1/auth/password", map[string]any{"oldPassword": "pass1234", "newPassword": "updated1"}, session).
		Require(t, http.StatusOK)

	// 本次会话被吊销
	e.get("/api/v1/auth/me", session).Require(t, http.StatusUnauthorized)
	// 改密前签发的其它会话按口令版本号作废
	e.get("/api/v1/auth/me", stale).Require(t, http.StatusUnauthorized)
	// 改密后新签发的会话正常（带 jti 才能保证不与被吊销那条重合成同一字符串）
	fresh := e.login("member", "updated1")
	if fresh == session {
		t.Fatal("改密后重新登录拿回了同一条已吊销令牌")
	}
	e.get("/api/v1/auth/me", fresh).Require(t, http.StatusOK)
}

func TestPasswordChangedAtSurvivesDatabaseRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	token := e.login("member", "pass1234")

	before := time.Now().Add(-time.Second)
	e.put("/api/v1/auth/password", map[string]any{"oldPassword": "pass1234", "newPassword": "updated1"}, token).
		Require(t, http.StatusOK)

	var user model.User
	if err := e.db.Select("id, password_changed_at").First(&user, member.ID).Error; err != nil {
		t.Fatalf("回读用户: %v", err)
	}
	if user.PasswordChangedAt == nil {
		t.Fatal("password_changed_at 未落库，旧会话无法作废")
	}
	// 写入用 UTC 截断到秒，读回必须是同一时刻。
	// 驱动若按本地时区回读，这里会偏移整个时区差，作废逻辑就会变成「永不相等」或「永远相等」。
	delta := time.Since(before)
	if user.PasswordChangedAt.After(time.Now().Add(time.Second)) ||
		user.PasswordChangedAt.Add(2*time.Minute).Before(before) {
		t.Fatalf("password_changed_at 往返后偏离真实时刻：got %v（now=%v，已过 %v）",
			user.PasswordChangedAt, time.Now(), delta)
	}
	if got := user.PasswordChangedAt.UTC().Format(time.RFC3339); !strings.HasSuffix(got, "Z") {
		t.Fatalf("格式化后不是 UTC：%q", got)
	}
}

func TestRequestBodySizeLimit(t *testing.T) {
	e := newTestEnvWith(t, func(cfg *config.Config) { cfg.MaxBodyBytes = 2048 })
	e.addMember("member")

	big := map[string]any{"title": strings.Repeat("长", 4000), "description": strings.Repeat("x", 8192)}
	e.post("/api/v1/tasks", big, e.login("member", "pass1234")).Require(t, http.StatusRequestEntityTooLarge)

	small := map[string]any{"title": "正常长度"}
	e.post("/api/v1/tasks", small, e.login("member", "pass1234")).Require(t, http.StatusCreated)
}

func TestAvatarUploadRejectsFakeImage(t *testing.T) {
	e := newTestEnv(t)
	e.addMember("member")
	token := e.login("member", "pass1234")

	// 扩展名与 Content-Type 都由客户端说了算，判定必须只看文件头：
	// 上传目录是对外静态暴露的，放进一个伪装成 png 的 HTML 就是存储型 XSS
	fake := []byte("<html><script>alert(1)</script></html>" + strings.Repeat(" ", 600))
	e.postMultipart("/api/v1/profile/avatar", nil, "file", "evil.png", "image/png", fake, token).
		Require(t, http.StatusBadRequest)

	empty := e.postMultipart("/api/v1/profile/avatar", nil, "file", "x.png", "image/png", nil, token)
	if empty.Status == http.StatusOK {
		t.Fatal("空文件不应上传成功")
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x20}, 600)...)
	res := e.postMultipart("/api/v1/profile/avatar", nil, "file", "../../escape.png", "image/png", png, token).
		Require(t, http.StatusOK)
	out := res.Map(t)
	avatarURL := Str(t, out, "avatar_url")
	if !strings.HasPrefix(avatarURL, "/uploads/avatars/") || !strings.HasSuffix(avatarURL, ".png") {
		t.Fatalf("头像地址不对：%q", avatarURL)
	}
	if strings.Contains(avatarURL, "..") || strings.Contains(avatarURL, "escape") {
		t.Fatalf("用户提供的文件名参与了落盘路径：%q", avatarURL)
	}

	// 落盘位置必须仍在 avatars 目录内
	name := filepath.Base(strings.TrimPrefix(avatarURL, "/uploads/avatars/"))
	dest := filepath.Join(e.cfg.UploadDir, "avatars", name)
	rel, err := filepath.Rel(filepath.Join(e.cfg.UploadDir, "avatars"), dest)
	if err != nil || rel != name || strings.HasPrefix(rel, "..") {
		t.Fatalf("头像落到了目录外：%q（%v）", dest, err)
	}
	if _, err := filepath.EvalSymlinks(dest); err != nil {
		t.Fatalf("头像未落在预期位置: %v", err)
	}
}
