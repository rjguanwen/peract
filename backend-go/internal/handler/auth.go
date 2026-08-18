package handler

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type UserOut struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type TokenOut struct {
	AccessToken string  `json:"access_token"`
	TokenType   string  `json:"token_type"`
	User        UserOut `json:"user"`
}

type registerReq struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	FullName string `json:"full_name"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`
}

func toUserOut(u *model.User) UserOut {
	return UserOut{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		FullName:  u.FullName,
		Role:      u.Role,
		IsActive:  u.IsActive,
		CreatedAt: u.CreatedAt,
	}
}

// Login POST /auth/login (OAuth2 form)
func (h *Handler) Login(c *gin.Context) {
	username := strings.TrimSpace(c.PostForm("username"))
	password := c.PostForm("password")
	if username == "" || password == "" {
		badRequest(c, "用户名或密码错误")
		return
	}
	var user model.User
	if err := h.db.Where("username = ?", username).First(&user).Error; err != nil {
		badRequest(c, "用户名或密码错误")
		return
	}
	if !user.CheckPassword(password) {
		badRequest(c, "用户名或密码错误")
		return
	}
	if !user.IsActive {
		forbidden(c, "账号已停用，请联系管理员")
		return
	}
	token, err := h.auth.CreateToken(user.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成令牌失败")
		return
	}
	c.JSON(http.StatusOK, TokenOut{
		AccessToken: token,
		TokenType:   "bearer",
		User:        toUserOut(&user),
	})
}

// Register POST /auth/register
func (h *Handler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Role = strings.ToLower(req.Role)
	if !usernameRe.MatchString(req.Username) {
		badRequest(c, "用户名只能包含字母、数字、下划线、点、横线")
		return
	}
	if req.Role != "" && req.Role != model.RoleUser && req.Role != model.RoleAdmin {
		badRequest(c, "角色不合法")
		return
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
	role := model.RoleUser
	if req.Role == model.RoleAdmin {
		role = model.RoleAdmin
	}
	user := model.NewUser(req.Username, req.Email, req.Password, role, req.FullName)
	if err := h.db.Create(user).Error; err != nil {
		fail(c, http.StatusInternalServerError, "创建用户失败")
		return
	}
	c.JSON(http.StatusCreated, toUserOut(user))
}

// Me GET /auth/me
func (h *Handler) Me(c *gin.Context) {
	ctx := currentUser(c)
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		notFound(c, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, toUserOut(&user))
}
