package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/config"
	"taskbackend/internal/database"
	"taskbackend/internal/model"
	"taskbackend/internal/onelink"
	"taskbackend/internal/perms"
	"taskbackend/internal/service"
)

// 本文件是 handler 层的回归测试脚手架：用真实 SQLite 文件 + 真实路由注册，
// 通过 HTTP 驱动断言，而不是直接调用 handler 函数——那样会绕开中间件与
// 状态码语义，而这一轮改动恰恰大量落在这两层。
//
// 身份部分在接入 OneLink 之后换了做法, 换法与理由见 sessionFor 的注释:
// 会话不再由 /auth/login 签发, 而是直接写进 SDK 的 Store。

type testEnv struct {
	t        *testing.T
	cfg      *config.Config
	db       *gorm.DB
	guard    *onelinksdk.Guard
	store    *onelink.Store
	profiles *onelink.Profiles
	h        *Handler
	router   *gin.Engine

	// perms 记着"平台上给这个人勾了哪些权限点"。它模拟的是平台侧的角色授权 ——
	// 应用侧已经没有任何角色概念, 所以这份映射只活在测试夹具里, 不落库。
	perms map[uint][]string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWith(t, nil)
}

// newTestEnvWith 允许单个用例在启动前收紧配置（体大小上限、SMTP 开关等）。
// 必须在 handler.New 之前改。
func newTestEnvWith(t *testing.T, mutate func(*config.Config)) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	cfg := &config.Config{
		ProjectName: "躬行测试",
		Env:         "development",
		DatabaseURL: "sqlite:///" + filepath.ToSlash(filepath.Join(dir, "test.db")),
		UploadDir:   filepath.Join(dir, "uploads"),
		AppBaseURL:  "http://test.local",
		// 接入配置给全: 缺了它 RegisterRoutes 会整族跳过(见 handler.go), 那样所有用例
		// 都会得到 404, 而 404 看起来像"路由写错了", 归因方向完全被带偏。
		OnelinkAppCode:      "task-system",
		OnelinkBaseURL:      "http://onelink.test",
		OnelinkPortalURL:    "http://portal.test",
		OnelinkAppSecret:    "unit-test-app-secret",
		OnelinkPermPullPath: "/onelink/perm-manifest",
		OnelinkUnreadPath:   "/onelink/unread",
		MaxBodyBytes:        1 << 20,
		NotifyWorkers:    1,
		NotifyQueueSize:  256,
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

	client, err := onelinksdk.New(onelinksdk.Config{
		BaseURL:   cfg.OnelinkBaseURL,
		AppCode:   cfg.OnelinkAppCode,
		AppSecret: cfg.OnelinkAppSecret,
	})
	if err != nil {
		t.Fatalf("构造 OneLink 客户端: %v", err)
	}
	store := onelink.NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("建会话表: %v", err)
	}
	guard, err := onelinksdk.NewGuard(onelinksdk.GuardConfig{
		Client:    client,
		PortalURL: cfg.OnelinkPortalURL,
		Store:     store,
		// 离线验签与存活轮询都关掉, 理由见 sessionFor 的注释: 它们要的是**平台的密钥**
		// 与**平台的存活接口**, 而那两样是 SDK 自己的用例覆盖的东西。关掉之后守卫走的
		// 路径(读 Store → 判到期 → 建 Principal)与生产完全一致。
		AliveInterval: -1,
		OnUnauthenticated: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"登录已失效"}`))
		}),
	})
	if err != nil {
		t.Fatalf("构造 OneLink 守卫: %v", err)
	}

	// 权限清单端点用真的那一份清单(perms.Manifest): 夹具不另造一份, 否则这里测过的
	// 东西与生产跑的不是同一个 —— 而这个端点的全部内容就是那份清单。
	permPull, err := onelinksdk.NewPermPullHandler(onelinksdk.PullConfig{
		Client:   client,
		Manifest: perms.Manifest,
	})
	if err != nil {
		t.Fatalf("装配权限清单端点: %v", err)
	}

	// 用户态端点用**真的那一个回调**(CountUnreadByPlatformUser), 不另造一份: 这一层要测的
	// 正是"口径" —— 门户角标与躬行自己的未读数接口必须同值, 而换一个假回调就等于把那条
	// 判据挪出测试范围了。
	unread, err := onelinksdk.NewUnreadHandler(onelinksdk.UnreadConfig{
		Client: client,
		State: func(ctx context.Context, platformUserID int64) (onelinksdk.UserState, error) {
			n, err := CountUnreadByPlatformUser(ctx, db, platformUserID)
			if err != nil {
				return onelinksdk.UserState{}, err
			}
			return onelinksdk.UserState{Unread: n}, nil
		},
	})
	if err != nil {
		t.Fatalf("装配未读数端点: %v", err)
	}

	// 通知投递器只建不 Start：入队不阻塞，测试也不该去连外部服务
	notifier := service.NewNotifier(cfg)
	profiles := onelink.NewProfiles(db)
	h := New(db, cfg, guard, profiles, notifier, permPull, unread)


	engine := gin.New()
	engine.Use(gin.Recovery())
	// 与 main.go 一致：不配受信代理就不采信 X-Forwarded-For
	if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		t.Fatalf("设置受信代理: %v", err)
	}
	h.RegisterRoutes(engine)

	t.Cleanup(func() { h.Close() })

	return &testEnv{
		t: t, cfg: cfg, db: db, guard: guard, store: store, profiles: profiles,
		h: h, router: engine, perms: map[uint][]string{},
	}
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
	// 会话走 cookie 而不是 Authorization 头。这是接入 OneLink 之后唯一的变化:
	// 凭据从"前端自己拿着的一段串"变成了"浏览器自动带上的一张 HttpOnly cookie",
	// 而后者 JS 读不到 —— 这正是把令牌从 localStorage 里拿出来的目的。
	//
	// 参数名仍然叫 token, 因为对调用方而言它就是一个不透明的会话句柄: 换掉参数名会让
	// 每一个用例都改一遍, 而那些改动不携带任何新信息。
	if token != "" {
		req.AddCookie(&http.Cookie{Name: onelinksdk.DefaultCookieName, Value: token})
	}
	return e.do(req)
}

// do 直接发一个手工构造的请求。
//
// 给"身份能不能被伪造"那一类用例用: 它们要发的请求不满足 request 的形状(比如只有
// Authorization 头、或者一个自定义的身份头), 而那正是被断言的对象。
func (e *testEnv) do(req *http.Request) call {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return call{Status: w.Code, Header: w.Header(), Body: w.Body.Bytes()}
}

// newHeaderRequest 造一个只带某个自定义头的请求。
func newHeaderRequest(e *testEnv, method, path, header, value string) *http.Request {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.Header.Set(header, value)
	return req
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

// ---- 权限档位 ----
//
// 它们模拟的是"平台上给这个人的角色勾了哪些权限点"。接入前这里对应的是 model.RoleUser /
// model.RoleAdmin 两个字符串, 而现在应用侧连角色字段都没有了 —— 判据全是权限点。
var (
	// permsAdmin 相当于接入前的 admin: 全部权限点。
	//
	// 同时含 M 型(菜单)与 B 型(接口)两组码, 与平台上的实际授权一致 —— 平台的角色授权
	// 是"在树上勾选", 勾一个人不会只勾到一半。后端只读 B 型那组, 但夹具照实给全,
	// 免得将来有人把某个 B 码换成 M 码时, 用例仍然绿。
	permsAdmin = []string{
		PermMenuDashboard, PermMenuTask, PermMenuTrash, PermMenuUser,
		PermDashboardView, PermTaskList, PermTaskListAll, PermTaskCreate, PermTaskUpdate,
		PermTaskDelete, PermTaskRestore, PermTaskShare, PermReminderMgr,
		PermUserView, PermUserManage,
	}
	// permsMember 相当于接入前的 user: 能干活, 但只看得到与自己相关的任务。
	//
	// 里面**有** PermTaskDelete 与 PermTaskRestore, 与接入前一致: 那时普通成员也能删
	// 自己建的任务、也能在自己的回收站里恢复。少了它们会把一批"本来就该通过"的用例
	// 变成 403, 而那种失败看起来像权限判错了。
	//
	// 里面**有** PermUserView: 指派任务要选人, 而人员列表就是那个下拉的数据源。
	permsMember = []string{
		PermMenuDashboard, PermMenuTask, PermMenuTrash, PermMenuUser,
		PermDashboardView, PermTaskList, PermTaskCreate, PermTaskUpdate,
		PermTaskDelete, PermTaskRestore, PermTaskShare, PermReminderMgr,
		PermUserView,
	}
)

// ---- 数据准备 ----

func (e *testEnv) addUser(username, email string) *model.User {
	e.t.Helper()
	u := model.NewUser(username, email, username+"姓名")
	// OnelinkUID 必须有值: 它是"这个本地档案对应平台上的谁"的唯一映射, 也是守卫在每次
	// 请求上认人的依据。留空会让这个用户永远登不进来, 而现象是"登录态无故失效"。
	uid := e.nextOnelinkUID()
	u.OnelinkUID = &uid
	if err := e.db.Create(u).Error; err != nil {
		e.t.Fatalf("创建用户 %s: %v", username, err)
	}
	return u
}

// addAdmin 建一个持全部权限点的用户。
func (e *testEnv) addAdmin(username string) *model.User {
	e.t.Helper()
	return e.addUserWithPerms(username, permsAdmin...)
}

// addMember 建一个持成员权限档位的用户。
func (e *testEnv) addMember(username string) *model.User {
	e.t.Helper()
	return e.addUserWithPerms(username, permsMember...)
}

// addUserWithPerms 建一个用户并记下"平台上给他勾了哪些权限点"。
func (e *testEnv) addUserWithPerms(username string, perms ...string) *model.User {
	e.t.Helper()
	u := e.addUser(username, username+"@test.local")
	e.perms[u.ID] = append([]string(nil), perms...)
	return u
}

var onelinkUIDSeq atomic.Int64

// nextOnelinkUID 造一个不会撞的平台用户主键。
//
// 从 1000 起是为了与本地 id 拉开: 两者相等时, 一个"拿 onelink_uid 当本地 id 用"的
// 错误实现会**恰好**通过 —— 而那个错误会让所有业务外键指向另一张表里的行。
func (e *testEnv) nextOnelinkUID() int64 { return 1000 + onelinkUIDSeq.Add(1) }

// login 为某个用户造一条应用会话, 返回它的句柄(cookie 值)。
//
// 参数保留 (username, password) 的形状, 而 password **被忽略** —— 这是有意的:
// 口令现在只存在于平台上, 应用侧连校验它的能力都没有(库里没有哈希)。
// 留着这个参数是因为"给谁发一条会话"这件事本身没变, 改签名会让每一个用例都动一遍。
//
// 会话怎么来的: 原来是走 /api/v1/auth/login 拿自签 JWT; 现在是直接写进 SDK 的 Store。
// 走真实兑换(签票据 → 换令牌)需要一个假平台, 而那会把 SDK 的协议细节(RS256、JWKS、
// 票据一次性)搬进业务测试 —— 那些细节已经由 SDK 自己的用例覆盖。这里跳过的是
// "会话最初怎么来的", 守卫之后走的路径(读 Store → 判到期 → 建 Principal → 挂权限)
// 与生产完全一致。
func (e *testEnv) login(username, _ string) string {
	e.t.Helper()
	var u model.User
	if err := e.db.Where("username = ?", username).First(&u).Error; err != nil {
		e.t.Fatalf("登录前先建用户 %s: %v", username, err)
	}
	return e.sessionFor(&u, e.perms[u.ID]...)
}

// sessionFor 为指定用户造会话, 权限点由调用方显式给出。
func (e *testEnv) sessionFor(u *model.User, perms ...string) string {
	e.t.Helper()
	if u.OnelinkUID == nil {
		e.t.Fatalf("用户 %s 没有 onelink_uid, 无法建会话", u.Username)
	}
	localID := fmt.Sprintf("testsession-%d-%d", u.ID, onelinkUIDSeq.Add(1))
	now := time.Now()
	st := &onelinksdk.SessionState{
		LocalID: localID,
		Identity: onelinksdk.Identity{
			SessionID:   fmt.Sprintf("platform-sid-%d", u.ID),
			UserID:      *u.OnelinkUID,
			Username:    u.Username,
			RealName:    u.FullName,
			Permissions: append([]string(nil), perms...),
		},
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		// 访问令牌必须留足寿命: 守卫在距到期不足 RenewWindow(默认 2 分钟)时会去平台续期,
		// 而这里没有平台 —— 那会变成一次连接失败, 表现为"所有用例随机 401"。
		AccessExpiresAt:  now.Add(time.Hour),
		RefreshExpiresAt: now.Add(24 * time.Hour),
		CreatedAt:        now,
		LastAliveCheck:   now,
	}
	if err := e.store.Put(context.Background(), st); err != nil {
		e.t.Fatalf("写入测试会话: %v", err)
	}
	return localID
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
