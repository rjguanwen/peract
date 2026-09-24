package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"taskbackend/internal/model"
)

// 本文件盯住「里程碑(计划节点)」这一族接口。
//
// 它们出错时大多仍然是 200: 计划排序反了、PATCH 把没提交的字段清空了、归属校验漏了
// 一半 —— 这三种在响应体里都长得像正常结果。所以断言必须落在**读回来的内容**上,
// 而不是落在状态码上。

type milestoneJSON struct {
	ID        uint   `json:"id"`
	TaskID    uint   `json:"task_id"`
	Title     string `json:"title"`
	PlannedAt string `json:"planned_at"`
	Note      string `json:"note"`
}

// detailJSON 只声明这一组用例要看的字段。Progresses 用 RawMessage 接:
// 它的形状由另一组用例管, 这里只关心"它在不在"。
type detailJSON struct {
	ID         uint              `json:"id"`
	Milestones []milestoneJSON   `json:"milestones"`
	Progresses []json.RawMessage `json:"progresses"`
}

func milestonePath(taskID uint) string { return fmt.Sprintf("/api/v1/tasks/%d/milestones", taskID) }

func milestoneItemPath(taskID, mid uint) string {
	return fmt.Sprintf("/api/v1/tasks/%d/milestones/%d", taskID, mid)
}

// taskUpdatedAt 读任务本体的 updated_at。
func taskUpdatedAt(t *testing.T, e *testEnv, taskID uint) time.Time {
	t.Helper()
	var task model.Task
	if err := e.db.First(&task, taskID).Error; err != nil {
		t.Fatalf("读任务 %d: %v", taskID, err)
	}
	return task.UpdatedAt
}

// 计划节点必须随详情**一起**回来, 且按计划时间升序。
//
// 这条断言的意义在于前端那条合并时间轴: 它拿到的两串必须来自**同一次响应** ——
// 分两次请求会让中间那个状态(有进展、没计划)被渲染出来, 而它看起来像"计划丢了",
// 于是用户会去重新录一遍。排序则是因为合并时间轴要按时间对齐两侧, 而"计划"那一侧
// 的顺序只能是它打算发生的顺序。
func TestMilestonePlanReturnsWithTaskDetailInPlannedOrder(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "上线新版本"})

	// 刻意**乱序**提交: 服务端按 planned_at 排, 而不是按插入顺序。
	e.post(milestonePath(task.ID), map[string]any{
		"title": "上线", "planned_at": "2026-10-20T18:00:00", "note": "切流量",
	}, token).Require(t, http.StatusCreated)
	var first milestoneJSON
	e.post(milestonePath(task.ID), map[string]any{
		"title": "提测", "planned_at": "2026-10-05T09:00:00",
	}, token).Require(t, http.StatusCreated).Into(t, &first)

	if first.TaskID != task.ID {
		t.Fatalf("返回的 task_id = %d, 期望 %d", first.TaskID, task.ID)
	}

	detail := e.get(fmt.Sprintf("/api/v1/tasks/%d", task.ID), token).Require(t, http.StatusOK)
	var out detailJSON
	detail.Into(t, &out)

	if len(out.Milestones) != 2 {
		t.Fatalf("详情里应有 2 个里程碑, 实际 %d 个：%s", len(out.Milestones), detail.Body)
	}
	if out.Milestones[0].Title != "提测" || out.Milestones[1].Title != "上线" {
		t.Fatalf("里程碑未按计划时间升序：%v", out.Milestones)
	}
	// 计划与进展必须同一次回来。这里只要求"这个键在", 不要求它非空 ——
	// 空数组与缺失是两件事, 而前端对两者的渲染不同(空态 vs 报错)。
	if out.Progresses == nil {
		t.Fatalf("详情响应里没有 progresses 键, 计划与进展无法在同一次响应里比对：%s", detail.Body)
	}

	// 只加计划、不加进展, 不该凭空生出一条进展记录 —— 计划是"打算", 不是"发生了什么"。
	// 建任务那一条是唯一的进展记录。
	if len(out.Progresses) != 1 {
		t.Fatalf("只加里程碑却出现了 %d 条进展记录(应只有建任务那一条)", len(out.Progresses))
	}
}

// 三条必填判据都要在写库**之前**挡住, 且不能留下半条记录。
func TestMilestoneRejectsEmptyPlan(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "上线新版本"})

	cases := []struct {
		name    string
		payload map[string]any
		wantIn  string
	}{
		{"名称缺失", map[string]any{"planned_at": "2026-10-05T09:00:00"}, "里程碑名称"},
		{"名称只有空白", map[string]any{"title": "   ", "planned_at": "2026-10-05T09:00:00"}, "里程碑名称"},
		{"计划时间缺失", map[string]any{"title": "提测"}, "计划时间"},
		{"时间格式不合法", map[string]any{"title": "提测", "planned_at": "10月5号"}, "参数不合法"},
		{"名称超长", map[string]any{
			"title": strings.Repeat("长", maxMilestoneTitleLen+1), "planned_at": "2026-10-05T09:00:00",
		}, "最多"},
		{"备注超长", map[string]any{
			"title": "提测", "planned_at": "2026-10-05T09:00:00",
			"note": strings.Repeat("字", maxMilestoneNoteLen+1),
		}, "备注最多"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := e.post(milestonePath(task.ID), c.payload, token).Require(t, http.StatusBadRequest)
			if detail := res.Detail(t); !strings.Contains(detail, c.wantIn) {
				t.Fatalf("错误文案应提到 %q, 实际 %q", c.wantIn, detail)
			}
		})
	}

	// 上面每一次拒绝都不能留下东西 —— 否则界面上会冒出没有名字的横线。
	var count int64
	if err := e.db.Model(&model.TaskMilestone{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
		t.Fatalf("统计里程碑: %v", err)
	}
	if count != 0 {
		t.Fatalf("被拒的请求留下了 %d 条里程碑", count)
	}
}

// 归属必须与主键**一起**作为查询条件。
//
// 只按 id 查会允许"用 A 任务的路径改 B 任务的节点", 而那条路径上的权限判定刚刚才为 A
// 做过 —— 于是持有 A 的写权限的人就能改 B 的计划。这里要求的是 404(查不到)而不是 403:
// 403 等于承认"这个 id 存在", 而它属于另一个任务。
func TestMilestoneIsScopedToItsTask(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	taskA := e.createTask(token, map[string]any{"title": "任务 A"})
	taskB := e.createTask(token, map[string]any{"title": "任务 B"})

	var m milestoneJSON
	e.post(milestonePath(taskA.ID), map[string]any{
		"title": "提测", "planned_at": "2026-10-05T09:00:00",
	}, token).Require(t, http.StatusCreated).Into(t, &m)

	// 用 B 的路径去改、去删 A 的节点
	e.patch(milestoneItemPath(taskB.ID, m.ID), map[string]any{"title": "被篡改"}, token).
		Require(t, http.StatusNotFound)
	e.del(milestoneItemPath(taskB.ID, m.ID), token).Require(t, http.StatusNotFound)

	// A 的节点必须原样还在 —— 上面两次都返回了错误, 而"返回错误"与"什么都没改"是两件事。
	detail := e.get(fmt.Sprintf("/api/v1/tasks/%d", taskA.ID), token).Require(t, http.StatusOK)
	var out detailJSON
	detail.Into(t, &out)
	if len(out.Milestones) != 1 || out.Milestones[0].Title != "提测" {
		t.Fatalf("任务 A 的里程碑被越权改动了：%s", detail.Body)
	}
}

// 写计划的门槛与"改任务/加进展"完全同一套: 创建者、负责人、或有全部任务权限的人。
func TestMilestoneWriteNeedsModifyRight(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	bob := e.addMember("bob")
	rootToken := e.login("root", "pass1234")
	bobToken := e.login("bob", "pass1234")

	task := e.createTask(rootToken, map[string]any{"title": "上线新版本"})
	body := map[string]any{"title": "提测", "planned_at": "2026-10-05T09:00:00"}

	// bob 持 PermTaskUpdate(所以路由那道闸放行), 但他既不是创建者也不是负责人。
	// 这里挡住他的必须是 canModify 那一层 —— 否则"任何能改任务的人都能改任何人的计划"。
	res := e.post(milestonePath(task.ID), body, bobToken).Require(t, http.StatusForbidden)
	if detail := res.Detail(t); !strings.Contains(detail, "里程碑") {
		t.Fatalf("403 的文案应说明是里程碑权限, 实际 %q", detail)
	}

	// 把 bob 设成负责人之后就该放行 —— 否则上面那条 403 可能只是因为整条链路坏了。
	e.patch(fmt.Sprintf("/api/v1/tasks/%d", task.ID),
		map[string]any{"assignee_id": bob.ID}, rootToken).Require(t, http.StatusOK)
	e.post(milestonePath(task.ID), body, bobToken).Require(t, http.StatusCreated)
}

// 上限是 50, 而且第 51 条必须被拒 —— 不能靠"删掉最旧的一条"腾位置。
func TestMilestoneCountIsCapped(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "节点很多的计划"})

	// 直接落库而不是发 50 次请求: 这条用例要盯的是**边界那一次**的行为,
	// 而把 50 次 HTTP 往返也塞进来只会让它在慢机器上变成偶发失败。
	for i := 0; i < maxMilestonesPerTask; i++ {
		m := model.TaskMilestone{
			TaskID:    task.ID,
			Title:     fmt.Sprintf("节点 %d", i),
			PlannedAt: model.DateTime{Time: time.Now().Add(time.Duration(i) * time.Hour)},
		}
		if err := e.db.Create(&m).Error; err != nil {
			t.Fatalf("预置里程碑: %v", err)
		}
	}

	res := e.post(milestonePath(task.ID), map[string]any{
		"title": "第 51 个", "planned_at": "2026-10-05T09:00:00",
	}, token).Require(t, http.StatusBadRequest)
	if detail := res.Detail(t); !strings.Contains(detail, fmt.Sprintf("%d", maxMilestonesPerTask)) {
		t.Fatalf("上限提示应带上数字 %d, 实际 %q", maxMilestonesPerTask, detail)
	}

	var count int64
	e.db.Model(&model.TaskMilestone{}).Where("task_id = ?", task.ID).Count(&count)
	if count != int64(maxMilestonesPerTask) {
		t.Fatalf("被拒之后里程碑数应仍是 %d, 实际 %d", maxMilestonesPerTask, count)
	}
}

// PATCH 是局部更新: 只提交备注时, 名称与计划时间必须原样留着。
//
// 这条是本文件里最容易写错的一条 —— "没提交就当成零值写回去"会得到一个没有时间的计划,
// 而它在时间轴上排到最前面, 看起来像"计划被挪到了很早以前"。
func TestMilestonePatchKeepsUnsubmittedFields(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "上线新版本"})

	var m milestoneJSON
	e.post(milestonePath(task.ID), map[string]any{
		"title": "提测", "planned_at": "2026-10-05T09:00:00", "note": "老备注",
	}, token).Require(t, http.StatusCreated).Into(t, &m)

	var updated milestoneJSON
	e.patch(milestoneItemPath(task.ID, m.ID), map[string]any{"note": "新备注"}, token).
		Require(t, http.StatusOK).Into(t, &updated)

	if updated.Title != "提测" {
		t.Fatalf("只改了备注, 名称却被写成 %q", updated.Title)
	}
	if updated.PlannedAt != m.PlannedAt {
		t.Fatalf("只改了备注, 计划时间却从 %q 变成 %q", m.PlannedAt, updated.PlannedAt)
	}
	if updated.Note != "新备注" {
		t.Fatalf("备注没改成功：%q", updated.Note)
	}

	// 把名称改成空白必须被拒, 而且**库里那一行不能变** —— 否则一次被拒的编辑会静默
	// 把一个计划节点变成一个没有名字的横线。
	res := e.patch(milestoneItemPath(task.ID, m.ID), map[string]any{"title": "   "}, token).
		Require(t, http.StatusBadRequest)
	if detail := res.Detail(t); !strings.Contains(detail, "里程碑名称") {
		t.Fatalf("错误文案应提到里程碑名称, 实际 %q", detail)
	}
	detail := e.get(fmt.Sprintf("/api/v1/tasks/%d", task.ID), token).Require(t, http.StatusOK)
	var out detailJSON
	detail.Into(t, &out)
	if len(out.Milestones) != 1 || out.Milestones[0].Title != "提测" {
		t.Fatalf("被拒的编辑改动了库里的行：%s", detail.Body)
	}
}

// 改计划要动任务的 updated_at。
//
// 判据同"加进展"那一侧: 计划与进展都是"这个任务身上发生的事", 而详情页上那个"更新时间"
// 就是给人看这件事的。只动一处会让"改了计划但更新时间没动"看起来像改动没保存成功。
func TestMilestoneWriteTouchesTaskUpdatedAt(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "上线新版本"})

	before := taskUpdatedAt(t, e, task.ID)
	// 让两次 time.Now() 之间至少隔一个可分辨的间隔: Windows 的时钟粒度不算细,
	// 而这条用例断言的是"变大了", 不是"变大了多少"。
	time.Sleep(20 * time.Millisecond)

	var m milestoneJSON
	e.post(milestonePath(task.ID), map[string]any{
		"title": "提测", "planned_at": "2026-10-05T09:00:00",
	}, token).Require(t, http.StatusCreated).Into(t, &m)

	afterAdd := taskUpdatedAt(t, e, task.ID)
	if !afterAdd.After(before) {
		t.Fatalf("新增里程碑没有推进任务的 updated_at: %v -> %v", before, afterAdd)
	}

	time.Sleep(20 * time.Millisecond)
	e.del(milestoneItemPath(task.ID, m.ID), token).Require(t, http.StatusOK)

	afterDel := taskUpdatedAt(t, e, task.ID)
	if !afterDel.After(afterAdd) {
		t.Fatalf("删除里程碑没有推进任务的 updated_at: %v -> %v", afterAdd, afterDel)
	}
}

// 删一个计划节点必须是真的删掉, 而不是留一行看不见的。
//
// 判据是"再删一次会 404": 如果删的是逻辑删除, 第二次删仍然查得到那一行, 于是它会给
// 200 —— 而那时库里已经有一行永远查不出来的里程碑了。
func TestMilestoneDeleteIsHard(t *testing.T) {
	e := newTestEnv(t)
	e.addAdmin("root")
	token := e.login("root", "pass1234")
	task := e.createTask(token, map[string]any{"title": "上线新版本"})

	var m milestoneJSON
	e.post(milestonePath(task.ID), map[string]any{
		"title": "提测", "planned_at": "2026-10-05T09:00:00",
	}, token).Require(t, http.StatusCreated).Into(t, &m)

	path := milestoneItemPath(task.ID, m.ID)
	e.del(path, token).Require(t, http.StatusOK)
	e.del(path, token).Require(t, http.StatusNotFound)

	var count int64
	e.db.Model(&model.TaskMilestone{}).Where("id = ?", m.ID).Count(&count)
	if count != 0 {
		t.Fatalf("删除之后库里仍有 %d 行; 计划节点没有回收站这个读者", count)
	}
}
