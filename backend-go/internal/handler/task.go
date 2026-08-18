package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/middleware"
	"taskbackend/internal/model"
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

type taskUpdateReq struct {
	Title       *string         `json:"title"`
	Description *string         `json:"description"`
	Priority    *string         `json:"priority"`
	AssigneeID  *uint           `json:"assignee_id"`
	DueDate     *model.DateTime `json:"due_date"`
	Status      *string         `json:"status"`
	Progress    *int            `json:"progress"`
}

type progressCreateReq struct {
	Comment  string  `json:"comment"`
	Progress *int    `json:"progress"`
	Status   *string `json:"status"`
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

// addProgressRecord 追加进展记录并返回
func (h *Handler) addProgressRecord(t *model.Task, user *middleware.UserContext, action, comment string, oldStatus, newStatus *string, progress *int) *model.TaskProgress {
	record := model.TaskProgress{
		TaskID:    t.ID,
		UserID:    &user.ID,
		Action:    action,
		Comment:   comment,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Progress:  progress,
	}
	h.db.Create(&record)
	return &record
}

// reloadTask 重新加载关联，确保返回给前端时名称完整
func (h *Handler) reloadTask(t *model.Task) {
	h.db.Preload("Assignee").Preload("Creator").First(t, t.ID)
}

// notifyAssign 生成站内提醒 + 外部推送
func (h *Handler) notifyAssign(t *model.Task, userID uint, message string) {
	reminder := model.Reminder{
		TaskID:     t.ID,
		UserID:     &userID,
		RemindAt:   time.Now(),
		RemindType: model.RemindAssign,
		Message:    message,
	}
	h.db.Create(&reminder)
	var user model.User
	if err := h.db.First(&user, userID).Error; err == nil && user.Email != "" {
		sendExternalNotification(t.Title, message, user.Email, h.cfg)
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
	if req.AssigneeID != nil {
		var assignee model.User
		if err := h.db.First(&assignee, *req.AssigneeID).Error; err != nil {
			badRequest(c, "负责人不存在")
			return
		}
	}
	ctx := currentUser(c)
	task := model.Task{
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Status:      model.StatusTodo,
		Priority:    req.Priority,
		AssigneeID:  req.AssigneeID,
		CreatorID:   &ctx.ID,
		DueDate:     req.DueDate,
		Progress:    0,
	}
	if err := h.db.Create(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建任务失败")
		return
	}
	h.addProgressRecord(&task, ctx, model.ActionCreated, "任务登记", nil, nil, nil)
	if req.AssigneeID != nil && *req.AssigneeID != ctx.ID {
		h.notifyAssign(&task, *req.AssigneeID, "你有一个新任务「"+task.Title+"」，请及时处理")
	}
	h.reloadTask(&task)
	c.JSON(http.StatusCreated, toTaskOut(&task))
}

// ListTasks GET /tasks
func (h *Handler) ListTasks(c *gin.Context) {
	ctx := currentUser(c)
	q := h.db.Model(&model.Task{}).Preload("Assignee").Preload("Creator")

	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	if priority := c.Query("priority"); priority != "" {
		q = q.Where("priority = ?", priority)
	}
	if assigneeID := c.Query("assignee_id"); assigneeID != "" {
		if id, err := strconv.Atoi(assigneeID); err == nil {
			q = q.Where("assignee_id = ?", id)
		}
	}
	if keyword := c.Query("keyword"); keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("title LIKE ? OR description LIKE ?", like, like)
	}
	if c.Query("overdue_only") == "true" {
		q = q.Where("status != ? AND due_date IS NOT NULL AND due_date < ?", model.StatusDone, time.Now())
	}
	if c.Query("mine") == "true" {
		q = q.Where("assignee_id = ?", ctx.ID)
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var total int64
	q.Count(&total)

	var tasks []model.Task
	q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks)

	items := make([]TaskOut, 0, len(tasks))
	for i := range tasks {
		items = append(items, toTaskOut(&tasks[i]))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetTask GET /tasks/:id
func (h *Handler) GetTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
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
	h.db.Preload("User").Where("task_id = ?", id).Order("created_at").Find(&progresses)

	out := TaskDetailOut{TaskOut: toTaskOut(&task)}
	out.Progresses = make([]TaskProgressOut, 0, len(progresses))
	for i := range progresses {
		out.Progresses = append(out.Progresses, toProgressOut(&progresses[i]))
	}
	c.JSON(http.StatusOK, out)
}

// UpdateTask PATCH /tasks/:id
func (h *Handler) UpdateTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	ctx := currentUser(c)
	var task model.Task
	if err := h.db.Preload("Assignee").Preload("Creator").First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if !h.canModify(&task, ctx) {
		forbidden(c, "只有创建者、负责人或管理员可以修改任务")
		return
	}
	var req taskUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}

	// 编辑基本信息（标题/描述/优先级/负责人/截止时间）仅限管理员或任务创建者
	// 状态与进度流转仍允许负责人操作
	editsBasic := req.Title != nil || req.Description != nil || req.Priority != nil ||
		req.DueDate != nil || req.AssigneeID != nil
	if editsBasic && !h.canEdit(&task, ctx) {
		forbidden(c, "只有任务创建者或管理员可以编辑任务信息")
		return
	}

	// 状态流转
	if req.Status != nil && *req.Status != task.Status {
		if !validStatus(*req.Status) {
			badRequest(c, "状态不合法")
			return
		}
		old := task.Status
		newStatus := *req.Status
		task.Status = newStatus
		if newStatus == model.StatusDone {
			task.Progress = 100
		} else if newStatus == model.StatusTodo && task.Progress == 100 {
			task.Progress = 0
		}
		h.addProgressRecord(&task, ctx, model.ActionStatusChanged, "", &old, &newStatus, nil)
		if task.AssigneeID != nil {
			h.notifyAssign(&task, *task.AssigneeID, "任务「"+task.Title+"」状态变更为："+newStatus)
		}
	}
	// 进度更新
	if req.Progress != nil && *req.Progress != task.Progress {
		p := *req.Progress
		if p < 0 || p > 100 {
			badRequest(c, "进度需在 0-100 之间")
			return
		}
		task.Progress = p
		if p == 100 && task.Status != model.StatusDone {
			task.Status = model.StatusDone
		} else if p < 100 && task.Status == model.StatusDone {
			task.Status = model.StatusInProgress
		}
		h.addProgressRecord(&task, ctx, model.ActionProgressUpdate, "进度更新", nil, nil, &p)
	}
	// 标题/描述/优先级/截止时间
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		task.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.Priority != nil {
		if !validPriority(*req.Priority) {
			badRequest(c, "优先级不合法")
			return
		}
		task.Priority = *req.Priority
	}
	if req.DueDate != nil {
		task.DueDate = req.DueDate
	}
	// 负责人变更
	if req.AssigneeID != nil && (task.AssigneeID == nil || *req.AssigneeID != *task.AssigneeID) {
		newID := *req.AssigneeID
		var assignee model.User
		if err := h.db.First(&assignee, newID).Error; err != nil {
			badRequest(c, "负责人不存在")
			return
		}
		comment := "分配给 " + assignee.FullName
		if assignee.FullName == "" {
			comment = "分配给 " + assignee.Username
		}
		h.addProgressRecord(&task, ctx, model.ActionAssigned, comment, nil, nil, nil)
		task.AssigneeID = &newID
		if newID != ctx.ID {
			h.notifyAssign(&task, newID, "你被分配了任务「"+task.Title+"」，请及时处理")
		}
	}

	task.UpdatedAt = time.Now()
	if err := h.db.Save(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新任务失败")
		return
	}
	h.reloadTask(&task)
	c.JSON(http.StatusOK, toTaskOut(&task))
}

// DeleteTask DELETE /tasks/:id（逻辑删除）
func (h *Handler) DeleteTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	ctx := currentUser(c)
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if !h.canDelete(&task, ctx) {
		forbidden(c, "只有任务创建者或管理员可以删除任务")
		return
	}
	// GORM 软删除：仅写入 deleted_at，不物理删除
	if err := h.db.Delete(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "删除任务失败")
		return
	}
	c.Status(http.StatusNoContent)
}

// ListDeletedTasks GET /tasks/deleted 查看已删除任务（管理员或任务创建者）
func (h *Handler) ListDeletedTasks(c *gin.Context) {
	ctx := currentUser(c)
	q := h.db.Unscoped().Model(&model.Task{}).Preload("Assignee").Preload("Creator")
	if ctx.Role != model.RoleAdmin {
		q = q.Where("creator_id = ?", ctx.ID)
	}

	if priority := c.Query("priority"); priority != "" {
		q = q.Where("priority = ?", priority)
	}
	if keyword := c.Query("keyword"); keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("title LIKE ? OR description LIKE ?", like, like)
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var total int64
	q.Count(&total)

	var tasks []model.Task
	q.Order("deleted_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks)

	items := make([]TaskOut, 0, len(tasks))
	for i := range tasks {
		items = append(items, toTaskOut(&tasks[i]))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// RestoreTask POST /tasks/:id/restore 恢复已删除任务
func (h *Handler) RestoreTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
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
	// 清除逻辑删除标记
	if err := h.db.Unscoped().Model(&task).Update("deleted_at", nil).Error; err != nil {
		fail(c, http.StatusInternalServerError, "恢复任务失败")
		return
	}
	task.DeletedAt = gorm.DeletedAt{}
	task.UpdatedAt = time.Now()
	h.db.Save(&task)
	h.reloadTask(&task)
	c.JSON(http.StatusOK, toTaskOut(&task))
}

// AddProgress POST /tasks/:id/progress
func (h *Handler) AddProgress(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	ctx := currentUser(c)
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if !h.canModify(&task, ctx) {
		forbidden(c, "只有创建者、负责人或管理员可以添加进展")
		return
	}
	var req progressCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}

	var record *model.TaskProgress
	switch {
	case req.Status != nil && *req.Status != "":
		if !validStatus(*req.Status) {
			badRequest(c, "状态不合法")
			return
		}
		old := task.Status
		newStatus := *req.Status
		task.Status = newStatus
		if newStatus == model.StatusDone {
			task.Progress = 100
		}
		record = h.addProgressRecord(&task, ctx, model.ActionStatusChanged, req.Comment, &old, &newStatus, nil)
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
		record = h.addProgressRecord(&task, ctx, model.ActionProgressUpdate, req.Comment, nil, nil, &p)
	default:
		record = h.addProgressRecord(&task, ctx, model.ActionComment, req.Comment, nil, nil, nil)
	}

	task.UpdatedAt = time.Now()
	if err := h.db.Save(&task).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新任务失败")
		return
	}
	h.db.Preload("User").First(record, record.ID)
	c.JSON(http.StatusOK, toProgressOut(record))
}
