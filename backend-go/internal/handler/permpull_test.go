package handler

// 权限清单端点的用例。
//
// 它是躬行**唯一一条入站调用**(平台 -> 躬行), 也是唯一一条挂在登录守卫之外的业务路径,
// 所以这一组用例问的问题与其它测试不同: 别的测试问"这个人有没有这个权限", 这里问
// "来的到底是不是平台"。
//
// 请求一律用 SDK 的 SignPull 造 —— 即"站在平台那一侧"。那个方向没有第二个实现:
// 平台侧算得对不对由 SDK 的向量用例保证, 这里保证的是"守卫真的在用它判"。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	onelinksdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/config"
	"taskbackend/internal/perms"
)

// pullNonceSeq 用例内的自增计数。**不能用时间戳**: Windows 上 time.Now 的粒度足以让
// 相邻两次调用拿到同一个纳秒值, 于是用例之间会互相判成重放, 而失败信息看起来像守卫坏了。
var pullNonceSeq int64

// signedPull 造一次"平台侧"的拉取请求。secret 传别的值就是"拿着错误的密钥来"。
func signedPull(e *testEnv, secret string) *http.Request {
	e.t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("test-nonce-%012d", atomic.AddInt64(&pullNonceSeq, 1))
	uri := e.cfg.OnelinkPermPullPath
	req := httptest.NewRequest(http.MethodGet, uri, strings.NewReader(""))
	req.Header.Set(onelinksdk.HeaderApp, e.cfg.OnelinkAppCode)
	req.Header.Set(onelinksdk.HeaderTimestamp, ts)
	req.Header.Set(onelinksdk.HeaderNonce, nonce)
	req.Header.Set(onelinksdk.HeaderSign,
		onelinksdk.SignPull(secret, e.cfg.OnelinkAppCode, ts, nonce, http.MethodGet, uri, nil))
	return req
}

// TestPermManifestEndpointServesWithoutSession 平台不带任何会话也要能取到清单。
//
// 这条断言是整组的核心, 因为它同时钉住两件相反的事:
//
//	把端点挂进 RequireLogin -> 平台拿到 401, 而平台侧把它读成"应用拒绝了这次拉取"
//	  (60020), 于是排查方向被带到"两侧密钥不一致"上去, 而真正的原因是路由挂错了。
//	端点忘了包 SDK 的 handler -> 谁都能取到这份清单(见下一条用例)。
func TestPermManifestEndpointServesWithoutSession(t *testing.T) {
	e := newTestEnv(t)

	res := e.do(signedPull(e, e.cfg.OnelinkAppSecret))
	res.Require(t, http.StatusOK)

	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, 期望 application/json", ct)
	}
	// 清单不该被链路上任何一层缓存住: 缓存会让平台下一次拉取拿到旧清单, 而现场表现是
	// "我明明加了权限点却没生效"。
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, 期望 no-store", cc)
	}

	// 响应体必须就是那份清单本身 —— 而且必须是**与上报共用的同一份**
	// (perms.Manifest), 不是这个端点另抄的一份。
	var got []onelinksdk.PermDecl
	if err := json.Unmarshal(res.Body, &got); err != nil {
		t.Fatalf("响应不是清单数组: %v\n%s", err, res.Body)
	}
	want, err := json.Marshal(perms.Manifest)
	if err != nil {
		t.Fatalf("序列化 perms.Manifest: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(res.Body), want) {
		t.Errorf("端点返回的清单与 perms.Manifest 不一致:\n  端点: %s\n  代码: %s", res.Body, want)
	}
}

// TestPermManifestEndpointRejectsEveryoneElse 未通过校验的调用方一条都拿不到。
//
// 断言里带 "onelink:" 是刻意的: 那说明拒绝发生在**端点内部**(签名校验), 而不是被登录
// 守卫拦下的 —— 两者都是 401, 但后者意味着路由挂错了位置, 而那种错在平台侧看起来与
// "密钥不一致"一模一样。
func TestPermManifestEndpointRejectsEveryoneElse(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	// 一个**合法**会话: 下面要用它证明"有会话也进不来"。
	session := e.login("root", "pass1234")

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
			return httptest.NewRequest(http.MethodGet, e.cfg.OnelinkPermPullPath, strings.NewReader(""))
		}, true},
		{"密钥不对", func() *http.Request { return signedPull(e, "wrong-secret") }, true},
		{"带会话 cookie 但没有签名", func() *http.Request {
			// 有人拿着一个合法会话去戳这个端点: 它照样必须被拒 —— 这个端点的判据是
			// "来的进程是不是平台", 而一个浏览器会话说明不了那件事。
			req := httptest.NewRequest(http.MethodGet, e.cfg.OnelinkPermPullPath, strings.NewReader(""))
			req.AddCookie(&http.Cookie{Name: onelinksdk.DefaultCookieName, Value: session})
			return req
		}, true},
		{"POST 而不是 GET", func() *http.Request {
			return httptest.NewRequest(http.MethodPost, e.cfg.OnelinkPermPullPath, strings.NewReader(""))
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := e.do(tc.req())
			if res.Status == http.StatusOK {
				t.Fatalf("拿到了 200 与 %d 字节清单", len(res.Body))
			}
			if strings.Contains(string(res.Body), "task-system:task:list") {
				t.Fatal("被拒的响应里带出了清单内容")
			}
			if tc.fromEndpoint && !strings.Contains(string(res.Body), "onelink:") {
				t.Fatalf("这次拒绝不是端点内部给的(像是被登录守卫拦下的): %s", res.Body)
			}
		})
	}
}

// TestPermManifestEndpointAbsentWithoutOnelink 没配 OneLink 时这条路由根本不存在。
//
// 那与"存在但永远 401"是两回事, 而平台侧看到的都是"拉不到": 前者说明这个部署还没接平台,
// 后者说明密钥不对。留着一条永远拒绝的路由, 只会让第二种解释变得合理。
//
// 这个用例不走 newTestEnv: 那个夹具会无条件构造 OneLink 客户端(缺 BaseURL 时直接失败),
// 而这里要的正是"没有客户端、也没有守卫"的那套装配。
func TestPermManifestEndpointAbsentWithoutOnelink(t *testing.T) {
	cfg := &config.Config{
		ProjectName:         "躬行测试",
		UploadDir:           t.TempDir(),
		OnelinkPermPullPath: "/onelink/perm-manifest",
		OnelinkUnreadPath:   "/onelink/unread",
		MaxBodyBytes:        1 << 20,
	}
	h := New(nil, cfg, nil, nil, nil, nil, nil)
	engine := gin.New()
	h.RegisterRoutes(engine)

	// 两条入站端点都要在这里 404。漏掉任何一条的表现都是平台侧某一格永远取不到, 而
	// "这个路径不存在"与"存在但永远 401"是两回事: 前者说明这个部署还没接平台, 后者
	// 说明密钥不对。留着一条永远拒绝的路由, 只会让第二种解释变得合理。
	for _, path := range []string{cfg.OnelinkPermPullPath, cfg.OnelinkUnreadPath} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, strings.NewReader("")))
		if w.Code != http.StatusNotFound {
			t.Errorf("没配 OneLink 时 %s 应 404, 实际 %d", path, w.Code)
		}
	}
}
