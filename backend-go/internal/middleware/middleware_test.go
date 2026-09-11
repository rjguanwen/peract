package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"taskbackend/internal/config"
)

func testCfg() *config.Config {
	return &config.Config{
		ProjectName: "躬行测试",
		SecretKey:   "middleware-test-secret-key-0123456789-abcdefghij",
	}
}

// newTestAuth 只需要令牌能力，不触库，因此 db 传 nil；用到用户查询的用例另配。
func newTestAuth(t *testing.T) *Auth {
	t.Helper()
	a := NewAuth(testCfg(), nil)
	t.Cleanup(a.Stop)
	return a
}

// ---- 限流 ----

func TestRateLimiterWindowAndReset(t *testing.T) {
	lim := NewRateLimiter(3, time.Minute)
	defer lim.Stop()

	for i := 0; i < 3; i++ {
		if !lim.Allow("k") {
			t.Fatalf("第 %d 次应放行（配额 3）", i+1)
		}
	}
	if lim.Allow("k") {
		t.Fatal("超出配额仍被放行")
	}
	if other := lim.Allow("另一个 key"); !other {
		t.Fatal("限流按 key 隔离，不应误伤其它 key")
	}

	retry := lim.RetryAfter("k")
	if retry <= 0 || retry > time.Minute {
		t.Fatalf("RetryAfter 应落在 (0, 窗口] 内，got %v", retry)
	}
	if lim.RetryAfter("从未出现的 key") != 0 {
		t.Fatal("未知 key 的 RetryAfter 应为 0")
	}

	lim.Reset("k")
	if !lim.Allow("k") {
		t.Fatal("Reset 后应重新可用（登录成功要能解除锁定）")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	lim := NewRateLimiter(1, 120*time.Millisecond)
	defer lim.Stop()

	if !lim.Allow("k") {
		t.Fatal("首次应放行")
	}
	if lim.Allow("k") {
		t.Fatal("窗口内第二次应被拒")
	}
	time.Sleep(150 * time.Millisecond)
	if !lim.Allow("k") {
		t.Fatal("窗口过后应重新放行")
	}
}

func TestRateLimiterSweepReclaimsMemory(t *testing.T) {
	// sweep 周期取 min(window, 5min)，这里用极短窗口让它真的跑起来
	lim := NewRateLimiter(1, 80*time.Millisecond)
	defer lim.Stop()

	for i := 0; i < 50; i++ {
		lim.Allow(string(rune('a'+i%26)) + "-key")
	}
	time.Sleep(400 * time.Millisecond)

	lim.mu.Lock()
	n := len(lim.windows)
	lim.mu.Unlock()
	if n != 0 {
		t.Fatalf("过期窗口未被回收，残留 %d 条（长期运行即内存泄漏）", n)
	}
}

func TestRateLimiterClampsNonsenseConfig(t *testing.T) {
	lim := NewRateLimiter(0, 0)
	defer lim.Stop()
	if !lim.Allow("k") {
		t.Fatal("limit=0 应被收敛到 1，首次必须放行")
	}
	if lim.Allow("k") {
		t.Fatal("limit=0 收敛后仍应只允许一次")
	}
	if lim.window <= 0 {
		t.Fatalf("window=0 应回落到正值，got %v", lim.window)
	}
	// Stop 幂等：main.go 与测试都可能重复调用
	lim.Stop()
}

func TestRateLimiterConcurrentAllow(t *testing.T) {
	lim := NewRateLimiter(200, time.Minute)
	defer lim.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				lim.Allow("shared")
				lim.RetryAfter("shared")
			}
		}()
	}
	wg.Wait()
	// 配额 200、请求 400：放行次数必须恰好等于配额，计数不能因竞态而漏增
	allowed := 0
	for i := 0; i < 400; i++ {
		if lim.Allow("shared") {
			allowed++
		}
	}
	if allowed != 0 {
		t.Fatalf("配额应已耗尽，仍放行了 %d 次", allowed)
	}
}

// ---- 令牌 ----

func TestCreateTokenRoundTrip(t *testing.T) {
	a := newTestAuth(t)
	token, err := a.CreateToken(42)
	if err != nil {
		t.Fatalf("签发令牌: %v", err)
	}
	claims, err := a.parse(token)
	if err != nil {
		t.Fatalf("解析自家令牌失败: %v", err)
	}
	if id, ok := claimUint(claims["sub"]); !ok || id != 42 {
		t.Fatalf("sub 回读不对：%v", claims["sub"])
	}
	// 令牌必须逐条唯一，否则吊销一条会殃及同一秒内的另一条
	another, _ := a.CreateToken(42)
	if another == token {
		t.Fatal("同一秒内两次签发得到了完全相同的令牌")
	}
}

func TestParseRejectsForgedTokens(t *testing.T) {
	a := newTestAuth(t)
	cfg := testCfg()
	future := time.Now().Add(time.Hour).Unix()

	// alg=none：经典 JWT 绕过
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": 1, "iat": time.Now().Unix(), "iss": cfg.ProjectName, "exp": future,
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("构造 none 令牌: %v", err)
	}

	//  issuer 不符：拿别的系统的同算法令牌来撞
	wrongIssuer := sign(t, cfg, jwt.MapClaims{"sub": 1, "iat": time.Now().Unix(), "iss": "另一个系统", "exp": future}, "another-secret-another-secret-another-secret-0000")
	// 密钥不符
	wrongSecret := sign(t, cfg, jwt.MapClaims{"sub": 1, "iat": time.Now().Unix(), "iss": cfg.ProjectName, "exp": future}, "attacker-secret-attacker-secret-attacker-secret")
	// 无 exp：永不过期的令牌一律拒
	noExpiry := sign(t, cfg, jwt.MapClaims{"sub": 1, "iat": time.Now().Unix(), "iss": cfg.ProjectName}, cfg.SecretKey)
	// 已过期
	expired := sign(t, cfg, jwt.MapClaims{"sub": 1, "iat": time.Now().Add(-2 * time.Hour).Unix(), "iss": cfg.ProjectName, "exp": time.Now().Add(-time.Hour).Unix()}, cfg.SecretKey)

	cases := map[string]string{
		"alg=none":   none,
		" issuer 不符": wrongIssuer,
		"密钥不符":       wrongSecret,
		"缺少 exp":     noExpiry,
		"已过期":        expired,
		"空串":         "",
		"乱码":         "not-a-jwt",
	}
	for name, tok := range cases {
		if _, err := a.parse(tok); err == nil {
			t.Errorf("%s 的令牌竟然通过了校验：%q", name, truncate(tok))
		}
	}
}

func TestRevokeTokenOnlyAffectsThatToken(t *testing.T) {
	a := newTestAuth(t)
	victim, _ := a.CreateToken(7)
	bystander, _ := a.CreateToken(7)

	a.RevokeToken(victim)
	if !a.isRevoked(victim) {
		t.Fatal("被吊销的令牌未进入黑名单")
	}
	if a.isRevoked(bystander) {
		t.Fatal("吊销殃及了同用户的另一条令牌")
	}
	// 非法串不该被记进黑名单（登出接口可能收到垃圾）
	a.RevokeToken("garbage")
	if a.isRevoked("garbage") {
		t.Fatal("无法解析的串不应出现在黑名单里")
	}
	// 过期条目按 exp 淘汰，而不是永久堆积
	sum := hashToken(victim)
	a.revokedMu.Lock()
	expiry, ok := a.revoked[sum]
	a.revokedMu.Unlock()
	if !ok {
		t.Fatal("黑名单条目丢失")
	}
	if expiry.Before(time.Now()) {
		t.Fatalf("黑名单保留时长应覆盖令牌自身有效期，got %v", expiry)
	}
}

func TestResetTokenIsScopedAndSingleUse(t *testing.T) {
	a := newTestAuth(t)
	access, _ := a.CreateToken(1)

	reset, err := a.SignResetToken("user@test.local")
	if err != nil {
		t.Fatalf("签发重置令牌: %v", err)
	}
	if email, err := a.VerifyResetToken(reset); err != nil || email != "user@test.local" {
		t.Fatalf("重置令牌校验失败: %v %q", err, email)
	}
	// 访问令牌不能当重置令牌用
	if _, err := a.VerifyResetToken(access); err == nil {
		t.Fatal("访问令牌被当成了重置令牌")
	}
	// 校验本身不消耗，只有改密成功才作废
	if _, err := a.VerifyResetToken(reset); err != nil {
		t.Fatalf("重复校验不应作废令牌: %v", err)
	}
	a.ConsumeResetToken(reset)
	if _, err := a.VerifyResetToken(reset); err == nil {
		t.Fatal("已使用的重置令牌仍可复用，一次性没兜住")
	}
}

func TestClaimIssuedAfterBoundary(t *testing.T) {
	at := time.Now().Truncate(time.Second)
	ok := jwt.MapClaims{"iat": float64(at.Unix())}
	if !claimIssuedAfter(ok, at) {
		t.Fatal("同一秒内签发的令牌应被接受（iat >= 版本号）")
	}
	if claimIssuedAfter(jwt.MapClaims{"iat": float64(at.Unix() - 1)}, at) {
		t.Fatal("早一秒的令牌必须被拒绝")
	}
	// 缺失或类型不对的 iat 一律按改造前的旧令牌处理
	if claimIssuedAfter(jwt.MapClaims{}, at) {
		t.Fatal("无 iat 的令牌应被拒绝")
	}
	if claimIssuedAfter(jwt.MapClaims{"iat": "123"}, at) {
		t.Fatal("iat 类型不符时应被拒绝，不能放宽")
	}
}

func TestClaimUintAcceptsBothForms(t *testing.T) {
	for _, v := range []any{float64(9), int(9), uint(9), "9"} {
		if got, ok := claimUint(v); !ok || got != 9 {
			t.Errorf("%T(%v) 解析为 %d/%v", v, v, got, ok)
		}
	}
	for _, v := range []any{nil, "abc", -1.0, struct{}{}} {
		if got, ok := claimUint(v); ok && got != 0 {
			t.Errorf("%v 不应被解析成 %d", v, got)
		}
	}
}

// ---- 中间件行为 ----

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc.def.ghi", "abc.def.ghi", true}, // 大小写不敏感
		{"Bearer  abc.def.ghi ", "abc.def.ghi", true},
		{"", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Token abc", "", false},
		{"Basic abc", "", false},
	}
	for _, tc := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			c.Request.Header.Set("Authorization", tc.header)
		}
		got, ok := BearerToken(c)
		if got != tc.want || ok != tc.ok {
			t.Errorf("头 %q 解析为 %q/%v，期望 %q/%v", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCORSEchoesOnlyAllowedOrigins(t *testing.T) {
	cfg := testCfg()
	cfg.CORSAllowOrigins = []string{"http://allowed.test", "http://other.test/"}
	engine := gin.New()
	engine.Use(CORS(cfg))
	engine.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	for _, tc := range []struct {
		origin     string
		wantEchoed bool
	}{
		{"http://allowed.test", true},
		{"http://OTHER.test", true}, // 忽略大小写与末尾斜杠
		{"http://evil.test", false},
		{"", false},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		engine.ServeHTTP(w, req)
		got := w.Header().Get("Access-Control-Allow-Origin")
		if tc.wantEchoed && got != tc.origin {
			t.Errorf("来源 %q 应回显 %q，实际 %q", tc.origin, tc.origin, got)
		}
		if !tc.wantEchoed && got != "" {
			t.Errorf("来源 %q 不该被放通，却得到了 %q", tc.origin, got)
		}
	}

	// 预检必须就地结束，不能打进业务处理
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "http://allowed.test")
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("预检应回 204，got %d", w.Code)
	}

	// 通配模式：显式 * 才放通任意来源
	all := testCfg()
	all.CORSAllowOrigins = []string{"*"}
	wild := gin.New()
	wild.Use(CORS(all))
	wild.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "http://anywhere.test")
	wild.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("通配模式应回 *，got %q", got)
	}
}

func TestBodySizeLimit(t *testing.T) {
	engine := gin.New()
	engine.Use(BodySizeLimit(100))
	engine.POST("/echo", func(c *gin.Context) {
		body, err := c.GetRawData()
		if err != nil {
			c.String(http.StatusBadGateway, "read failed: %v", err)
			return
		}
		c.String(http.StatusOK, "%d", len(body))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("a", 50)))
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("限额内应通过，got %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("a", 500)))
	req.ContentLength = 500
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应回 413，got %d", w.Code)
	}
}

// sign 用指定密钥按测试需要拼一枚令牌，用于构造各种畸形凭证。
func sign(t *testing.T, cfg *config.Config, claims jwt.MapClaims, secret string) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("构造测试令牌: %v", err)
	}
	return token
}

func truncate(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}
