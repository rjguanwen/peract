package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

const (
	maxTitleLength  = 255
	maxDescLength   = 20000
	maxCommentLen   = 5000
	defaultPageSize = 20
	maxPageSize     = 100

	// 里程碑的三条限制。标题比任务标题短得多: 它填的是一个**节点名**("提测"/"上线"),
	// 不是一段说明 —— 留出 255 只会让人把说明写进标题, 然后真正的说明那一栏空着。
	maxMilestoneTitleLen = 128
	maxMilestoneNoteLen  = 500
	// maxMilestonesPerTask 一个任务的里程碑上限。
	//
	// 50 这个数不是从性能来的(这张表小到不值得谈性能), 而是从**语义**来的: 里程碑是
	// "关键节点", 而一个任务有五十个关键节点的时候, 那个清单已经变成了任务清单本身,
	// 它要回答的问题("我打算什么时候到哪一步")在这之前就被淹掉了。划一条线, 是让这个
	// 上限变成一个必须被显式回答的问题, 而不是在某次误导入之后才发现有人建了两千条。
	maxMilestonesPerTask = 50
)

type TaskOut struct {
	ID           uint            `json:"id"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Status       string          `json:"status"`
	Priority     string          `json:"priority"`
	AssigneeID   *uint           `json:"assignee_id"`
	AssigneeName *string         `json:"assignee_name"`
	CreatorID    *uint           `json:"creator_id"`
	CreatorName  *string         `json:"creator_name"`
	DueDate      *model.DateTime `json:"due_date"`
	Progress     int             `json:"progress"`
	IsOverdue    bool            `json:"is_overdue"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	DeletedAt    *time.Time      `json:"deleted_at"`
}

type TaskProgressOut struct {
	ID        uint      `json:"id"`
	TaskID    uint      `json:"task_id"`
	UserID    *uint     `json:"user_id"`
	UserName  *string   `json:"user_name"`
	Action    string    `json:"action"`
	Comment   string    `json:"comment"`
	OldStatus *string   `json:"old_status"`
	NewStatus *string   `json:"new_status"`
	Progress  *int      `json:"progress"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskMilestoneOut 一个计划节点。
//
// 这里**没有**"是否达成""实际完成时间""偏差天数"这类字段, 而且这不是遗漏: 达成与否是
// 计划与进展**比出来**的结论, 不是一个可以存在某一侧的属性。把它算成字段, 就等于在服务端
// 钉死一套判定("有进展记录晚于计划时间就算达成"?), 而真实情形里那句话经常是错的 ——
// 人们会在计划时间之后补一条"其实早就完成了"的备注。结论留给看的人下, 服务端只保证
// 两串时间都是原样的、可对齐的。
type TaskMilestoneOut struct {
	ID        uint           `json:"id"`
	TaskID    uint           `json:"task_id"`
	Title     string         `json:"title"`
	PlannedAt model.DateTime `json:"planned_at"`
	Note      string         `json:"note"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type TaskDetailOut struct {
	TaskOut
	Progresses []TaskProgressOut `json:"progresses"`
	// Milestones 计划节点。它与 Progresses **一起**返回, 而不是另开一条 GET:
	// 这个功能的全部意义就是"两串并排看", 拆成两次请求会让前端在两次响应之间拿到一个
	// 中间状态(有进展、没计划), 而那个状态的界面看起来像"计划丢了" —— 用户会去重新录一遍。
	Milestones []TaskMilestoneOut `json:"milestones"`
}

type taskCreateReq struct {
	Title       string          `json:"title" binding:"required"`
	Description string          `json:"description"`
	Priority    string          `json:"priority"`
	AssigneeID  *uint           `json:"assignee_id"`
	DueDate     *model.DateTime `json:"due_date"`
}

// taskUpdateReq 是 PATCH 语义的局部更新请求。
// 指针为 nil 表示"本次不改"，因此清空字段必须显式使用 clear_* 标志。
type taskUpdateReq struct {
	Title         *string         `json:"title"`
	Description   *string         `json:"description"`
	Priority      *string         `json:"priority"`
	AssigneeID    *uint           `json:"assignee_id"`
	ClearAssignee bool            `json:"clear_assignee"`
	DueDate       *model.DateTime `json:"due_date"`
	ClearDueDate  bool            `json:"clear_due_date"`
	Status        *string         `json:"status"`
	Progress      *int            `json:"progress"`
}

type progressCreateReq struct {
	Comment  string  `json:"comment"`
	Progress *int    `json:"progress"`
	Status   *string `json:"status"`
}

type milestoneCreateReq struct {
	// 这里**不**挂 binding:"required"。三条必填判据统一由 normalizeMilestone 给,
	// 于是"名称为空"与"名称只有空白"得到的是同一句人话。挂上它之后, 空名称会先被
	// validator 拦下, 而它的报错里带着 Go 的结构体名("milestoneCreateReq.Title") ——
	// 那句是给开发者看的, 却会原样出现在界面上(判据同 AddShare: 那里也是手工把
	// binding 的错误翻译成"请提供有效的被分享者邮箱")。
	Title     string          `json:"title"`
	PlannedAt *model.DateTime `json:"planned_at"`
	Note      string          `json:"note"`
}

// milestoneUpdateReq 是 PATCH 语义的局部更新请求: 指针为 nil 表示"本次不改"。
//
// 这里**没有** clear_* 标志, 而 taskUpdateReq 有 —— 判据是"这个字段能不能为空":
// 任务的负责人与截止时间可以为空(所以需要一个显式的清空开关把"不改"与"改成空"分开),
// 而里程碑的名称与计划时间都不能为空。少一个必填字段, 那个节点就不再是计划。
type milestoneUpdateReq struct {
	Title     *string         `json:"title"`
	PlannedAt *model.DateTime `json:"planned_at"`
	Note      *string         `json:"note"`
}

// pendingNotice 事务提交后才投递的外部通知，避免回滚后仍发出假通知。
type pendingNotice struct {
	userID  uint
	title   string
	content string
}

func validStatus(s string) bool {
	switch s {
	case model.StatusTodo, model.StatusInProgress, model.StatusDone:
		return true
	}
	return false
}

func validPriority(p string) bool {
	switch p {
	case model.PriorityLow, model.PriorityMedium, model.PriorityHigh, model.PriorityUrgent:
		return true
	}
	return false
}

func userNamePtr(u *model.User) *string {
	if u == nil {
		return nil
	}
	name := u.FullName
	if name == "" {
		name = u.Username
	}
	return &name
}

func toTaskOut(t *model.Task) TaskOut {
	t.ComputeOverdue()
	var deletedAt *time.Time
	if t.DeletedAt.Valid {
		d := t.DeletedAt.Time
		deletedAt = &d
	}
	return TaskOut{
		ID:           t.ID,
		Title:        t.Title,
		Description:  t.Description,
		Status:       t.Status,
		Priority:     t.Priority,
		AssigneeID:   t.AssigneeID,
		AssigneeName: userNamePtr(t.Assignee),
		CreatorID:    t.CreatorID,
		CreatorName:  userNamePtr(t.Creator),
		DueDate:      t.DueDate,
		Progress:     t.Progress,
		IsOverdue:    t.IsOverdue,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
		DeletedAt:    deletedAt,
	}
}

func toMilestoneOut(m *model.TaskMilestone) TaskMilestoneOut {
	return TaskMilestoneOut{
		ID:        m.ID,
		TaskID:    m.TaskID,
		Title:     m.Title,
		PlannedAt: m.PlannedAt,
		Note:      m.Note,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toProgressOut(p *model.TaskProgress) TaskProgressOut {
	return TaskProgressOut{
		ID:        p.ID,
		TaskID:    p.TaskID,
		UserID:    p.UserID,
		UserName:  userNamePtr(p.User),
		Action:    p.Action,
		Comment:   p.Comment,
		OldStatus: p.OldStatus,
		NewStatus: p.NewStatus,
		Progress:  p.Progress,
		CreatedAt: p.CreatedAt,
	}
}

// 下面三个判定的第一个条件原来都是"角色是不是 admin"。接入 OneLink 之后角色在平台侧
// (sys_user_role.app_id), 应用侧拿到的只有权限码快照 —— 而这三个位置要问的其实是
// "这个人是不是**已经能看见全部任务**", 那正好是 PermTaskListAll 这个权限点的含义。
//
// 判据必须从会话里取(而不是从 model.User 上读一列): 本地档案里没有任何权限信息,
// 而从平台实时查一次会让每一次编辑都多一次网络往返, 换来的是"权限撤销后立即生效" ——
// 那件事由平台侧对每次带令牌的调用重判来保证, 这里不需要重复实现。

// canModify 改任务状态/进展的权限: 能看全部的人, 或这个任务的创建者/被分配人。
func (h *Handler) canModify(c *gin.Context, t *model.Task) bool {
	ctx := currentUser(c)
	if ctx == nil {
		return false
	}
	if can(c, PermTaskListAll) {
		return true
	}
	if t.CreatorID != nil && *t.CreatorID == ctx.ID {
		return true
	}
	if t.AssigneeID != nil && *t.AssigneeID == ctx.ID {
		return true
	}
	return false
}

// canDelete 删除权限：能看全部的人, 或任务创建者
func (h *Handler) canDelete(c *gin.Context, t *model.Task) bool {
	return h.canEdit(c, t)
}

// canEdit 编辑权限：能看全部的人, 或任务创建者
func (h *Handler) canEdit(c *gin.Context, t *model.Task) bool {
	ctx := currentUser(c)
	if ctx == nil {
		return false
	}
	if can(c, PermTaskListAll) {
		return true
	}
	return t.CreatorID != nil && *t.CreatorID == ctx.ID
}

// loadTask 按路径参数取任务，失败时已写好响应。
func (h *Handler) loadTask(c *gin.Context) (*model.Task, bool) {
	id, err := parseIDParam(c)
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return nil, false
	}
	var task model.Task
	if err := h.db.Preload("Assignee").Preload("Creator").First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return nil, false
	}
	return &task, true
}

// progressRecord 构造一条待写入的进展记录
func progressRecord(t *model.Task, user *userCtx, action, comment string, oldStatus, newStatus *string, progress *int) model.TaskProgress {
	return model.TaskProgress{
		TaskID:    t.ID,
		UserID:    &user.ID,
		Action:    action,
		Comment:   comment,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Progress:  progress,
	}
}

// enqueueNotices 事务提交成功后再投递外部通知（站内提醒已随事务落库）。
func (h *Handler) enqueueNotices(taskTitle string, notices []pendingNotice) {
	for _, n := range notices {
		var user model.User
		if err := h.db.Select("id, email").First(&user, n.userID).Error; err != nil || user.Email == "" {
			continue
		}
		h.notify.Enqueue(service.TaskNotice{
			Title:   taskTitle,
			Content: n.content,
			EmailTo: user.Email,
		})
	}
}

// CreateTask POST /tasks
func (h *Handler) CreateTask(c *gin.Context) {
	var req taskCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	if req.Priority == "" {
		req.Priority = model.PriorityMedium
	}
	if !validPriority(req.Priority) {
		badRequest(c, "优先级不合法")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		badRequest(c, "标题不能为空")
		return
	}
	if len([]rune(title)) > maxTitleLength {
		badRequest(c, fmt.Sprintf("标题最多 %d 字", maxTitleLength))
		return
	}
	if len([]rune(req.Description)) > maxDescLength {
		badRequest(c, fmt.Sprintf("描述最多 %d 字", maxDescLength))
		return
	}
	ctx := currentUser(c)

	var records []model.TaskProgress
	var notices []pendingNotice
	task := model.Task{
		Title:       title,
		Description: req.Description,
		Status:      model.StatusTodo,
		Priority:    req.Priority,
		AssigneeID:  req.AssigneeID,
		CreatorID:   &ctx.ID,
		DueDate:     req.DueDate,
		Progress:    0,
	}

	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		records = append(records, progressRecord(&task, ctx, model.ActionCreated, "任务登记", nil, nil, nil))
		if req.AssigneeID != nil && *req.AssigneeID != ctx.ID {
			reminders, ns := assignmentNotices(&task, *req.AssigneeID, "你有一个新任务「"+task.Title+"」，请及时处理")
			records = append(records, progressRecord(&task, ctx, model.ActionAssigned, "", nil, nil, nil))
			for _, r := range reminders {
				if err := tx.Create(&r).Error; err != nil {
					return err
				}
			}
			notices = append(notices, ns...)
		}
		for i := range records {
			if err := tx.Create(&records[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "创建任务失败")
		return
	}
	h.enqueueNotices(task.Title, notices)

	h.reloadTask(&task)
	c.JSON(http.StatusCreated, toTaskOut(&task))
}

// assignmentNotices 生成"分配"类提醒（站内 + 外部）。
func assignmentNotices(t *model.Task, targetID uint, message string) ([]model.Reminder, []pendingNotice) {
	reminders := []model.Reminder{{
		TaskID:     t.ID,
		UserID:     &targetID,
		RemindAt:   time.Now(),
		RemindType: model.RemindAssign,
		Message:    message,
	}}
	notices := []pendingNotice{{userID: targetID, title: t.Title, content: message}}
	return reminders, notices
}

// statusNotices 生成"状态变更"类提醒。
func statusNotices(t *model.Task, targetID uint, message string) ([]model.Reminder, []pendingNotice) {
	reminders := []model.Reminder{{
		TaskID:     t.ID,
		UserID:     &targetID,
		RemindAt:   time.Now(),
		RemindType: model.RemindStatus,
		Message:    message,
	}}
	return reminders, []pendingNotice{{userID: targetID, title: t.Title, content: message}}
}

// ListTasks GET /tasks
// visibility=mine（默认）：只看得到自己创建的/分配给自己的/被分享的任务
// visibility=all（需要 PermTaskListAll）：可看所有任务
// ?mine=true 沿用兼容旧行为，等效于 visibility=mine
// ?creator_id= 可进一步按创建者筛选（能看全部的人用）
func (h *Handler) ListTasks(c *gin.Context) {
	ctx := currentUser(c)

	// 判据从"角色是不是 admin"换成了权限点。名字也从 isAdmin 改成 canSeeAll:
	// 它要回答的从来不是"这个人是不是管理员", 而是"他能不能看见全部任务" ——
	// 前者在角色只有两个值的年代恰好等价, 而现在"躬行管理员"这个角色里可以只勾
	// 一半权限点。
	canSeeAll := can(c, PermTaskListAll)

	// visibility：mine / all；mine=true 向后兼容映射到 mine
	vis := c.Query("visibility")
	if vis == "" && c.Query("mine") == "true" {
		vis = "mine"
	}
	if vis == "" {
		vis = "mine"
	}
	if vis == "all" && !canSeeAll {
		forbidden(c, "没有查看全部任务的权限")
		return
	}

	filters := taskFilters{
		status:      c.Query("status"),
		priority:    c.Query("priority"),
		assigneeID:  c.Query("assignee_id"),
		creatorID:   c.Query("creator_id"),
		keyword:     c.Query("keyword"),
		overdueOnly: c.Query("overdue_only") == "true",
		visibility:  vis,
	}
	if filters.status != "" && !validStatus(filters.status) {
		badRequest(c, "状态筛选值不合法")
		return
	}
	if filters.priority != "" && !validPriority(filters.priority) {
		badRequest(c, "优先级筛选值不合法")
		return
	}

	page, pageSize := parsePagination(c)

	// buildQuery 组装查询条件，自动注入权限范围
	buildQuery := func() *gorm.DB {
		q := h.db.Model(&model.Task{})

		// --- 权限范围 ---
		if !canSeeAll {
			// 没有 PermTaskListAll：只看 creator_id=me OR assignee_id=me OR 被分享的任务
			q = q.Where("(creator_id = ? OR assignee_id = ? OR id IN (SELECT task_id FROM task_shares WHERE user_id = ?))",
				ctx.ID, ctx.ID, ctx.ID)
		}

		if filters.status != "" {
			q = q.Where("status = ?", filters.status)
		}
		if filters.priority != "" {
			q = q.Where("priority = ?", filters.priority)
		}
		if filters.assigneeID != "" {
			if id, err := strconv.Atoi(filters.assigneeID); err == nil && id > 0 {
				q = q.Where("assignee_id = ?", id)
			}
		}
		// creator_id 筛选：能看全部的人可按创建者查，其余人只能查自己（已在权限范围约束）
		if filters.creatorID != "" {
			if id, err := strconv.Atoi(filters.creatorID); err == nil && id > 0 {
				if canSeeAll {
					q = q.Where("creator_id = ?", id)
				} else if id == int(ctx.ID) {
					q = q.Where("creator_id = ?", id)
				} // 查其他创建者 → 静默忽略，维持自己的可见范围
			}
		}
		if filters.keyword != "" {
			like := likePattern(filters.keyword)
			q = q.Where(rawLikeClause, like, like)
		}
		if filters.overdueOnly {
			q = q.Where("status != ? AND due_date IS NOT NULL AND due_date < ?", model.StatusDone, time.Now())
		}
		return q
	}

	var total int64
	if err := buildQuery().Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询任务失败")
		return
	}

	var tasks []model.Task
	if err := buildQuery().Preload("Assignee").Preload("Creator").
		Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询任务失败")
		return
	}

	items := make([]TaskOut, 0, len(tasks))
	for i := range tasks {
		items = append(items, toTaskOut(&tasks[i]))
	}
	c.JSON(http.StatusOK, pageResult(items, total, page, pageSize))
}

// GetTask GET /tasks/:id
func (h *Handler) GetTask(c *gin.Context) {
	id, err := parseIDParam(c)
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	var task model.Task
	if err := h.db.Preload("Assignee").Preload("Creator").First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	// 权限检查：admin/创建者/负责人/被分享者才可查看
	if !h.canViewTask(c, uint(id)) {
		forbidden(c, "你没有查看此任务的权限")
		return
	}
	var progresses []model.TaskProgress
	if err := h.db.Preload("User").Where("task_id = ?", id).Order("created_at").Find(&progresses).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询进展记录失败")
		return
	}

	// 计划节点。按计划时间升序 —— 详情页把它与进展记录合并成一条时间轴, 而"计划"
	// 那一侧的排序只有一种有意义的读法: 按它打算发生的时间。
	//
	// 排序里带上 id 作为第二段: 同一时刻的两个节点(很常见, 比如"提测"与"发通知")
	// 若只按时间排, 两次请求可能给出不同的顺序, 而界面上会表现为"刷新一下顺序就变了"。
	var milestones []model.TaskMilestone
	if err := h.db.Where("task_id = ?", id).Order("planned_at, id").Find(&milestones).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询里程碑失败")
		return
	}

	out := TaskDetailOut{TaskOut: toTaskOut(&task)}
	out.Progresses = make([]TaskProgressOut, 0, len(progresses))
	for i := range progresses {
		out.Progresses = append(out.Progresses, toProgressOut(&progresses[i]))
	}
	out.Milestones = make([]TaskMilestoneOut, 0, len(milestones))
	for i := range milestones {
		out.Milestones = append(out.Milestones, toMilestoneOut(&milestones[i]))
	}
	c.JSON(http.StatusOK, out)
}

// UpdateTask PATCH /tasks/:id
//
// 整个写入过程在一个事务里完成：先做全量校验，再落库，
// 杜绝"校验失败返回 400，但状态变更/负责人变更的进展记录与提醒已经写进库"的脏数据。
func (h *Handler) UpdateTask(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	ctx := currentUser(c)
	if !h.canModify(c, task) {
		forbidden(c, "只有创建者、负责人或有全部任务权限的人可以修改任务")
		return
	}
	var req taskUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}

	// ---- 1. 纯校验阶段：任何失败都直接返回，不产生副作用 ----
	if req.ClearAssignee && req.AssigneeID != nil {
		badRequest(c, "assignee_id 与 clear_assignee 不能同时提供")
		return
	}
	if req.ClearDueDate && req.DueDate != nil {
		badRequest(c, "due_date 与 clear_due_date 不能同时提供")
		return
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			badRequest(c, "标题不能为空")
			return
		}
		if len([]rune(t)) > maxTitleLength {
			badRequest(c, fmt.Sprintf("标题最多 %d 字", maxTitleLength))
			return
		}
	}
	if req.Description != nil && len([]rune(*req.Description)) > maxDescLength {
		badRequest(c, fmt.Sprintf("描述最多 %d 字", maxDescLength))
		return
	}
	if req.Priority != nil && !validPriority(*req.Priority) {
		badRequest(c, "优先级不合法")
		return
	}
	if req.Status != nil && !validStatus(*req.Status) {
		badRequest(c, "状态不合法")
		return
	}
	if req.Progress != nil && (*req.Progress < 0 || *req.Progress > 100) {
		badRequest(c, "进度需在 0-100 之间")
		return
	}

	var newAssignee *model.User
	if req.AssigneeID != nil {
		var assignee model.User
		if err := h.db.Select("id, username, full_name").First(&assignee, *req.AssigneeID).Error; err != nil {
			badRequest(c, "负责人不存在")
			return
		}
		newAssignee = &assignee
	}

	// 编辑基本信息（标题/描述/优先级/负责人/截止时间）仅限管理员或创建者；
	// 状态与进度流转仍允许负责人操作。
	editsBasic := req.Title != nil || req.Description != nil || req.Priority != nil ||
		req.DueDate != nil || req.ClearDueDate || req.AssigneeID != nil || req.ClearAssignee
	if editsBasic && !h.canEdit(c, task) {
		forbidden(c, "只有任务创建者或有全部任务权限的人可以编辑任务信息")
		return
	}

	// ---- 2. 计算变更 ----
	var (
		records   []model.TaskProgress
		reminders []model.Reminder
		notices   []pendingNotice
		changed   bool
	)

	if req.Status != nil && *req.Status != task.Status {
		old, newStatus := task.Status, *req.Status
		task.Status = newStatus
		switch {
		case newStatus == model.StatusDone:
			task.Progress = 100
		case newStatus == model.StatusTodo && task.Progress == 100:
			task.Progress = 0
		}
		records = append(records, progressRecord(task, ctx, model.ActionStatusChanged, "", &old, &newStatus, nil))
		if task.AssigneeID != nil && *task.AssigneeID != ctx.ID {
			rs, ns := statusNotices(task, *task.AssigneeID, "任务「"+task.Title+"」状态变更为："+statusLabel(newStatus))
			reminders = append(reminders, rs...)
			notices = append(notices, ns...)
		}
		changed = true
	}

	if req.Progress != nil && *req.Progress != task.Progress {
		p := *req.Progress
		task.Progress = p
		switch {
		case p == 100 && task.Status != model.StatusDone:
			task.Status = model.StatusDone
		case p < 100 && task.Status == model.StatusDone:
			task.Status = model.StatusInProgress
		}
		records = append(records, progressRecord(task, ctx, model.ActionProgressUpdate, "进度更新", nil, nil, &p))
		changed = true
	}

	if req.Title != nil {
		task.Title = strings.TrimSpace(*req.Title)
		changed = true
	}
	if req.Description != nil {
		task.Description = *req.Description
		changed = true
	}
	if req.Priority != nil {
		task.Priority = *req.Priority
		changed = true
	}
	if req.ClearDueDate {
		task.DueDate = nil
		changed = true
	} else if req.DueDate != nil {
		task.DueDate = req.DueDate
		changed = true
	}

	if req.ClearAssignee {
		if task.AssigneeID != nil {
			task.AssigneeID = nil
			records = append(records, progressRecord(task, ctx, model.ActionAssigned, "取消负责人", nil, nil, nil))
			changed = true
		}
	} else if newAssignee != nil {
		same := task.AssigneeID != nil && *task.AssigneeID == newAssignee.ID
		if !same {
			comment := "分配给 " + newAssignee.FullName
			if newAssignee.FullName == "" {
				comment = "分配给 " + newAssignee.Username
			}
			task.AssigneeID = &newAssignee.ID
			records = append(records, progressRecord(task, ctx, model.ActionAssigned, comment, nil, nil, nil))
			if newAssignee.ID != ctx.ID {
				rs, ns := assignmentNotices(task, newAssignee.ID, "你被分配了任务「"+task.Title+"」，请及时处理")
				reminders = append(reminders, rs...)
				notices = append(notices, ns...)
			}
			changed = true
		}
	}

	if !changed {
		// 没有任何实际改动，直接回当前状态，避免无意义的写入与 updated_at 抖动
		h.reloadTask(task)
		c.JSON(http.StatusOK, toTaskOut(task))
		return
	}

	// ---- 3. 一次性落库 ----
	task.UpdatedAt = time.Now()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTask(tx, task).Error; err != nil {
			return err
		}
		for i := range records {
			if err := tx.Create(&records[i]).Error; err != nil {
				return err
			}
		}
		for i := range reminders {
			if err := tx.Create(&reminders[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "更新任务失败")
		return
	}
	h.enqueueNotices(task.Title, notices)

	h.reloadTask(task)
	c.JSON(http.StatusOK, toTaskOut(task))
}

// DeleteTask DELETE /tasks/:id（逻辑删除）
func (h *Handler) DeleteTask(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	if !h.canDelete(c, task) {
		forbidden(c, "只有任务创建者或有全部任务权限的人可以删除任务")
		return
	}
	// GORM 软删除：仅写入 deleted_at，不物理删除
	if err := h.db.Delete(task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "删除任务失败")
		return
	}
	c.Status(http.StatusNoContent)
}

// ListDeletedTasks GET /tasks/deleted 查看已删除任务（管理员看全部，其他角色只看自己的）
func (h *Handler) ListDeletedTasks(c *gin.Context) {
	ctx := currentUser(c)
	priority := c.Query("priority")
	if priority != "" && !validPriority(priority) {
		badRequest(c, "优先级筛选值不合法")
		return
	}
	keyword := c.Query("keyword")
	page, pageSize := parsePagination(c)

	build := func() *gorm.DB {
		// Unscoped() 只是关闭 GORM 的软删除过滤，必须再显式要求 deleted_at 非空，
		// 否则回收站会把未删除的任务一并列出。
		q := h.db.Unscoped().Model(&model.Task{}).Where("deleted_at IS NOT NULL")
		// 回收站里能看多少, 判据与列表一致: "能不能看全部任务"。
		if !can(c, PermTaskListAll) {
			q = q.Where("creator_id = ?", ctx.ID)
		}
		if priority != "" {
			q = q.Where("priority = ?", priority)
		}
		if keyword != "" {
			like := likePattern(keyword)
			q = q.Where(rawLikeClause, like, like)
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询回收站失败")
		return
	}

	var tasks []model.Task
	if err := build().Preload("Assignee").Preload("Creator").
		Order("deleted_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询回收站失败")
		return
	}

	items := make([]TaskOut, 0, len(tasks))
	for i := range tasks {
		items = append(items, toTaskOut(&tasks[i]))
	}
	c.JSON(http.StatusOK, pageResult(items, total, page, pageSize))
}

// RestoreTask POST /tasks/:id/restore 恢复已删除任务
func (h *Handler) RestoreTask(c *gin.Context) {
	id, err := parseIDParam(c)
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	var task model.Task
	if err := h.db.Unscoped().First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if !task.IsDeleted() {
		badRequest(c, "任务未删除，无需恢复")
		return
	}
	if !h.canDelete(c, &task) {
		forbidden(c, "只有任务创建者或有全部任务权限的人可以恢复任务")
		return
	}
	// 单条 UPDATE 即可完成恢复：UpdateColumn 绕开软删除写保护，也不再需要二次 Save 全量字段
	if err := h.db.Unscoped().Model(&model.Task{}).
		Where("id = ?", id).
		UpdateColumn("deleted_at", nil).Error; err != nil {
		fail(c, http.StatusInternalServerError, "恢复任务失败")
		return
	}
	h.reloadTask(&task)
	c.JSON(http.StatusOK, toTaskOut(&task))
}

// AddProgress POST /tasks/:id/progress
func (h *Handler) AddProgress(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	if !h.canModify(c, task) {
		forbidden(c, "只有创建者、负责人或有全部任务权限的人可以添加进展")
		return
	}
	var req progressCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	if len([]rune(req.Comment)) > maxCommentLen {
		badRequest(c, fmt.Sprintf("进展备注最多 %d 字", maxCommentLen))
		return
	}

	ctx := currentUser(c)
	var record model.TaskProgress
	switch {
	case req.Status != nil && *req.Status != "":
		if !validStatus(*req.Status) {
			badRequest(c, "状态不合法")
			return
		}
		old, newStatus := task.Status, *req.Status
		task.Status = newStatus
		if newStatus == model.StatusDone {
			task.Progress = 100
		}
		record = progressRecord(task, ctx, model.ActionStatusChanged, req.Comment, &old, &newStatus, nil)
	case req.Progress != nil:
		p := *req.Progress
		if p < 0 || p > 100 {
			badRequest(c, "进度需在 0-100 之间")
			return
		}
		task.Progress = p
		if p == 100 && task.Status != model.StatusDone {
			task.Status = model.StatusDone
		}
		record = progressRecord(task, ctx, model.ActionProgressUpdate, req.Comment, nil, nil, &p)
	default:
		if strings.TrimSpace(req.Comment) == "" {
			badRequest(c, "请填写进展内容")
			return
		}
		record = progressRecord(task, ctx, model.ActionComment, req.Comment, nil, nil, nil)
	}

	task.UpdatedAt = time.Now()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTask(tx, task).Error; err != nil {
			return err
		}
		record.TaskID = task.ID
		return tx.Create(&record).Error
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "更新任务失败")
		return
	}
	// 回读只为补上提交人名称，失败不影响刚才的写入，不值得把已成功的请求报成 500
	if err := h.db.Preload("User").First(&record, record.ID).Error; err != nil {
		log.Printf("[任务进展] 回读提交人失败：%v", err)
	}
	c.JSON(http.StatusOK, toProgressOut(&record))
}

// ---------------------------- 里程碑(计划) ----------------------------
//
// 三个入口共用同一套判据, 写在这里一次:
//
//   - **读**: 由路由上的 RequirePerm(PermTaskList) + canViewTask 管 —— 计划随详情页
//     一起返回, 不另开 GET(理由见 TaskDetailOut.Milestones 的注释)。
//   - **写**: RequirePerm(PermTaskUpdate) + canModify, 与"改状态/加进展"**完全同一套**。
//
// 为什么不为里程碑单独开一个权限点: 它是任务计划的一部分, 而"能改这个任务的人"就是
// "能定这个任务的计划的人"。拆成两个码的实际后果是新建一个应用之后要再授一次权,
// 而"新功能上线后没人能用"是这类改动最容易引入的故障 —— 它不报错, 只是界面上那几个
// 按钮不出现, 而排查方向会被带到前端。真要拆出去时, 判据应当是"有一种人的职责是定
// 计划但不碰执行", 而不是"它是一张新表"。

// touchTaskPlan 把"任务的计划被改过"落到 tasks.updated_at 上, 与里程碑的写入同事务。
//
// 判据同 AddProgress: 进展与计划都是"这个任务身上发生的事", 而详情页上那个"更新时间"
// 就是给人看这件事的。两处只动一处的后果是"改了计划, 但更新时间没动" —— 那看起来
// 像改动没保存成功, 而人会再改一遍。
//
// 单条 UPDATE 而不是 saveTask: 后者会把内存里的关联一并写回(见 saveTask 的注释),
// 而这里要改的只有一列。
func touchTaskPlan(tx *gorm.DB, taskID uint) error {
	return tx.Model(&model.Task{}).Where("id = ?", taskID).
		UpdateColumn("updated_at", time.Now()).Error
}

// normalizeMilestone 校验并归一化一条计划节点。
//
// 三条判据指向同一件事: **计划不能是空的**。名称为空、或没有时间的节点在界面上会渲染
// 成一条没有任何信息的横线, 而它还会参与时间轴排序 —— 那种行除了让人以为"数据坏了"
// 之外没有别的用途。
func normalizeMilestone(title, note string, planned *model.DateTime) (string, string, model.DateTime, error) {
	t := strings.TrimSpace(title)
	switch {
	case t == "":
		return "", "", model.DateTime{}, errors.New("请填写里程碑名称")
	case len([]rune(t)) > maxMilestoneTitleLen:
		return "", "", model.DateTime{}, fmt.Errorf("里程碑名称最多 %d 字", maxMilestoneTitleLen)
	case planned == nil || planned.IsZero():
		return "", "", model.DateTime{}, errors.New("请选择里程碑的计划时间")
	case len([]rune(note)) > maxMilestoneNoteLen:
		return "", "", model.DateTime{}, fmt.Errorf("备注最多 %d 字", maxMilestoneNoteLen)
	}
	return t, note, *planned, nil
}

// loadMilestone 取本任务下的一个节点, 失败时已写好响应。
//
// 归属(task_id)与主键**一起**作为查询条件, 而不是先按 id 取出来再比较:
// 只按 id 查会允许"用 A 任务的路径改 B 任务的节点" —— 而那条路径上的权限判定刚刚
// 才为 A 做过。把归属放进 WHERE, 这种请求在数据库那一层就查不到东西, 于是它得到的
// 是一句"里程碑不存在", 而不是一次越权。
func (h *Handler) loadMilestone(c *gin.Context, taskID uint) (*model.TaskMilestone, bool) {
	mid, err := strconv.Atoi(c.Param("mid"))
	if err != nil || mid <= 0 {
		badRequest(c, "里程碑 ID 不合法")
		return nil, false
	}
	var m model.TaskMilestone
	if err := h.db.Where("id = ? AND task_id = ?", mid, taskID).First(&m).Error; err != nil {
		notFound(c, "里程碑不存在")
		return nil, false
	}
	return &m, true
}

// AddMilestone POST /tasks/:id/milestones
func (h *Handler) AddMilestone(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	if !h.canModify(c, task) {
		forbidden(c, "只有创建者、负责人或有全部任务权限的人可以设定里程碑")
		return
	}
	var req milestoneCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	title, note, planned, err := normalizeMilestone(req.Title, req.Note, req.PlannedAt)
	if err != nil {
		badRequest(c, err.Error())
		return
	}

	// 条数上限在写之前查, 而不是靠唯一索引: 这里没有可以借力的唯一约束 ——
	// 同一个任务上有两个同名节点是完全正常的("评审"来回两次)。
	var count int64
	if err := h.db.Model(&model.TaskMilestone{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询里程碑失败")
		return
	}
	if count >= maxMilestonesPerTask {
		badRequest(c, fmt.Sprintf("一个任务最多 %d 个里程碑", maxMilestonesPerTask))
		return
	}

	m := model.TaskMilestone{TaskID: task.ID, Title: title, PlannedAt: planned, Note: note}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		return touchTaskPlan(tx, task.ID)
	}); err != nil {
		fail(c, http.StatusInternalServerError, "保存里程碑失败")
		return
	}
	c.JSON(http.StatusCreated, toMilestoneOut(&m))
}

// UpdateMilestone PATCH /tasks/:id/milestones/:mid
func (h *Handler) UpdateMilestone(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	if !h.canModify(c, task) {
		forbidden(c, "只有创建者、负责人或有全部任务权限的人可以修改里程碑")
		return
	}
	m, ok := h.loadMilestone(c, task.ID)
	if !ok {
		return
	}
	var req milestoneUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}

	// 局部更新: 先把"改完之后的完整形态"拼出来再交给同一套校验, 而不是逐个字段各判一次。
	// 后者会让"只改备注"与"同时改名称和时间"走两条不同的判定路径, 而两条路径迟早不一致。
	title, note, planned := m.Title, m.Note, m.PlannedAt
	if req.Title != nil {
		title = *req.Title
	}
	if req.Note != nil {
		note = *req.Note
	}
	if req.PlannedAt != nil {
		planned = *req.PlannedAt
	}
	newTitle, newNote, newPlanned, err := normalizeMilestone(title, note, &planned)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	m.Title, m.Note, m.PlannedAt = newTitle, newNote, newPlanned

	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(m).Error; err != nil {
			return err
		}
		return touchTaskPlan(tx, task.ID)
	}); err != nil {
		fail(c, http.StatusInternalServerError, "保存里程碑失败")
		return
	}
	c.JSON(http.StatusOK, toMilestoneOut(m))
}

// DeleteMilestone DELETE /tasks/:id/milestones/:mid
//
// 真删, 不是逻辑删除: 计划节点没有"回收站"这个读者(对比 Task —— 那里的逻辑删除是为
// 了回收站页面)。留一行看不见的节点只会让"这个任务到底有几个节点"变成一个需要带
// 条件才能问清楚的问题。
func (h *Handler) DeleteMilestone(c *gin.Context) {
	task, ok := h.loadTask(c)
	if !ok {
		return
	}
	if !h.canModify(c, task) {
		forbidden(c, "只有创建者、负责人或有全部任务权限的人可以删除里程碑")
		return
	}
	m, ok := h.loadMilestone(c, task.ID)
	if !ok {
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.TaskMilestone{}, m.ID).Error; err != nil {
			return err
		}
		return touchTaskPlan(tx, task.ID)
	}); err != nil {
		fail(c, http.StatusInternalServerError, "删除里程碑失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// saveTask 只写任务本体。
//
// 必须显式 Select("*") + Omit 关联：loadTask 会把 Assignee/Creator 一并读进结构体，
// 而带着已加载关联的 Save 会先 upsert 关联、再把关联主键回写进外键列——
// 于是 clear_assignee 刚置为 NULL 就被覆盖回旧负责人，users 行也会被内存里的旧副本改写。
// Select("*") 同时保证 nil 指针按 NULL 落库，不被零值跳过。
func saveTask(tx *gorm.DB, task *model.Task) *gorm.DB {
	return tx.Select("*").Omit("Assignee", "Creator", "Progresses").Save(task)
}

// reloadTask 重新加载关联，确保返回给前端时名称完整。
// 同样只是补全展示字段，失败时降级为不显示人名，不中断请求。
func (h *Handler) reloadTask(t *model.Task) {
	if err := h.db.Preload("Assignee").Preload("Creator").First(t, t.ID).Error; err != nil {
		log.Printf("[任务] 回读关联用户失败（task=%d）：%v", t.ID, err)
	}
}

// statusLabel 把状态码翻译成中文，用于提醒文案（此前直接推送 "done" 这类原始值）。
func statusLabel(s string) string {
	switch s {
	case model.StatusTodo:
		return "待处理"
	case model.StatusInProgress:
		return "进行中"
	case model.StatusDone:
		return "已完成"
	default:
		return s
	}
}
