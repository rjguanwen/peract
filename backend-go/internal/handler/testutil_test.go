package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/middleware"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

// 本文件是 handler 层的回归测试脚手架：用真实 SQLite 文件 + 真实路由注册，
// 通过 HTTP 驱动断言，而不是直接调用 handler 函数——那样会绕开中间件与
// 状态码语义，而这一轮改动恰恰大量落在这两层。

type testEnv struct {
	t      *testing.T
	cfg    *config.Config
	db     *gorm.DB
	auth   *middleware.Auth
	h      *Handler
	router *gin.Engine
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWith(t, nil)
}

// newTestEnvWith 允许单个用例在启动前收紧配置（限流阈值、体大小上限、SMTP 开关等）。
// 必须在 handler.New 之前改：限流器在构造时就按配额建好了。
func newTestEnvWith(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	cfg := &config.Config{
		ProjectName:  "躬行测试",
		Env:          "development",
		SecretKey:    "unit-test-secret-key-0123456789-abcdefghij-klmn",
		DatabaseURL:  "sqlite:///" + filepath.ToSlash(filepath.Join(dir, "test.db")),
		UploadDir:    filepath.Join(dir, "uploads"),
		AppBaseURL:   "http://test.local",
		MaxBodyBytes: 1 << 20,
		// 令牌有效期留空，走 AccessTokenExpireMinutes() 的默认回落
		// 限流配额默认给足，专门测限流的用例再单独收紧，避免互相干扰
		LoginFailLimit:  50,
		LoginFailWindow: time.Minute,
		NotifyLimit:     200,
		NotifyWindow:    time.Minute,
		NotifyWorkers:   1,
		NotifyQueueSize: 256,
	}
	if mutate != nil {
		mutate(cfg)
	}

	db, err := database.Open(cfg)
	if err != nil {
		t.Fatalf("打开测试数据库: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移测试数据库: %v", err)
	}
	// Windows 下不先关掉连接池，t.TempDir 的清理会因文件占用失败
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	auth := middleware.NewAuth(cfg, db)
	// 通知投递器只建不 Start：入队不阻塞，测试也不该去连外部服务
	notifier := service.NewNotifier(cfg)
	h := New(db, cfg, auth, notifier)

	engine := gin.New()
	engine.Use(gin.Recovery())
	// 与 main.go 一致：不配受信代理就不采信 X-Forwarded-For
	if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		t.Fatalf("设置受信代理: %v", err)
	}
	h.RegisterRoutes(engine)

	t.Cleanup(func() {
		h.Close()
		auth.Stop()
	})

	return &testEnv{t: t, cfg: cfg, db: db, auth: auth, h: h, router: engine}
}

// call 一次 HTTP 调用的结果。
type call struct {
	Status int
	Header http.Header
	Body   []byte
}

func (c call) Require(t *testing.T, status int) call {
	t.Helper()
	if c.Status != status {
		t.Fatalf("HTTP 状态期望 %d，实际 %d；响应体：%s", status, c.Status, c.Body)
	}
	return c
}

func (c call) Map(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(c.Body, &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, c.Body)
	}
	return out
}

func (c call) Into(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(c.Body, v); err != nil {
		t.Fatalf("解码响应失败: %v\n%s", err, c.Body)
	}
}

// Detail 取出统一错误响应里的 detail。
func (c call) Detail(t *testing.T) string {
	t.Helper()
	return Str(t, c.Map(t), "detail")
}

func (c call) Keys() []string {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(c.Body, &raw)
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	return keys
}

// Str / Num / Bool / Has 是响应字段的取值断言，缺失或类型不符一律测试失败，
// 免得拼写错的键名静默返回零值、把断言变成永真。
func Str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("响应缺少字段 %q：%v", key, m)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("字段 %q 期望字符串，实际 %T（%v）", key, v, v)
	}
	return s
}

func Num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("响应缺少字段 %q：%v", key, m)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("字段 %q 期望数字，实际 %T（%v）", key, v, v)
	}
	return n
}

func Bool(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("响应缺少字段 %q：%v", key, m)
	}
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("字段 %q 期望布尔，实际 %T（%v）", key, v, v)
	}
	return b
}

func Has(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key]
	if !ok {
		return false
	}
	return v != nil
}

func (e *testEnv) request(method, path string, body io.Reader, contentType, token string) call {
	e.t.Helper()
	var reader io.Reader = body
	if reader == nil {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return call{Status: w.Code, Header: w.Header(), Body: w.Body.Bytes()}
}

func (e *testEnv) get(path, token string) call {
	e.t.Helper()
	return e.request(http.MethodGet, path, nil, "", token)
}

func (e *testEnv) post(path string, payload any, token string) call {
	e.t.Helper()
	return e.request(http.MethodPost, path, jsonBody(e.t, payload), "application/json", token)
}

func (e *testEnv) patch(path string, payload any, token string) call {
	e.t.Helper()
	return e.request(http.MethodPatch, path, jsonBody(e.t, payload), "application/json", token)
}

func (e *testEnv) put(path string, payload any, token string) call {
	e.t.Helper()
	return e.request(http.MethodPut, path, jsonBody(e.t, payload), "application/json", token)
}

func (e *testEnv) del(path, token string) call {
	e.t.Helper()
	return e.request(http.MethodDelete, path, nil, "", token)
}

// postForm 用于登录接口（OAuth2 password form）。
func (e *testEnv) postForm(path string, form url.Values, token string) call {
	e.t.Helper()
	return e.request(http.MethodPost, path, strings.NewReader(form.Encode()),
		"application/x-www-form-urlencoded", token)
}

func (e *testEnv) postMultipart(path string, fields map[string]string, fileField, fileName, contentType string, content []byte, token string) call {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			e.t.Fatalf("写表单字段: %v", err)
		}
	}
	if fileField != "" {
		part, err := mw.CreateFormFile(fileField, fileName)
		if err != nil {
			e.t.Fatalf("建表单文件: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			e.t.Fatalf("写表单内容: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		e.t.Fatalf("关闭表单: %v", err)
	}
	return e.request(http.MethodPut, path, &buf, mw.FormDataContentType(), token)
}

func jsonBody(t *testing.T, payload any) io.Reader {
	t.Helper()
	if payload == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码请求体: %v", err)
	}
	return bytes.NewReader(raw)
}

// ---- 数据准备 ----

func (e *testEnv) addUser(username, email, password, role string) *model.User {
	e.t.Helper()
	u := model.NewUser(username, email, password, role, username+"姓名")
	if err := e.db.Create(u).Error; err != nil {
		e.t.Fatalf("创建用户 %s: %v", username, err)
	}
	return u
}

func (e *testEnv) addAdmin(username string) *model.User {
	e.t.Helper()
	return e.addUser(username, username+"@test.local", "pass1234", model.RoleAdmin)
}

func (e *testEnv) addMember(username string) *model.User {
	e.t.Helper()
	return e.addUser(username, username+"@test.local", "pass1234", model.RoleUser)
}

// login 走真实登录接口拿令牌，顺带覆盖表单解析与令牌签发链路。
func (e *testEnv) login(username, password string) string {
	e.t.Helper()
	res := e.postForm("/api/v1/auth/login", url.Values{
		"username": {username},
		"password": {password},
	}, "").Require(e.t, http.StatusOK)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	res.Into(e.t, &out)
	if out.AccessToken == "" {
		e.t.Fatalf("登录未返回令牌：%s", out.AccessToken)
	}
	return out.AccessToken
}

func (e *testEnv) adminToken() string { return e.login("root", "pass1234") }

// taskJSON 只声明断言需要的字段：时间类字段按原始字符串接收，避免依赖自定义 Marshal 细节。
type taskJSON struct {
	ID           uint    `json:"id"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	Priority     string  `json:"priority"`
	AssigneeID   *uint   `json:"assignee_id"`
	AssigneeName *string `json:"assignee_name"`
	DueDate      *string `json:"due_date"`
	Progress     int     `json:"progress"`
}

type pageOf struct {
	Items    []json.RawMessage `json:"items"`
	Total    float64           `json:"total"`
	Page     float64           `json:"page"`
	PageSize float64           `json:"page_size"`
}

// createTask 以指定身份新建任务并回读。
func (e *testEnv) createTask(token string, payload map[string]any) taskJSON {
	e.t.Helper()
	res := e.post("/api/v1/tasks", payload, token).Require(e.t, http.StatusCreated)
	var task taskJSON
	res.Into(e.t, &task)
	if task.ID == 0 {
		e.t.Fatalf("新建任务未返回 ID：%s", res.Body)
	}
	return task
}

func (e *testEnv) fetchTask(token string, id uint) taskJSON {
	e.t.Helper()
	res := e.get("/api/v1/tasks/"+u2s(id), token).Require(e.t, http.StatusOK)
	var task taskJSON
	res.Into(e.t, &task)
	return task
}

func u2s(v uint) string { return strconv.FormatUint(uint64(v), 10) }

// signToken 手工签一个指定 iat 的令牌，用于验证「口令版本号作废旧会话」。
// 不能靠 sleep 一个真实秒差来造旧令牌，那会把测试变成计时器。
func (e *testEnv) signToken(userID uint, iat time.Time) string {
	e.t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"iat": iat.Unix(),
		"iss": e.cfg.ProjectName,
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(e.cfg.SecretKey))
	if err != nil {
		e.t.Fatalf("签发测试令牌: %v", err)
	}
	return token
}

// urlValues 以成对参数构造表单，省去测试里反复写 url.Values{…} 的噪声。
func urlValues(kv ...string) url.Values {
	if len(kv)%2 != 0 {
		panic("urlValues 需要成对参数")
	}
	form := make(url.Values, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		form.Set(kv[i], kv[i+1])
	}
	return form
}

// taskIDs 从分页响应里抽出任务 id 列表，用于「在不在列表里」这类断言。
func taskIDs(t *testing.T, c call) []uint {
	t.Helper()
	var page pageOf
	c.Into(t, &page)
	ids := make([]uint, 0, len(page.Items))
	for _, raw := range page.Items {
		var one struct {
			ID uint `json:"id"`
		}
		if err := json.Unmarshal(raw, &one); err != nil {
			t.Fatalf("解码列表项失败: %v\n%s", err, raw)
		}
		ids = append(ids, one.ID)
	}
	return ids
}

func contains(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// addReminder 直接落库提醒，绕开创建接口，以便精确控制 sent/remind_at 组合。
func (e *testEnv) addReminder(taskID uint, userID uint, remindAt time.Time, sent, read bool) *model.Reminder {
	e.t.Helper()
	r := &model.Reminder{TaskID: taskID, UserID: &userID, RemindAt: remindAt, RemindType: model.RemindManual, Sent: sent}
	if read {
		now := time.Now()
		r.ReadAt = &now
	}
	if err := e.db.Create(r).Error; err != nil {
		e.t.Fatalf("创建提醒: %v", err)
	}
	return r
}
