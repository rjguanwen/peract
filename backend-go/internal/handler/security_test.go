package handler

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
)

// 本文件盯的是安全语义。接入 OneLink 之后, 其中一大半(限流、去枚举、安全答案哈希、
// 重置令牌一次性、口令版本号作废旧会话)随着"应用自己管口令"这件事一起搬到了平台上 ——
// 在应用侧再测一遍它们没有意义, 因为应用侧已经没有那些代码路径了。
//
// 留下来与新加的是**接入之后仍然由应用自己承担**的那部分: 身份不能被伪造、请求体不能
// 提权、票据之外不能凭空建会话、权限点缺了就是真的不能用。

// 身份只能来自会话 cookie。
//
// 这一条在接入前同样存在(那时是 Authorization 里的自签 JWT), 但**判据变了**: 现在是
// "守卫读 Store", 而 Store 的键是 cookie 值。于是"随便塞一个头就能换人"这类错误会从
// 完全不同的地方冒出来 —— 比如某个 handler 图省事从请求头取 onelink uid。
//
// 三个方向都判: 伪造身份头无效、旧式 Bearer 令牌无效、合法 cookie 有效。
func TestIdentityCannotBeForgedByHeaders(t *testing.T) {
	e := newTestEnv(t)
	victim := e.addAdmin("root")
	token := e.login("root", "pass1234")

	// 1) 伪造身份头。中间件不读它们, 所以这条请求应当是**未登录**, 而不是"以 victim 身份进来"。
	for _, h := range []string{"X-User-Id", "X-Onelink-Uid", "X-Remote-User", "X-Auth-User"} {
		req := newHeaderRequest(e, http.MethodGet, "/api/v1/auth/me", h, u2s(victim.ID))
		if got := e.do(req); got.Status != http.StatusUnauthorized {
			t.Fatalf("%s 头被当成了身份(状态 %d)：%s", h, got.Status, got.Body)
		}
	}

	// 2) 旧式 Bearer 令牌。自签 JWT 那条链路已经删掉, 因此一个长得像令牌的串
	//    既不能进 Authorization, 也不能当 cookie 值。
	req := newHeaderRequest(e, http.MethodGet, "/api/v1/auth/me", "Authorization", "Bearer "+token)
	if got := e.do(req); got.Status != http.StatusUnauthorized {
		t.Fatalf("Authorization 头仍能认人(状态 %d)：%s", got.Status, got.Body)
	}

	// 3) 合法 cookie 有效 —— 否则上面两条"拒绝"可能只是因为整条链路坏了。
	e.get("/api/v1/auth/me", token).Require(t, http.StatusOK)
}

// 请求体里塞提权字段必须无效。
//
// 判据是"改完之后库里那一列没动", 而不是"接口回了 200" —— 后者是设计(未识别的字段被
// 忽略), 而前者才是安全属性。三个字段各代表一类越界: 身份映射(onelink_uid)、角色(role)、
// 权限快照(permissions)。前两个是本地列, 第三个**根本没有本地列** —— 它是会话快照,
// 所以一个"能通过请求体给自己加权限"的实现必须先把快照落到库里, 而那正是要防的。
func TestProfilePatchIgnoresPrivilegedFields(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	token := e.login("member", "pass1234")

	e.patch("/api/v1/users/"+u2s(member.ID), map[string]any{
		"full_name":   "改个名",
		"role":        "admin",
		"permissions": []string{PermTaskListAll},
		"onelink_uid": 999999,
	}, token).Require(t, http.StatusForbidden) // 成员没有 PermUserManage, 连入口都进不去

	// 换一个有权限的人来试: 该改的改了, 不该改的一个都没动
	e.addAdmin("root")
	adminToken := e.adminToken()
	e.patch("/api/v1/users/"+u2s(member.ID), map[string]any{
		"full_name":   "改个名",
		"role":        "admin",
		"permissions": []string{PermTaskListAll},
		"onelink_uid": 999999,
	}, adminToken).Require(t, http.StatusOK)

	var stored model.User
	if err := e.db.First(&stored, member.ID).Error; err != nil {
		t.Fatalf("回读用户: %v", err)
	}
	if stored.FullName != "改个名" {
		t.Fatalf("该改的字段没改: full_name = %q", stored.FullName)
	}
	if stored.OnelinkUID == nil || *stored.OnelinkUID == 999999 {
		t.Fatalf("onelink_uid 被请求体改掉了: %v", stored.OnelinkUID)
	}
	// 权限快照只活在会话里, 库里根本没有它的位置 —— 这条断言钉的是"以后也不要加进去"
	var cols []struct {
		Name string
	}
	if err := e.db.Raw("PRAGMA table_info(users)").Scan(&cols).Error; err != nil {
		t.Fatalf("读表结构: %v", err)
	}
	for _, c := range cols {
		switch strings.ToLower(c.Name) {
		case "role", "permissions", "super_admin":
			t.Fatalf("users 表里出现了权限列 %q：权限只能来自平台的角色授权, "+
				"落到本地列上就等于给应用侧造了第二个真值源", c.Name)
		}
	}
}

// 没有票据就没有会话。
//
// /sso/landing 挂在守卫之外(它正是"还没有会话"时唯一要能走到的入口), 所以它是唯一一个
// **未经认证就会被执行**的端点。它的契约是"只认票据": 没有票据时回未登录, 绝不能顺手
// 造一条会话 —— 那等于任何人访问一次就自动登录成了某个人。
func TestLandingWithoutTicketDoesNotCreateSession(t *testing.T) {
	e := newTestEnv(t)

	got := e.get("/sso/landing", "")
	if got.Status == http.StatusOK {
		t.Fatalf("/sso/landing 在无票据时回了 200：%s", got.Body)
	}
	if got.Status != http.StatusUnauthorized {
		t.Fatalf("/sso/landing 无票据时应回未登录(401), 实际 %d：%s", got.Status, got.Body)
	}
	var n int64
	if err := e.db.Table("onelink_sessions").Count(&n).Error; err != nil {
		t.Fatalf("统计会话行: %v", err)
	}
	if n != 0 {
		t.Fatalf("一次无票据的落地页访问建出了 %d 条会话", n)
	}
	// 也不能下发会话 cookie
	if sc := got.Header.Get("Set-Cookie"); sc != "" {
		t.Fatalf("无票据的落地页下发了 cookie：%q", sc)
	}
}

// 权限点缺了就是真的不能用 —— 而不是"界面不摆, 接口还通"。
//
// 这是整个接入里最容易退化成假象的一环: 前端的入口靠同一份快照隐藏, 所以只要服务端
// 漏挂一个 RequirePerm, 现象就是"看不出任何异常"(按钮本来就没摆出来), 而任何知道
// 路径的人都能直接调。
func TestMissingPermissionIsActuallyDenied(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("member")

	// 只给"查看任务"这一个码
	narrow := e.sessionFor(member, PermTaskList)

	cases := []struct {
		name, method, path string
		perm               string
	}{
		{"新建任务", http.MethodPost, "/api/v1/tasks", PermTaskCreate},
		{"查看人员", http.MethodGet, "/api/v1/users", PermUserView},
		{"回收站", http.MethodGet, "/api/v1/tasks/deleted", PermTaskRestore},
		{"仪表盘", http.MethodGet, "/api/v1/stats/overview", PermDashboardView},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res call
			switch tc.method {
			case http.MethodPost:
				res = e.post(tc.path, map[string]any{"title": "不该建得出来"}, narrow)
			default:
				res = e.get(tc.path, narrow)
			}
			if res.Status != http.StatusForbidden {
				t.Fatalf("%s 在缺少 %s 时应回 403, 实际 %d：%s", tc.name, tc.perm, res.Status, res.Body)
			}
			// 403 而不是 401: 401 会让前端把人送回门户重新登录, 而重登一万次也还是
			// 没有这个权限 —— 那会把一个"找管理员授权"的问题变成一次登录循环。
			if res.Status == http.StatusUnauthorized {
				t.Fatalf("%s 回成了 401, 前端会陷入跳转循环", tc.name)
			}
		})
	}
}

// 停用自己会把人锁在门外: 本地 is_active 一关, 他在所有需要指派的地方消失,
// 而唯一能改回来的入口正是这里。所以它必须被拒, 且拒得早(在写库之前)。
func TestCannotDeactivateSelf(t *testing.T) {
	e := newTestEnv(t)
	admin := e.addAdmin("root")
	token := e.sessionFor(admin, permsAdmin...)

	e.patch("/api/v1/users/"+u2s(admin.ID), map[string]any{"is_active": false}, token).
		Require(t, http.StatusForbidden)

	var stored model.User
	if err := e.db.First(&stored, admin.ID).Error; err != nil {
		t.Fatalf("回读用户: %v", err)
	}
	if !stored.IsActive {
		t.Fatal("停用自己的请求被拒了, 但库里已经改成停用")
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
