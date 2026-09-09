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
var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

type UserOut struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"is_active"`
	AvatarURL *string   `json:"avatar_url"`
	Signature string    `json:"signature"`
	CreatedAt time.Time `json:"created_at"`
}

type TokenOut struct {
	AccessToken string  `json:"access_token"`
	TokenType   string  `json:"token_type"`
	User        UserOut `json:"user"`
}

type registerReq struct {
	Username    string `json:"username" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	FullName    string `json:"full_name"`
	Password    string `json:"password" binding:"required,min=6"`
	InviteToken string `json:"inviteToken"`
}

func toUserOut(u *model.User) UserOut {
	return UserOut{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		FullName:  u.FullName,
		Role:      u.Role,
		IsActive:  u.IsActive,
		AvatarURL: u.AvatarURL,
		Signature: u.Signature,
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

// Register POST /auth/register (JSON: username/email/password/full_name，可选 inviteToken)
// 系统关闭公开注册后，仅持有有效邀请链接（inviteToken）的用户可以注册。
// 注册的用户一律为普通用户，防止开放注册提权为管理员。
func (h *Handler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "参数不合法："+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.InviteToken = strings.TrimSpace(req.InviteToken)

	if req.Username == "" || req.Email == "" || req.Password == "" {
		badRequest(c, "用户名、邮箱和密码均为必填")
		return
	}
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

	// 若携带邀请令牌，先校验其有效性
	var invite *model.Invitation
	if req.InviteToken != "" {
		var iv model.Invitation
		if err := h.db.Where("token = ?", req.InviteToken).First(&iv).Error; err != nil {
			badRequest(c, "邀请链接无效，请联系管理员")
			return
		}
		invite = &iv
	}

	// 无邀请时，注册开关关闭则拒绝
	if invite == nil && h.getSetting(model.SettingRegistrationEnabled, "true") != "true" {
		forbidden(c, "系统已暂停新用户注册，请联系管理员邀请你加入")
		return
	}

	// 校验邀请状态与邮箱一致性
	if invite != nil {
		if invitationExpired(invite.ExpiresAt) {
			badRequest(c, "邀请链接已过期，请联系管理员重新邀请")
			return
		}
		switch invite.Status {
		case model.InviteStatusRegistered:
			badRequest(c, "该邀请已被使用，请直接登录")
			return
		case model.InviteStatusRevoked:
			badRequest(c, "该邀请已被撤销，请联系管理员")
			return
		}
		if invite.Email != req.Email {
			badRequest(c, "该邀请链接仅限「"+invite.Email+"」邮箱注册")
			return
		}
	}

	var count int64
	h.db.Model(&model.User{}).Where("email = ?", req.Email).Count(&count)
	if count > 0 {
		// 该邮箱已注册：如有待接受的邀请则同步标记为已使用，保持邀请列表状态准确
		h.markInviteUsed(req.Email)
		badRequest(c, "该邮箱已注册")
		return
	}
	h.db.Model(&model.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		badRequest(c, "用户名已存在")
		return
	}

	now := time.Now()
	user := model.NewUser(req.Username, req.Email, req.Password, model.RoleUser, req.FullName)
	user.CreatedAt = now
	if err := h.db.Create(user).Error; err != nil {
		fail(c, http.StatusInternalServerError, "注册失败，请稍后重试")
		return
	}
	// 注册成功：将该邮箱的待接受邀请标记为已使用
	h.markInviteUsed(req.Email)
	token, err := h.auth.CreateToken(user.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, "生成凭证失败")
		return
	}
	c.JSON(http.StatusCreated, TokenOut{
		AccessToken: token,
		TokenType:   "bearer",
		User:        toUserOut(user),
	})
}

// Logout POST /auth/logout 退出登录（无状态 JWT，前端清除本地 token 即可）
func (h *Handler) Logout(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true})
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
