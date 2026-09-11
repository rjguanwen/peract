package handler

import (
	"net/http"
	"testing"
	"time"

	"taskbackend/internal/model"
)

// 本文件盯住这一轮修复中「前后端契约」相关的行为：
// 未读数包装、批量已读、PATCH 清空标志、回收站语义、令牌吊销。
// 这些点出错时接口仍然是 200，只有断言能挡住。

func TestLoginIssuesUsableToken(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")

	token := e.login("root", "pass1234")

	me := e.get("/api/v1/auth/me", token).Require(t, http.StatusOK).Map(t)
	if got := Str(t, me, "username"); got != "root" {
		t.Fatalf("登录后的身份不对，got %q", got)
	}
	if got := Str(t, me, "role"); got != model.RoleAdmin {
		t.Fatalf("角色不对，got %q", got)
	}
	// 响应绝不能带出口令哈希或安全答案
	for _, leak := range []string{"hashed_password", "security_answer", "password"} {
		if _, ok := me[leak]; ok {
			t.Fatalf("用户响应泄露了敏感字段 %q：%v", leak, me)
		}
	}
}

func TestLoginRejectsBadCredentialsAndInactiveAccount(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	blocked := e.addMember("blocked")
	if err := e.db.Model(&model.User{}).Where("id = ?", blocked.ID).
		Update("is_active", false).Error; err != nil {
		t.Fatalf("停用账号: %v", err)
	}

	// 用户不存在与密码错误必须是同一句话，否则登录接口就是账号枚举器
	unknown := e.postForm("/api/v1/auth/login", urlValues("username", "ghost", "password", "whatever"), "").
		Require(t, http.StatusBadRequest).Detail(t)
	wrong := e.postForm("/api/v1/auth/login", urlValues("username", "root", "password", "nope"), "").
		Require(t, http.StatusBadRequest).Detail(t)
	if unknown != wrong {
		t.Fatalf("账号不存在与口令错误的提示不一致，可被用于枚举： %q vs %q", unknown, wrong)
	}

	e.postForm("/api/v1/auth/login", urlValues("username", "blocked", "password", "pass1234"), "").
		Require(t, http.StatusForbidden)
}

func TestLogoutRevokesAccessToken(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")

	token := e.login("root", "pass1234")
	e.get("/api/v1/auth/me", token).Require(t, http.StatusOK)

	e.post("/api/v1/auth/logout", nil, token).Require(t, http.StatusOK)

	// JWT 无状态，不拉黑的话登出等于没登出
	if got := e.get("/api/v1/auth/me", token).Require(t, http.StatusUnauthorized); got.Detail(t) == "" {
		t.Fatal("登出后旧令牌仍可用")
	}

	// 登出后立刻重登必须拿到可用的新会话：
	// 没有 jti 时，同一秒内重登会拿回与刚被拉黑那条逐字节相同的令牌
	again := e.login("root", "pass1234")
	if again == token {
		t.Fatal("重登返回的令牌与已吊销令牌完全相同，新会话会被黑名单误拦")
	}
	e.get("/api/v1/auth/me", again).Require(t, http.StatusOK)
}

func TestAnonymousAndMemberAccessControl(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	member := e.addMember("member")
	memberToken := e.login("member", "pass1234")

	e.get("/api/v1/tasks", "").Require(t, http.StatusUnauthorized)
	// 注册用户不得因为请求里塞了 role 就提权
	if member.Role != model.RoleUser {
		t.Fatalf("成员角色被改成了 %q", member.Role)
	}
	e.post("/api/v1/users", map[string]any{"username": "sneaky", "email": "s@test.local", "password": "pass1234"}, memberToken).
		Require(t, http.StatusForbidden)
	e.get("/api/v1/admin/settings", memberToken).Require(t, http.StatusForbidden)
	e.get("/api/v1/users", memberToken).Require(t, http.StatusOK)
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
