package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

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
	q := h.db.Model(&model.Reminder{}).Where("user_id = ?", ctx.ID)

	if c.Query("unread_only") == "true" {
		q = q.Where("read_at IS NULL AND (sent = ? OR remind_at <= ?)", true, time.Now())
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

	var reminders []model.Reminder
	q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&reminders)

	// 补充任务标题
	items := make([]ReminderOut, 0, len(reminders))
	for i := range reminders {
		var task model.Task
		title := ""
		if err := h.db.Select("title").First(&task, reminders[i].TaskID).Error; err == nil {
			title = task.Title
		}
		reminders[i].TaskTitle = title
		items = append(items, toReminderOut(&reminders[i]))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// UnreadCount GET /reminders/unread-count
func (h *Handler) UnreadCount(c *gin.Context) {
	ctx := currentUser(c)
	var count int64
	h.db.Model(&model.Reminder{}).
		Where("user_id = ? AND read_at IS NULL AND (sent = ? OR remind_at <= ?)", ctx.ID, true, time.Now()).
		Count(&count)
	c.JSON(http.StatusOK, count)
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
	if err := h.db.First(&task, req.TaskID).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	// 仅创建者/负责人/管理员可设置提醒
	canSet := h.canModify(&task, ctx)
	if !canSet {
		forbidden(c, "无权限为该任务设置提醒")
		return
	}
	target := task.AssigneeID
	if target == nil {
		target = task.CreatorID
	}
	if req.RemindType == "" {
		req.RemindType = model.RemindManual
	}
	if req.Message == "" {
		req.Message = "任务「" + task.Title + "」提醒"
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
	id, err := strconv.Atoi(c.Param("id"))
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
		reminder.ReadAt = &now
		h.db.Save(&reminder)
	}
	var task model.Task
	title := ""
	if err := h.db.Select("title").First(&task, reminder.TaskID).Error; err == nil {
		title = task.Title
	}
	reminder.TaskTitle = title
	c.JSON(http.StatusOK, toReminderOut(&reminder))
}
