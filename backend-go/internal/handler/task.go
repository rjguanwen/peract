package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/middleware"
	"taskbackend/internal/model"
	"taskbackend/internal/service"
)

const (
	maxTitleLength  = 255
	maxDescLength   = 20000
	maxCommentLen   = 5000
	defaultPageSize = 20
	maxPageSize     = 100
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

type TaskDetailOut struct {
	TaskOut
	Progresses []TaskProgressOut `json:"progresses"`
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

func (h *Handler) canModify(t *model.Task, user *middleware.UserContext) bool {
	if user.Role == model.RoleAdmin {
		return true
	}
	if t.CreatorID != nil && *t.CreatorID == user.ID {
		return true
	}
	if t.AssigneeID != nil && *t.AssigneeID == user.ID {
		return true
	}
	return false
}

// canDelete 删除权限：仅管理员或任务创建者
func (h *Handler) canDelete(t *model.Task, user *middleware.UserContext) bool {
	return h.canEdit(t, user)
}

// canEdit 编辑权限：仅管理员或任务创建者
func (h *Handler) canEdit(t *model.Task, user *middleware.UserContext) bool {
	if user.Role == model.RoleAdmin {
		return true
	}
	return t.CreatorID != nil && *t.CreatorID == user.ID
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
func progressRecord(t *model.Task, user *middleware.UserContext, action, comment string, oldStatus, newStatus *string, progress *int) model.TaskProgress {
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
func (h *Handler) ListTasks(c *gin.Context) {
	ctx := currentUser(c)

	filters := taskFilters{
		status:      c.Query("status"),
		priority:    c.Query("priority"),
		assigneeID:  c.Query("assignee_id"),
		keyword:     c.Query("keyword"),
		overdueOnly: c.Query("overdue_only") == "true",
		mine:        c.Query("mine") == "true",
	}
	if filters.mine {
		filters.assigneeID = strconv.FormatUint(uint64(ctx.ID), 10)
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

	// count 与 find 使用相互独立的会话，避免 Order/Limit 污染 COUNT 语句
	build := func() *gorm.DB {
		q := h.db.Model(&model.Task{})
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
	if err := build().Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询任务失败")
		return
	}

	var tasks []model.Task
	if err := build().Preload("Assignee").Preload("Creator").
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
	var progresses []model.TaskProgress
	if err := h.db.Preload("User").Where("task_id = ?", id).Order("created_at").Find(&progresses).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询进展记录失败")
		return
	}

	out := TaskDetailOut{TaskOut: toTaskOut(&task)}
	out.Progresses = make([]TaskProgressOut, 0, len(progresses))
	for i := range progresses {
		out.Progresses = append(out.Progresses, toProgressOut(&progresses[i]))
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
	if !h.canModify(task, ctx) {
		forbidden(c, "只有创建者、负责人或管理员可以修改任务")
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
	if editsBasic && !h.canEdit(task, ctx) {
		forbidden(c, "只有任务创建者或管理员可以编辑任务信息")
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
	ctx := currentUser(c)
	if !h.canDelete(task, ctx) {
		forbidden(c, "只有任务创建者或管理员可以删除任务")
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
		if ctx.Role != model.RoleAdmin {
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
	ctx := currentUser(c)
	var task model.Task
	if err := h.db.Unscoped().First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if !task.IsDeleted() {
		badRequest(c, "任务未删除，无需恢复")
		return
	}
	if !h.canDelete(&task, ctx) {
		forbidden(c, "只有任务创建者或管理员可以恢复任务")
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
	ctx := currentUser(c)
	if !h.canModify(task, ctx) {
		forbidden(c, "只有创建者、负责人或管理员可以添加进展")
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
