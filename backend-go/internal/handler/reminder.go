package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/model"
)

type ReminderOut struct {
	ID         uint           `json:"id"`
	TaskID     uint           `json:"task_id"`
	TaskTitle  string         `json:"task_title"`
	UserID     *uint          `json:"user_id"`
	RemindAt   model.DateTime `json:"remind_at"`
	RemindType string         `json:"remind_type"`
	Message    string         `json:"message"`
	Sent       bool           `json:"sent"`
	ReadAt     *time.Time     `json:"read_at"`
	CreatedAt  time.Time      `json:"created_at"`
}

type reminderCreateReq struct {
	TaskID     uint           `json:"task_id" binding:"required"`
	RemindAt   model.DateTime `json:"remind_at" binding:"required"`
	Message    string         `json:"message"`
	RemindType string         `json:"remind_type"`
}

// unreadableClause 只统计"已可见但未读"的提醒：已推送或已到点。
const unreadableClause = "read_at IS NULL AND (sent = ? OR remind_at <= ?)"

func toReminderOut(r *model.Reminder) ReminderOut {
	return ReminderOut{
		ID:         r.ID,
		TaskID:     r.TaskID,
		TaskTitle:  r.TaskTitle,
		UserID:     r.UserID,
		RemindAt:   *model.NewDateTime(r.RemindAt),
		RemindType: r.RemindType,
		Message:    r.Message,
		Sent:       r.Sent,
		ReadAt:     r.ReadAt,
		CreatedAt:  r.CreatedAt,
	}
}

// ListReminders GET /reminders
func (h *Handler) ListReminders(c *gin.Context) {
	ctx := currentUser(c)
	now := time.Now()

	build := func() *gorm.DB {
		q := h.db.Model(&model.Reminder{}).Where("user_id = ?", ctx.ID)
		if c.Query("unread_only") == "true" {
			q = q.Where(unreadableClause, true, now)
		}
		return q
	}

	page, pageSize := parsePagination(c)

	var total int64
	if err := build().Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询提醒失败")
		return
	}

	var reminders []model.Reminder
	if err := build().Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&reminders).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询提醒失败")
		return
	}

	// 一次批量取回本页涉及的任务标题，替代原先每条提醒一次查询的 N+1 写法
	titles := h.taskTitles(reminders)
	items := make([]ReminderOut, 0, len(reminders))
	for i := range reminders {
		reminders[i].TaskTitle = titles[reminders[i].TaskID]
		items = append(items, toReminderOut(&reminders[i]))
	}
	c.JSON(http.StatusOK, pageResult(items, total, page, pageSize))
}

// taskTitles 批量查询提醒关联任务的标题，软删除的任务也能取到（回收站里的任务仍需展示来源）。
func (h *Handler) taskTitles(reminders []model.Reminder) map[uint]string {
	ids := make([]uint, 0, len(reminders))
	seen := make(map[uint]bool, len(reminders))
	for i := range reminders {
		if !seen[reminders[i].TaskID] {
			seen[reminders[i].TaskID] = true
			ids = append(ids, reminders[i].TaskID)
		}
	}
	titles := make(map[uint]string, len(ids))
	if len(ids) == 0 {
		return titles
	}
	var rows []struct {
		ID    uint
		Title string
	}
	if err := h.db.Unscoped().Model(&model.Task{}).Select("id, title").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return titles
	}
	for _, row := range rows {
		titles[row.ID] = row.Title
	}
	return titles
}

// UnreadCount GET /reminders/unread-count
func (h *Handler) UnreadCount(c *gin.Context) {
	ctx := currentUser(c)
	var count int64
	if err := h.db.Model(&model.Reminder{}).
		Where("user_id = ?", ctx.ID).
		Where(unreadableClause, true, time.Now()).
		Count(&count).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询未读数失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// CreateReminder POST /reminders
func (h *Handler) CreateReminder(c *gin.Context) {
	ctx := currentUser(c)
	var req reminderCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	var task model.Task
	if err := h.db.Select("id, title, assignee_id, creator_id").First(&task, req.TaskID).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	// 仅创建者/负责人/管理员可设置提醒
	if !h.canModify(&task, ctx) {
		forbidden(c, "无权限为该任务设置提醒")
		return
	}
	// 提醒不得投递给无关第三方：请求里不再接受 user_id，目标固定为负责人（或创建者）
	target := task.AssigneeID
	if target == nil {
		target = task.CreatorID
	}
	if req.RemindType == "" {
		req.RemindType = model.RemindManual
	}
	if req.RemindType != model.RemindManual && req.RemindType != model.RemindOverdue {
		req.RemindType = model.RemindManual
	}
	if req.Message == "" {
		req.Message = "任务「" + task.Title + "」提醒"
	}
	if req.RemindAt.Before(time.Now().Add(-time.Hour)) {
		badRequest(c, "提醒时间不能是很久以前的时间")
		return
	}
	reminder := model.Reminder{
		TaskID:     task.ID,
		UserID:     target,
		RemindAt:   req.RemindAt.Time,
		RemindType: req.RemindType,
		Message:    req.Message,
	}
	if err := h.db.Create(&reminder).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建提醒失败")
		return
	}
	reminder.TaskTitle = task.Title
	c.JSON(http.StatusCreated, toReminderOut(&reminder))
}

// MarkRead POST /reminders/:id/read
func (h *Handler) MarkRead(c *gin.Context) {
	ctx := currentUser(c)
	id, err := parseIDParam(c)
	if err != nil {
		badRequest(c, "提醒 ID 不合法")
		return
	}
	var reminder model.Reminder
	if err := h.db.First(&reminder, id).Error; err != nil {
		notFound(c, "提醒不存在")
		return
	}
	if (reminder.UserID == nil || *reminder.UserID != ctx.ID) && ctx.Role != model.RoleAdmin {
		forbidden(c, "无权限操作该提醒")
		return
	}
	if reminder.ReadAt == nil {
		now := time.Now()
		// 只更新单列，避免 Save 把整行（含可能已被调度器改写的 sent/sent_at）覆盖回旧值
		if err := h.db.Model(&model.Reminder{}).Where("id = ?", id).
			UpdateColumn("read_at", now).Error; err != nil {
			fail(c, http.StatusInternalServerError, "标记已读失败")
			return
		}
		reminder.ReadAt = &now
	}
	var task model.Task
	title := ""
	if err := h.db.Unscoped().Select("title").First(&task, reminder.TaskID).Error; err == nil {
		title = task.Title
	}
	reminder.TaskTitle = title
	c.JSON(http.StatusOK, toReminderOut(&reminder))
}

// MarkAllRead POST /reminders/read-all 一次性把当前用户所有可见提醒标记为已读，
// 替代前端 Promise.all 逐条请求的写法。
func (h *Handler) MarkAllRead(c *gin.Context) {
	ctx := currentUser(c)
	now := time.Now()
	res := h.db.Model(&model.Reminder{}).
		Where("user_id = ?", ctx.ID).
		Where("read_at IS NULL").
		UpdateColumn("read_at", now)
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, "标记已读失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": res.RowsAffected})
}
