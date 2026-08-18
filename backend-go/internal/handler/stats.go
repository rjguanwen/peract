package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// Overview GET /stats/overview
func (h *Handler) Overview(c *gin.Context) {
	ctx := currentUser(c)
	now := time.Now()

	// 全部任务统计
	var total, todo, inProgress, done int64
	h.db.Model(&model.Task{}).Count(&total)
	h.db.Model(&model.Task{}).Where("status = ?", model.StatusTodo).Count(&todo)
	h.db.Model(&model.Task{}).Where("status = ?", model.StatusInProgress).Count(&inProgress)
	h.db.Model(&model.Task{}).Where("status = ?", model.StatusDone).Count(&done)

	// 逾期
	var overdue int64
	h.db.Model(&model.Task{}).
		Where("status != ? AND due_date IS NOT NULL AND due_date < ?", model.StatusDone, now).
		Count(&overdue)

	// 我的待办与我的逾期
	var minePending, mineOverdue int64
	h.db.Model(&model.Task{}).Where("assignee_id = ? AND status != ?", ctx.ID, model.StatusDone).Count(&minePending)
	h.db.Model(&model.Task{}).
		Where("assignee_id = ? AND status != ? AND due_date IS NOT NULL AND due_date < ?", ctx.ID, model.StatusDone, now).
		Count(&mineOverdue)

	// 优先级分布
	type kv struct {
		Key   string
		Count int64
	}
	var rows []kv
	h.db.Model(&model.Task{}).Select("priority as key, count(*) as count").Group("priority").Scan(&rows)
	priorityDist := map[string]int64{
		model.PriorityLow:    0,
		model.PriorityMedium: 0,
		model.PriorityHigh:   0,
		model.PriorityUrgent: 0,
	}
	for _, r := range rows {
		priorityDist[r.Key] = r.Count
	}

	c.JSON(http.StatusOK, gin.H{
		"total":         total,
		"todo":          todo,
		"in_progress":   inProgress,
		"done":          done,
		"overdue":       overdue,
		"mine_pending":  minePending,
		"mine_overdue":  mineOverdue,
		"priority_dist": priorityDist,
	})
}
