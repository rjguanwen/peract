package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// shareTaskReq 分享任务请求
type shareTaskReq struct {
	TaskID       uint   `json:"task_id" binding:"required"`
	UserID       uint   `json:"user_id" binding:"required"`
	InviteeEmail string `json:"invitee_email" binding:"required,email"` // 通过邮箱查找用户
}

// ListShares GET /tasks/:id/shares — 列出任务的所有分享对象（仅创建者可查）
func (h *Handler) ListShares(c *gin.Context) {
	ctx := currentUser(c)
	taskID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	// 权限：仅任务创建者可以管理分享
	var task model.Task
	if err := h.db.Select("creator_id").First(&task, taskID).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if (task.CreatorID == nil || *task.CreatorID != ctx.ID) && !can(c, PermTaskListAll) {
		forbidden(c, "仅任务创建者可查看分享列表")
		return
	}
	var shares []model.TaskShare
	h.db.Where("task_id = ?", taskID).Order("created_at DESC").Find(&shares)
	// 补全用户信息
	var out []gin.H
	for _, s := range shares {
		var user model.User
		if err := h.db.Select("id, username, full_name, email").First(&user, s.UserID).Error; err != nil {
			continue
		}
		out = append(out, gin.H{
			"id":        s.ID,
			"task_id":   s.TaskID,
			"user_id":   s.UserID,
			"username":  user.Username,
			"full_name": user.FullName,
			"email":     user.Email,
			"shared_at": s.CreatedAt,
		})
	}
	if out == nil {
		out = []gin.H{}
	}
	c.JSON(http.StatusOK, out)
}

// AddShare POST /tasks/:id/shares — 分享任务给其他用户（仅创建者可操作）
func (h *Handler) AddShare(c *gin.Context) {
	ctx := currentUser(c)
	taskID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "任务 ID 不合法")
		return
	}
	var req struct {
		InviteeEmail string `json:"invitee_email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "请提供有效的被分享者邮箱")
		return
	}
	req.InviteeEmail = strings.ToLower(strings.TrimSpace(req.InviteeEmail))

	// 权限：仅任务创建者可分享
	var task model.Task
	if err := h.db.Select("id, creator_id, title").First(&task, taskID).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if (task.CreatorID == nil || *task.CreatorID != ctx.ID) && !can(c, PermTaskListAll) {
		forbidden(c, "仅任务创建者可分享任务")
		return
	}

	// 按邮箱查找被分享用户
	var invitee model.User
	if err := h.db.Where("email = ?", req.InviteeEmail).First(&invitee).Error; err != nil {
		notFound(c, "未找到该邮箱对应的用户")
		return
	}

	// 不能分享给自己
	if invitee.ID == ctx.ID {
		badRequest(c, "不能将任务分享给自己")
		return
	}

	// 这里原来有一条"不能分享给管理员"。它没了, 而且**不是遗漏**:
	// 被分享人是不是已经能看见全部任务, 取决于平台侧的角色授权(sys_user_role),
	// 应用侧拿不到 —— 本地档案里没有任何权限信息。而一条多余的分享行没有危害:
	// 它只是让这个人多一个可见任务, 而他本来就看得见。
	//
	// 真要判, 就得为每一次分享回平台查一次被分享人的权限。那既多一次网络往返,
	// 又把"谁能看见什么"这个判断分散到了两处 —— 而分散的那两处迟早会不一致。

	share := model.TaskShare{
		TaskID:    uint(taskID),
		UserID:    invitee.ID,
		GrantedBy: ctx.ID,
	}
	if err := h.db.Create(&share).Error; err != nil {
		// 唯一索引冲突（重复分享）
		if strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "Duplicate") {
			badRequest(c, "该用户已在分享列表中")
			return
		}
		fail(c, http.StatusInternalServerError, "保存分享记录失败")
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":        share.ID,
		"task_id":   share.TaskID,
		"user_id":   share.UserID,
		"username":  invitee.Username,
		"full_name": invitee.FullName,
		"email":     invitee.Email,
		"shared_at": share.CreatedAt,
	})
}

// RevokeShare DELETE /tasks/:id/shares/:shareId — 撤销分享（仅创建者可操作）
func (h *Handler) RevokeShare(c *gin.Context) {
	ctx := currentUser(c)
	taskID, _ := strconv.Atoi(c.Param("id"))
	shareID, _ := strconv.Atoi(c.Param("shareId"))

	// 权限：仅任务创建者可撤销分享
	var task model.Task
	if err := h.db.Select("creator_id").First(&task, taskID).Error; err != nil {
		notFound(c, "任务不存在")
		return
	}
	if (task.CreatorID == nil || *task.CreatorID != ctx.ID) && !can(c, PermTaskListAll) {
		forbidden(c, "仅任务创建者可撤销分享")
		return
	}

	result := h.db.Where("id = ? AND task_id = ?", shareID, taskID).Delete(&model.TaskShare{})
	if result.RowsAffected == 0 {
		notFound(c, "分享记录不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
