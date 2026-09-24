package handler

// 用户态(未读数)端点的用例。
//
// 它与 permpull_test.go 同族: 平台的第二条入站调用, 同样挂在登录守卫之外, 同样只认平台
// 签名。但它多一条别的测试都没有的判据 —— **口径必须与躬行自己的未读数接口同值**。
//
// 为什么那条最要紧: 门户上那张卡片的角标, 与用户点进躬行后看到的未读数, 如果各自算各自的,
// 会出现"角标说 3, 点进去一条未读也没有" —— 而两个数字各自都算得对。那是最难解释的一类
// 不一致, 因为没有任何一处会报错。所以这里不另造一份计数逻辑, 而是拿两个接口对账。
//
// 请求一律用 SDK 的 SignUser 造(即"站在平台那一侧"): 那个方向没有第二个实现, 平台侧算得
// 对不对由 SDK 的向量用例保证, 这里保证的是"守卫真的在用它判"。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"
)

// unreadNonceSeq 用例内的自增计数, 理由同 permpull_test.go 的 pullNonceSeq:
// Windows 上 time.Now 的粒度足以让相邻两次调用拿到同一个纳秒值, 于是用例之间会互相判成
// 重放, 而失败信息看起来像守卫坏了。
var unreadNonceSeq int64

// signedUnread 造一次"平台侧"的用户态查询。secret 传别的值就是"拿着错误的密钥来"。
func signedUnread(e *testEnv, secret string, platformUserID int64) *http.Request {
	e.t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("unread-nonce-%012d", atomic.AddInt64(&unreadNonceSeq, 1))
	uri := e.cfg.OnelinkUnreadPath + "?userId=" + strconv.FormatInt(platformUserID, 10)
	req := httptest.NewRequest(http.MethodGet, uri, strings.NewReader(""))
	req.Header.Set(onelinksdk.HeaderApp, e.cfg.OnelinkAppCode)
	req.Header.Set(onelinksdk.HeaderTimestamp, ts)
	req.Header.Set(onelinksdk.HeaderNonce, nonce)
	req.Header.Set(onelinksdk.HeaderSign,
		onelinksdk.SignUser(secret, e.cfg.OnelinkAppCode, platformUserID, ts, nonce, http.MethodGet, uri, nil))
	return req
}

// unreadOf 解一次用户态响应。断言 unread 字段**必须存在** —— 一个省略了它的响应会被
// 平台判成"响应不合法"并降级, 而现场表现与"应用没实现"一模一样。
func unreadOf(t *testing.T, res call) int64 {
	t.Helper()
	var state struct {
		Unread *int64 `json:"unread"`
	}
	if err := json.Unmarshal(res.Body, &state); err != nil {
		t.Fatalf("响应不是一个 JSON 对象: %v\n%s", err, res.Body)
	}
	if state.Unread == nil {
		t.Fatalf("响应里没有 unread 字段: %s", res.Body)
	}
	return *state.Unread
}

// TestUnreadEndpointServesWithoutSession 平台不带任何会话也要能拿到未读数。
//
// 与清单端点那条同族, 但这里的失败更安静: 清单拉不到时管理台会报错(60019/60020), 而未读数
// 拿不到时门户上只是一个灰色角标 —— 没有任何一处会报错, 只有一条 Warn 日志。
func TestUnreadEndpointServesWithoutSession(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	token := e.login("member", "pass1234")
	task := e.createTask(token, map[string]any{"title": "有提醒的任务"})

	now := time.Now()
	e.addReminder(task.ID, member.ID, now.Add(-time.Minute), true, false)  // 已推送未读
	e.addReminder(task.ID, member.ID, now.Add(-time.Hour), false, false)   // 已到点未读
	e.addReminder(task.ID, member.ID, now.Add(24*time.Hour), false, false) // 未到点, 不算未读
	e.addReminder(task.ID, member.ID, now.Add(-time.Minute), true, true)   // 已读

	res := e.do(signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID))
	res.Require(t, http.StatusOK)

	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, 期望 application/json", ct)
	}
	// 未读数是"此刻"的值, 被任何一层缓存住都会让用户看到过期的角标。平台侧自己有一层
	// 20 秒缓存, 那是它的决定; 这里不能让别人也缓存一份。
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, 期望 no-store", cc)
	}
	if got := unreadOf(t, res); got != 2 {
		t.Errorf("unread = %d, 期望 2(已推送未读 + 已到点未读)", got)
	}
}

// TestUnreadCountMatchesTheInAppEndpoint 门户角标与躬行自己的未读数接口必须同值。
//
// 这是本文件最值钱的一条。两个数各自算各自的时, 现象是"角标说 3, 点进去一条未读也没有",
// 而两个数字各自都算得对 —— 没有一处会报错, 所以只能靠这条对账拦住。数据里刻意混进三种
// 边界: 未到点的(不算)、已读的(不算)、别人的(不算)。
func TestUnreadCountMatchesTheInAppEndpoint(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	other := e.addMember("other")
	token := e.login("member", "pass1234")
	task := e.createTask(token, map[string]any{"title": "对账用的任务"})

	now := time.Now()
	e.addReminder(task.ID, member.ID, now.Add(-time.Minute), true, false)
	e.addReminder(task.ID, member.ID, now.Add(-time.Hour), false, false)
	e.addReminder(task.ID, member.ID, now.Add(24*time.Hour), false, false) // 未到点
	e.addReminder(task.ID, member.ID, now.Add(-time.Minute), true, true)   // 已读
	e.addReminder(task.ID, other.ID, now.Add(-time.Minute), true, false)   // 别人的

	inApp := Num(t, e.get("/api/v1/reminders/unread-count", token).Require(t, http.StatusOK).Map(t), "count")
	if inApp != 2 {
		t.Fatalf("对账数据不对: 应用内未读数 = %v, 期望 2(先确认这个数, 再谈两边是否一致)", inApp)
	}

	res := e.do(signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID)).Require(t, http.StatusOK)
	if got := unreadOf(t, res); float64(got) != inApp {
		t.Fatalf("门户角标 %d 与应用内未读数 %v 不一致: 两个口径分叉了, "+
			"而现象会是\"角标说 %d, 点进去一条未读也没有\"", got, inApp, got)
	}
}

// TestUnreadEndpointIsPerUser 未读数按人隔离, 而且别人的那个 0 是"真的 0"。
//
// 后半句值得单独说: 这个用例里 other 有本地档案, 所以它拿到的 0 来自"确实没有未读"。
// 一个把"查不到本地档案"也返回 0 的实现能通过这一条, 却会在下一个用例(未知平台用户)
// 上现形 —— 两条合起来才钉住了"0 与取不到是两回事"。
func TestUnreadEndpointIsPerUser(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	other := e.addMember("other")
	token := e.login("member", "pass1234")
	task := e.createTask(token, map[string]any{"title": "任务"})
	e.addReminder(task.ID, member.ID, time.Now().Add(-time.Minute), true, false)

	if got := unreadOf(t, e.do(signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID))); got != 1 {
		t.Errorf("member 的未读数 = %d, 期望 1", got)
	}
	if got := unreadOf(t, e.do(signedUnread(e, e.cfg.OnelinkAppSecret, *other.OnelinkUID))); got != 0 {
		t.Errorf("other 的未读数 = %d, 期望 0(别人的提醒不能串过来)", got)
	}
}

// TestUnreadEndpointUnknownPlatformUser 平台用户在躬行没有本地档案时回 5xx。
//
// 回 200 + 0 会被平台读成"这个人确实没有未读", 于是用户看到一个干净的角标、不去打开躬行;
// 而事实是这个平台账号还没通过 SSO 进来过(他的本地档案还没建)。回 5xx 会让门户那一格显示
// "取不到"并留一条日志 —— 那才是事实。
func TestUnreadEndpointUnknownPlatformUser(t *testing.T) {
	e := newTestEnv(t)
	e.addMember("member") // 存在一个本地档案, 但它的 onelink_uid 不是下面这个值
	res := e.do(signedUnread(e, e.cfg.OnelinkAppSecret, 987654321))
	if res.Status < 500 {
		t.Fatalf("未知平台用户得到 %d, 期望 5xx: 回 200+0 会被平台读成\"确实没有未读\"", res.Status)
	}
}

// TestUnreadEndpointRejectsEveryoneElse 未通过校验的调用方一条都拿不到。
//
// 断言里带 "onelink:" 是刻意的: 那说明拒绝发生在**端点内部**(签名校验), 而不是被登录守卫
// 拦下的 —— 两者都是 401, 但后者意味着路由挂错了位置, 而那种错在平台侧看起来与"密钥不一致"
// 一模一样(它会记成 denied, 排查方向被带到密钥上去)。
func TestUnreadEndpointRejectsEveryoneElse(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	// 一个**合法**会话: 下面要用它证明"有会话也进不来"。
	session := e.login("member", "pass1234")

	cases := []struct {
		name string
		req  func() *http.Request
		// fromEndpoint 这一条请求是否应当**走到端点内部**再被拒。
		//
		// 方法不对那一条不是: 那条路由只注册了 GET, 于是 gin 直接回 404 —— 框架的行为,
		// 不是端点的判据。把它也算进"端点给的拒绝"会得到一句与事实不符的失败信息。
		fromEndpoint bool
	}{
		{"没带任何签名头", func() *http.Request {
			return httptest.NewRequest(http.MethodGet,
				e.cfg.OnelinkUnreadPath+"?userId="+strconv.FormatInt(*member.OnelinkUID, 10),
				strings.NewReader(""))
		}, true},
		{"密钥不对", func() *http.Request { return signedUnread(e, "wrong-secret", *member.OnelinkUID) }, true},
		{"带会话 cookie 但没有签名", func() *http.Request {
			// 有人拿着一个合法会话去戳这个端点: 它照样必须被拒。这一条比清单端点那里
			// 更要紧 —— 这个端点按 userId 回答, 认会话就等于给了任何登录用户一个
			// "查别人有多少未读"的枚举器。
			req := httptest.NewRequest(http.MethodGet,
				e.cfg.OnelinkUnreadPath+"?userId="+strconv.FormatInt(*member.OnelinkUID, 10),
				strings.NewReader(""))
			req.AddCookie(&http.Cookie{Name: onelinksdk.DefaultCookieName, Value: session})
			return req
		}, true},
		{"POST 而不是 GET", func() *http.Request {
			return httptest.NewRequest(http.MethodPost, e.cfg.OnelinkUnreadPath, strings.NewReader(""))
		}, false},
		{"缺 userId", func() *http.Request {
			req := signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID)
			req.URL.RawQuery = ""
			return req
		}, true},
		{"userId 不是数字", func() *http.Request {
			req := signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID)
			req.URL.RawQuery = "userId=abc"
			return req
		}, true},
		{"userId 为 0", func() *http.Request {
			// 0 不是任何人的主键, 而它在应用侧的查询里可能落进"没有用户"那一支 ——
			// 于是回一个 0, 一个静默的假数字。
			req := signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID)
			req.URL.RawQuery = "userId=0"
			return req
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := e.do(tc.req())
			if res.Status == http.StatusOK {
				t.Fatalf("拿到了 200 与 %s", res.Body)
			}
			if strings.Contains(string(res.Body), `"unread"`) {
				t.Fatal("被拒的响应里带出了未读数")
			}
			if tc.fromEndpoint && !strings.Contains(string(res.Body), "onelink:") {
				t.Fatalf("这次拒绝不是端点内部给的(像是被登录守卫拦下的): %s", res.Body)
			}
		})
	}
}

// TestUnreadEndpointRejectsTamperedUserID 端到端证明 userId 真的进了签名串。
//
// 场景就是那条静默越权: 有人拿到一次合法请求(签名是按 member 的平台 ID 算的), 把 query 里的
// 数字换成另一个人的再发一次。它必须被拒 —— 而一个"uri 里已经带了 userId 所以不必单独签"
// 的实现会放它过去, 于是任何人都能读到别人的未读数, 且两侧日志都正常。
//
// 这一条与 SDK 的向量用例互补: 向量证明的是算法, 这里证明的是 ServeHTTP 真的把**请求里那个
// 值**喂进了签名, 且 uri 用的是含 query 的那一份。
func TestUnreadEndpointRejectsTamperedUserID(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	other := e.addMember("other")

	req := signedUnread(e, e.cfg.OnelinkAppSecret, *member.OnelinkUID)
	req.URL.RawQuery = "userId=" + strconv.FormatInt(*other.OnelinkUID, 10)
	res := e.do(req)
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("改过 userId 的请求得到 %d, 期望 401: userId 没有进待签名串(这是一次静默越权)", res.Status)
	}
	// 反方向: 签名按 other 算、query 写 member —— 同样必须被拒。
	req2 := signedUnread(e, e.cfg.OnelinkAppSecret, *other.OnelinkUID)
	req2.URL.RawQuery = "userId=" + strconv.FormatInt(*member.OnelinkUID, 10)
	if res2 := e.do(req2); res2.Status != http.StatusUnauthorized {
		t.Errorf("签名与 query 不一致的请求得到 %d, 期望 401", res2.Status)
	}
}
