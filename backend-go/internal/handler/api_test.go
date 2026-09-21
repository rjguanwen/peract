package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"taskbackend/internal/model"
)

// 本文件盯住「前后端契约」相关的行为：
// 未读数包装、批量已读、PATCH 清空标志、回收站语义、会话失效。
// 这些点出错时接口仍然是 200，只有断言能挡住。

// 会话能认人、能带出权限快照, 且不泄露任何本地敏感列。
//
// 接入 OneLink 之后这里不再断言 role: 角色的载体是平台侧的角色授权, 应用侧给的是一份
// 权限码快照 —— 而快照的内容正是前端用来决定摆哪些入口的依据, 所以它必须真的在。
func TestSessionResolvesIdentityAndPermissions(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")

	token := e.login("root", "pass1234")

	me := e.get("/api/v1/auth/me", token).Require(t, http.StatusOK).Map(t)
	if got := Str(t, me, "username"); got != "root" {
		t.Fatalf("会话对应的身份不对，got %q", got)
	}
	perms, ok := me["permissions"].([]any)
	if !ok || len(perms) == 0 {
		t.Fatalf("权限快照缺失或为空：%v", me["permissions"])
	}
	// 快照里必须有那个决定数据范围的码: 少了它, 前端不会摆"全部任务"这个入口,
	// 而服务端仍然允许 —— 界面与权限两边对不上时, 用户会以为自己没权限。
	found := false
	for _, one := range perms {
		if one == PermTaskListAll {
			found = true
		}
	}
	if !found {
		t.Fatalf("权限快照里没有 %s：%v", PermTaskListAll, perms)
	}
	// 响应绝不能带出本地档案里那几列认证遗留字段
	for _, leak := range []string{"hashed_password", "security_answer", "password", "role"} {
		if _, ok := me[leak]; ok {
			t.Fatalf("用户响应泄露了 %q：%v", leak, me)
		}
	}
}

// 会话不存在、伪造、或已过期时一律 401 —— 而不是 500 或"匿名放行"。
//
// 这三种在接入前由 JWT 的签名与 exp 保证, 现在由守卫查 Store 保证。判据变了,
// 但**失败必须是同一个 401** 这件事没变: 前端只认 401 才会把人送回门户。
func TestGuardRejectsUnknownAndExpiredSessions(t *testing.T) {
	e := newTestEnv(t)
	root := e.addAdmin("root")

	// 伪造的 cookie 值: 形状合法(NonceOK 允许的字符集与长度), 但 Store 里没有
	e.get("/api/v1/auth/me", "forged-session-value").Require(t, http.StatusUnauthorized)

	// 过期会话: 直接把访问令牌的到期时刻推到过去。守卫的 admit 会把它判成死会话,
	// 而不是去平台续期(这里没有平台)。
	token := e.login("root", "pass1234")
	if err := e.db.Model(&struct{}{}).Table("onelink_sessions").
		Where("local_id = ?", token).
		Update("access_expires", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("把会话改成过期: %v", err)
	}
	e.get("/api/v1/auth/me", token).Require(t, http.StatusUnauthorized)

	// 干净的一条会话仍然可用 —— 否则上面两条"拒绝"可能只是因为整条链路坏了
	e.get("/api/v1/auth/me", e.sessionFor(root, permsAdmin...)).Require(t, http.StatusOK)
}

// 登出必须真的把会话从库里删掉。会话是有状态的, 所以"删了"与"没删"是可观测的 ——
// 而它正是接入 OneLink 相对自签 JWT 最实质的收益: 登出不再需要一张黑名单去补。
func TestLogoutDeletesSession(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")

	token := e.login("root", "pass1234")
	e.get("/api/v1/auth/me", token).Require(t, http.StatusOK)

	// 登出回 302 而不是 200: 它是给浏览器点的, 成功后要回到站内首页(SDK 的 Logout)。
	// 断言状态码而不是"只看副作用", 是因为 302 与 401 在这里长得不一样 —— 后者说明
	// 这条路由被挪到了守卫之后, 而那样一来未登录的人连登出都做不到。
	bye := e.post("/logout", nil, token).Require(t, http.StatusFound)
	// cookie 必须被清掉: 留着一条指向已删会话的 cookie, 表现是"登出后刷新页面又像登录了"
	// (请求带上死 cookie, 守卫回 401, 前端跳门户 —— 用户看到的是被弹了两次)。
	if sc := bye.Header.Get("Set-Cookie"); !strings.Contains(sc, "Max-Age=0") && !strings.Contains(sc, "Max-Age=-1") {
		t.Fatalf("登出未清除会话 cookie：%q", sc)
	}

	if got := e.get("/api/v1/auth/me", token).Require(t, http.StatusUnauthorized); got.Detail(t) == "" {
		t.Fatal("登出后旧会话仍可用")
	}
	// 库里那一行必须没了, 而不是只被标了某个"已登出"位 —— 前者不会留下令牌原文
	var n int64
	if err := e.db.Table("onelink_sessions").Where("local_id = ?", token).Count(&n).Error; err != nil {
		t.Fatalf("统计会话行: %v", err)
	}
	if n != 0 {
		t.Fatalf("登出后会话行仍在库里(%d 行)", n)
	}

	// 登出后重登拿到的是另一条会话, 且能用
	again := e.login("root", "pass1234")
	if again == token {
		t.Fatal("重登拿到了与已删除会话相同的句柄")
	}
	e.get("/api/v1/auth/me", again).Require(t, http.StatusOK)
}

// 功能权限按权限点判, 不按"有没有登录"判。
//
// 接入前这里靠一个 role 字符串; 现在每个入口在路由上各自挂一个码, 于是"能看人员列表"
// 与"能改别人档案"是两件不同的事 —— 而前者成员需要(指派任务要选人), 后者不需要。
func TestMemberPermissionBoundaries(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("member")
	memberToken := e.login("member", "pass1234")

	e.get("/api/v1/tasks", "").Require(t, http.StatusUnauthorized)

	// 成员能看人员列表(任务表单的负责人下拉靠它)
	e.get("/api/v1/users", memberToken).Require(t, http.StatusOK)
	// 但不能改别人的档案
	e.patch("/api/v1/users/"+u2s(member.ID), map[string]any{"full_name": "自己改的"}, memberToken).
		Require(t, http.StatusForbidden)
	// 也不能要求"看全部任务"
	e.get("/api/v1/tasks?visibility=all", memberToken).Require(t, http.StatusForbidden)

	// 权限点是从会话快照里取的, 所以换一条"没有这个码"的会话就该被拒 ——
	// 这一条同时证明判据真的落在权限点上, 而不是某个写死的默认值。
	e.perms[member.ID] = []string{PermTaskList}
	narrow := e.sessionFor(member, PermTaskList)
	e.get("/api/v1/tasks", narrow).Require(t, http.StatusOK)
	e.post("/api/v1/tasks", map[string]any{"title": "不该建得出来"}, narrow).
		Require(t, http.StatusForbidden)
}

func TestTaskClearFlagsActuallyNullColumns(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("helper")
	token := e.adminToken()

	due := time.Now().Add(48 * time.Hour).Format("2006-01-02T15:04:05")
	task := e.createTask(token, map[string]any{
		"title":       "要清空字段的任务",
		"assignee_id": member.ID,
		"due_date":    due,
	})
	if task.AssigneeID == nil || *task.AssigneeID != member.ID {
		t.Fatalf("前置条件不成立，负责人没设上：%+v", task)
	}
	if task.DueDate == nil {
		t.Fatal("前置条件不成立，截止时间没设上")
	}

	// PATCH 语义：字段缺省即「不改」，清空必须走显式标志
	untouched := e.patch("/api/v1/tasks/"+u2s(task.ID), map[string]any{"progress": 30}, token).
		Require(t, http.StatusOK)
	var after taskJSON
	untouched.Into(t, &after)
	if after.AssigneeID == nil || after.DueDate == nil {
		t.Fatalf("只改进度却把负责人/截止时间一起清了，PATCH 语义被破坏：%+v", after)
	}

	cleared := e.patch("/api/v1/tasks/"+u2s(task.ID), map[string]any{
		"clear_assignee": true,
		"clear_due_date": true,
	}, token).Require(t, http.StatusOK)
	var got taskJSON
	cleared.Into(t, &got)
	if got.AssigneeID != nil {
		t.Fatalf("clear_assignee 未生效，assignee_id = %v", *got.AssigneeID)
	}
	if got.DueDate != nil {
		t.Fatalf("clear_due_date 未生效，due_date = %q", *got.DueDate)
	}
	// 落库而非只在响应里抹掉
	if reloaded := e.fetchTask(token, task.ID); reloaded.AssigneeID != nil || reloaded.DueDate != nil {
		t.Fatalf("清空未持久化：%+v", reloaded)
	}

	// 后续写回（进展、状态流转）不得把已清的负责人再“带”回来：
	// 那几次 Save 读的是同一批已 Preload 的关联副本
	e.post("/api/v1/tasks/"+u2s(task.ID)+"/progress", map[string]any{"comment": "清完负责人后记一笔"}, token).
		Require(t, http.StatusOK)
	if back := e.fetchTask(token, task.ID); back.AssigneeID != nil || back.DueDate != nil {
		t.Fatalf("添加进展后已清空字段又长回来了：%+v", back)
	}
}

func TestTaskClearFlagConflictRejected(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("helper")
	token := e.adminToken()

	task := e.createTask(token, map[string]any{"title": "冲突参数", "assignee_id": member.ID})

	e.patch("/api/v1/tasks/"+u2s(task.ID), map[string]any{
		"assignee_id": member.ID, "clear_assignee": true,
	}, token).Require(t, http.StatusBadRequest)

	e.patch("/api/v1/tasks/"+u2s(task.ID), map[string]any{
		"due_date": time.Now().Add(time.Hour).Format("2006-01-02T15:04:05"), "clear_due_date": true,
	}, token).Require(t, http.StatusBadRequest)

	// 冲突请求必须原样拒绝，不能一半生效
	if back := e.fetchTask(token, task.ID); back.AssigneeID == nil || *back.AssigneeID != member.ID {
		t.Fatalf("被拒绝的冲突请求改动了数据：%+v", back)
	}
}

func TestTrashListsOnlyDeletedTasks(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.adminToken()

	kept := e.createTask(token, map[string]any{"title": "还在的任务"})
	gone := e.createTask(token, map[string]any{"title": "已删除的任务"})

	e.del("/api/v1/tasks/"+u2s(gone.ID), token).Require(t, http.StatusNoContent)

	trash := e.get("/api/v1/tasks/deleted", token).Require(t, http.StatusOK)
	if ids := taskIDs(t, trash); len(ids) != 1 || ids[0] != gone.ID {
		t.Fatalf("回收站应只含已删除任务，实际 %v（还包含 %d）", ids, kept.ID)
	}

	// 列表页不得再看到被删任务
	live := e.get("/api/v1/tasks", token).Require(t, http.StatusOK)
	if ids := taskIDs(t, live); contains(ids, gone.ID) {
		t.Fatalf("已删除任务仍出现在正常列表中：%v", ids)
	}
}

func TestRestoreTaskBringsItBack(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("helper")
	token := e.adminToken()

	title := "恢复我"
	task := e.createTask(token, map[string]any{"title": title, "assignee_id": member.ID, "priority": model.PriorityHigh})
	e.del("/api/v1/tasks/"+u2s(task.ID), token).Require(t, http.StatusNoContent)

	e.post("/api/v1/tasks/"+u2s(task.ID)+"/restore", nil, token).Require(t, http.StatusOK)

	restored := e.fetchTask(token, task.ID)
	// 恢复只应清掉 deleted_at，不应顺手丢字段
	if restored.Title != title || restored.Priority != model.PriorityHigh {
		t.Fatalf("恢复后字段丢失：%+v", restored)
	}
	if restored.AssigneeID == nil || *restored.AssigneeID != member.ID {
		t.Fatalf("恢复后负责人丢失：%+v", restored)
	}
	if ids := taskIDs(t, e.get("/api/v1/tasks/deleted", token).Require(t, http.StatusOK)); contains(ids, task.ID) {
		t.Fatalf("恢复后任务仍留在回收站：%v", ids)
	}

	// 未删除的任务不该被「恢复」
	e.post("/api/v1/tasks/"+u2s(task.ID)+"/restore", nil, token).Require(t, http.StatusBadRequest)
}

// 任务列表是设计上的共享看板（mine / assignee_id 只是可选筛选），
// 但回收站必须严格按人隔离：代码里写了 creator_id 限定，就要有测试钉住它。
func TestTrashScopeIsPerUser(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	e.addMember("owner")
	e.addMember("other")
	ownerToken := e.login("owner", "pass1234")
	otherToken := e.login("other", "pass1234")

	task := e.createTask(ownerToken, map[string]any{"title": "私有任务"})
	e.del("/api/v1/tasks/"+u2s(task.ID), ownerToken).Require(t, http.StatusNoContent)

	if ids := taskIDs(t, e.get("/api/v1/tasks/deleted", otherToken).Require(t, http.StatusOK)); contains(ids, task.ID) {
		t.Fatalf("无关成员看到了别人的回收站：%v", ids)
	}
	ids := taskIDs(t, e.get("/api/v1/tasks/deleted", ownerToken).Require(t, http.StatusOK))
	if !contains(ids, task.ID) {
		t.Fatalf("创建者的回收站里应能找到刚删的任务：%v", ids)
	}
	e.post("/api/v1/tasks/"+u2s(task.ID)+"/restore", nil, otherToken).Require(t, http.StatusForbidden)

	// 管理员看全部
	if ids := taskIDs(t, e.get("/api/v1/tasks/deleted", e.adminToken()).Require(t, http.StatusOK)); !contains(ids, task.ID) {
		t.Fatalf("管理员回收站应含全部已删任务：%v", ids)
	}
}

func TestReminderUnreadCountShapeAndReadAll(t *testing.T) {
	e := newTestEnv(t)
	member := e.addMember("member")
	token := e.login("member", "pass1234")
	task := e.createTask(token, map[string]any{"title": "有提醒的任务"})

	now := time.Now()
	e.addReminder(task.ID, member.ID, now.Add(-time.Minute), true, false)  // 已推送未读
	e.addReminder(task.ID, member.ID, now.Add(-time.Hour), false, false)   // 已到点未读
	e.addReminder(task.ID, member.ID, now.Add(24*time.Hour), false, false) // 未到点，不计入未读

	// 前端 ReminderBell 读的是 data.count，裸数字会让角标恒为 0
	count := e.get("/api/v1/reminders/unread-count", token).Require(t, http.StatusOK).Map(t)
	if got := Num(t, count, "count"); got != 2 {
		t.Fatalf("未读数期望 2，实际 %v（响应 %v）", got, count)
	}

	list := e.get("/api/v1/reminders?page_size=50", token).Require(t, http.StatusOK).Map(t)
	items := list["items"]
	if arr, ok := items.([]any); !ok || len(arr) != 3 {
		t.Fatalf("提醒列表期望 3 条，实际 %v", items)
	}

	// 一次请求清掉全部未读，替代前端逐条 Promise.all
	updated := e.post("/api/v1/reminders/read-all", nil, token).Require(t, http.StatusOK).Map(t)
	if got := Num(t, updated, "updated"); got != 3 {
		t.Fatalf("read-all 期望更新 3 条（含未到点），实际 %v", got)
	}
	after := e.get("/api/v1/reminders/unread-count", token).Require(t, http.StatusOK).Map(t)
	if got := Num(t, after, "count"); got != 0 {
		t.Fatalf("read-all 后未读数应为 0，实际 %v", got)
	}

	// 未读数按人隔离
	e.addMember("other")
	otherToken := e.login("other", "pass1234")
	if got := Num(t, e.get("/api/v1/reminders/unread-count", otherToken).Require(t, http.StatusOK).Map(t), "count"); got != 0 {
		t.Fatalf("新用户不该继承别人的未读数，got %v", got)
	}
	if e.get("/api/v1/reminders/unread-count", "").Require(t, http.StatusUnauthorized).Status != 401 {
		t.Fatal("未读数接口应当要求登录")
	}
}
