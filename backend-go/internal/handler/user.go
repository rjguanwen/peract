package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

type userCreateReq struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	FullName string `json:"full_name"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`
}

type userUpdateReq struct {
	FullName *string `json:"full_name"`
	Email    *string `json:"email" binding:"omitempty,email"`
	Role     *string `json:"role"`
	Password *string `json:"password"`
	IsActive *bool   `json:"is_active"`
}

// ListUsers GET /users
func (h *Handler) ListUsers(c *gin.Context) {
	var users []model.User
	if err := h.db.Order("id").Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, "查询用户失败")
		return
	}
	out := make([]UserOut, 0, len(users))
	for i := range users {
		out = append(out, toUserOut(&users[i]))
	}
	c.JSON(http.StatusOK, out)
}

// CreateUser POST /users (admin)
func (h *Handler) CreateUser(c *gin.Context) {
	var req userCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	if !usernameRe.MatchString(req.Username) {
		badRequest(c, "用户名只能包含字母、数字、下划线、点、横线")
		return
	}
	if !emailRe.MatchString(req.Email) {
		badRequest(c, "邮箱格式不正确")
		return
	}
	if len(req.Password) < 6 {
		badRequest(c, "密码至少需要 6 个字符")
		return
	}
	role := strings.ToLower(req.Role)
	if role != "" && role != model.RoleUser && role != model.RoleAdmin {
		badRequest(c, "角色不合法")
		return
	}
	if role == "" {
		role = model.RoleUser
	}
	var count int64
	h.db.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		badRequest(c, "用户名已存在")
		return
	}
	h.db.Model(&model.User{}).Where("email = ?", req.Email).Count(&count)
	if count > 0 {
		badRequest(c, "邮箱已被注册")
		return
	}
	user := model.NewUser(req.Username, req.Email, req.Password, role, req.FullName)
	if err := h.db.Create(user).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建用户失败")
		return
	}
	c.JSON(http.StatusCreated, toUserOut(user))
}

// UpdateUser PATCH /users/:id (admin)
// 保护规则：不能修改管理员账号的角色或启用状态；不能修改自己的角色或启用状态。
func (h *Handler) UpdateUser(c *gin.Context) {
	ctx := currentUser(c)
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		badRequest(c, "用户 ID 不合法")
		return
	}
	var req userUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	var user model.User
	if err := h.db.First(&user, id).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}

	// 是否在改动「角色/启用状态」这类权限敏感字段
	changingPrivilege := false
	if req.Role != nil && strings.ToLower(*req.Role) != user.Role {
		changingPrivilege = true
	}
	if req.IsActive != nil && *req.IsActive != user.IsActive {
		changingPrivilege = true
	}
	if changingPrivilege {
		if user.Role == model.RoleAdmin {
			forbidden(c, "不能修改管理员账号的角色或启用状态")
			return
		}
		if user.ID == ctx.ID {
			forbidden(c, "不能修改自己的角色或启用状态")
			return
		}
	}

	if req.FullName != nil {
		user.FullName = strings.TrimSpace(*req.FullName)
	}
	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if !emailRe.MatchString(email) {
			badRequest(c, "邮箱格式不正确")
			return
		}
		var count int64
		h.db.Model(&model.User{}).Where("email = ? AND id != ?", email, user.ID).Count(&count)
		if count > 0 {
			badRequest(c, "邮箱已被注册")
			return
		}
		user.Email = email
	}
	if req.Role != nil {
		role := strings.ToLower(*req.Role)
		if role != model.RoleUser && role != model.RoleAdmin {
			badRequest(c, "角色不合法")
			return
		}
		user.Role = role
	}
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 6 {
			badRequest(c, "密码至少需要 6 个字符")
			return
		}
		if err := user.SetPassword(*req.Password); err != nil {
			fail(c, http.StatusInternalServerError, "设置密码失败")
			return
		}
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}
	if err := h.db.Save(&user).Error; err != nil {
		fail(c, http.StatusInternalServerError, "更新用户失败")
		return
	}
	c.JSON(http.StatusOK, toUserOut(&user))
}
