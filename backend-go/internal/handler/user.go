package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

// ListUsers GET /users
//
// 返回裸数组是本接口已有的前端契约（任务表单的负责人下拉直接当数组用），保持不变。
//
// 每一行的 permissions/super_admin 都是空的, 而且是**故意**空的: 那是"当前会话"的快照,
// 挂在别人身上没有意义。要看某个人能做什么, 看的是平台侧的角色授权 —— 那个界面在
// OneLink 上。在应用侧再渲染一遍等于给同一个真值造第二份展示, 而两份展示迟早会不一致。
func (h *Handler) ListUsers(c *gin.Context) {
	var users []model.User
	if err := h.db.Order("id").Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询用户失败")
		return
	}
	out := make([]UserOut, 0, len(users))
	for i := range users {
		out = append(out, toUserOut(&users[i], nil))
	}
	c.JSON(http.StatusOK, out)
}

// UpdateUser PATCH /users/:id
//
// 只改**本地档案**里应用自己说了算的那几列: 姓名与个性签名之外的业务开关(能不能被指派)。
//
// 账号、邮箱、口令、角色都不在这里 —— 它们在平台上, 而应用侧改它们只会造出一份与平台
// 不一致的档案: 下一次这个人从门户进来, 平台那份会把它覆盖回去, 表现为"管理员改了他的
// 账号, 过一会儿又变回来了"。把不可改的字段从请求结构里去掉, 比接住它再报错更省事,
// 也更不容易在将来被人"顺手打开"。
func (h *Handler) UpdateUser(c *gin.Context) {
	ctx := currentUser(c)
	id, err := parseIDParam(c)
	if err != nil {
		badRequest(c, "用户 ID 不合法")
		return
	}
	var req struct {
		FullName  *string `json:"full_name"`
		Signature *string `json:"signature"`
		IsActive  *bool   `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	var user model.User
	if err := h.db.First(&user, id).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}

	// 不能停用自己。这不是"谨慎", 而是自锁: 本地这一列一关, 这个人会在所有需要指派
	// 的地方消失, 而他手里唯一能改回来的入口正是这里。
	if req.IsActive != nil && !*req.IsActive && user.ID == ctx.ID {
		forbidden(c, "不能停用自己")
		return
	}

	updates := map[string]any{}
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		if len([]rune(name)) > maxFullNameLen {
			badRequest(c, "姓名最多 128 字")
			return
		}
		updates["full_name"] = name
	}
	if req.Signature != nil {
		sig := strings.TrimSpace(*req.Signature)
		if len([]rune(sig)) > 80 {
			badRequest(c, "个性签名最多 80 字")
			return
		}
		updates["signature"] = sig
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if len(updates) == 0 {
		badRequest(c, "没有需要保存的内容")
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新用户失败")
		return
	}
	if err := h.db.First(&user, user.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新用户失败")
		return
	}
	c.JSON(http.StatusOK, toUserOut(&user, nil))
}
