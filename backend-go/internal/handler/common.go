package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// rawLikeClause 关键词检索子句。SQLite 的 LIKE 默认不区分 ASCII 大小写，
// 配合 ESCAPE 转义，避免用户输入的 % 和 _ 被当作通配符放大结果集。
const rawLikeClause = "title LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\'"

// taskFilters 任务列表的筛选条件集合。
type taskFilters struct {
	status      string
	priority    string
	assigneeID  string
	creatorID   string
	keyword     string
	overdueOnly bool
	mine        bool
	visibility  string // "mine"(默认)/"all"，非管理员只能看自己的任务
}

// parseIDParam 解析路径中的 :id，非正整数一律报错。
func parseIDParam(c *gin.Context) (int, error) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id %q", raw)
	}
	return id, nil
}

// parsePagination 统一解析 page / page_size，越界值收敛到合法区间而非直接拒绝。
func parsePagination(c *gin.Context) (page, pageSize int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultPageSize)))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

// likePattern 把用户输入转换为可安全用于 ESCAPE '\' 的 LIKE 模式串。
func likePattern(keyword string) string {
	trimmed := strings.TrimSpace(keyword)
	if trimmed == "" {
		return ""
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(trimmed)
	return "%" + escaped + "%"
}

// pageResult 统一分页响应结构。
func pageResult[T any](items []T, total int64, page, pageSize int) gin.H {
	return gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}

// exists 做唯一性预检查。
//
// 查询本身出错时必须报错返回，不能把「查不动」当成「没有重复」继续往下写：
// 那会把冲突留到 Create 阶段，变成一句看不出原因的「创建失败」。
func (h *Handler) exists(dest any, query string, args ...any) (bool, error) {
	var count int64
	if err := h.db.Model(dest).Where(query, args...).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// failInternal 记录真实错误、对外只给一句不带细节的提示。
func failInternal(c *gin.Context, where string, err error, msg string) {
	log.Printf("[%s] %v", where, err)
	fail(c, http.StatusInternalServerError, msg)
}

// canViewTask 判断用户是否对任务有查看权限：admin / 创建者 / 被分配人 / 被分享者。
func (h *Handler) canViewTask(c *gin.Context, taskID uint) bool {
	ctx := currentUser(c)
	if ctx == nil {
		return false
	}
	if ctx.Role == model.RoleAdmin {
		return true
	}
	var task model.Task
	if err := h.db.Select("creator_id, assignee_id").First(&task, taskID).Error; err != nil {
		return false
	}
	if (task.CreatorID != nil && *task.CreatorID == ctx.ID) ||
		(task.AssigneeID != nil && *task.AssigneeID == ctx.ID) {
		return true
	}
	var cnt int64
	h.db.Model(&model.TaskShare{}).Where("task_id = ? AND user_id = ?", taskID, ctx.ID).Count(&cnt)
	return cnt > 0
}
