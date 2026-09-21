package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/model"
)

// Overview GET /stats/overview
//
// 看板每次刷新都要跑一遍这里。原实现是 8 条独立 COUNT，SQLite 单写锁下
// 这些查询会串行排队；改为 3 条条件聚合（CASE WHEN）后往返次数降到三分之一，
// 且同一批数据在一次扫描内得出，避免了并发写入时各计数彼此不一致。
// 权限范围：没有 PermTaskListAll 的人只看 creator_id=me OR assignee_id=me OR 被分享的任务。
func (h *Handler) Overview(c *gin.Context) {
	ctx := currentUser(c)
	canSeeAll := can(c, PermTaskListAll)
	now := time.Now()

	// 权限范围子查询（用于 WHERE 子句注入）
	visScope := func(q *gorm.DB) *gorm.DB {
		if canSeeAll {
			return q
		}
		return q.Where(
			"(creator_id = ? OR assignee_id = ? OR id IN (SELECT task_id FROM task_shares WHERE user_id = ?))",
			ctx.ID, ctx.ID, ctx.ID)
	}

	var global struct {
		Total      int64
		Todo       int64
		InProgress int64
		Done       int64
		Overdue    int64
	}
	if err := visScope(h.db.Model(&model.Task{})).
		Select(`COUNT(*) AS total,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS todo,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS in_progress,
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS done,
			SUM(CASE WHEN status <> ? AND due_date IS NOT NULL AND due_date < ? THEN 1 ELSE 0 END) AS overdue`,
			model.StatusTodo, model.StatusInProgress, model.StatusDone, model.StatusDone, now).
		Scan(&global).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计失败")
		return
	}

	// 我的待办：未完成任务数 + 其中逾期数，同样一次扫描得出
	var mine struct {
		Pending int64
		Overdue int64
	}
	if err := visScope(h.db.Model(&model.Task{})).
		Select(`COUNT(*) AS pending,
			SUM(CASE WHEN due_date IS NOT NULL AND due_date < ? THEN 1 ELSE 0 END) AS overdue`, now).
		Where("assignee_id = ? AND status <> ?", ctx.ID, model.StatusDone).
		Scan(&mine).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计失败")
		return
	}

	type kv struct {
		Key   string
		Count int64
	}
	var rows []kv
	if err := visScope(h.db.Model(&model.Task{})).
		Select("priority AS key, COUNT(*) AS count").
		Group("priority").Scan(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, "统计失败")
		return
	}
	// 先铺满四档再填值，保证前端拿到的 priority_dist 永远是完整结构
	priorityDist := map[string]int64{
		model.PriorityLow:    0,
		model.PriorityMedium: 0,
		model.PriorityHigh:   0,
		model.PriorityUrgent: 0,
	}
	for _, r := range rows {
		if _, ok := priorityDist[r.Key]; ok {
			priorityDist[r.Key] = r.Count
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":         global.Total,
		"todo":          global.Todo,
		"in_progress":   global.InProgress,
		"done":          global.Done,
		"overdue":       global.Overdue,
		"mine_pending":  mine.Pending,
		"mine_overdue":  mine.Overdue,
		"priority_dist": priorityDist,
	})
}
