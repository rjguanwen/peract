package handler

import (
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/middleware"
)

type Handler struct {
	db        *gorm.DB
	cfg       *config.Config
	auth      *middleware.Auth
	uploadDir string
}

func New(db *gorm.DB, cfg *config.Config, auth *middleware.Auth) *Handler {
	dir, err := filepath.Abs(cfg.UploadDir)
	if err != nil {
		dir = cfg.UploadDir
	}
	_ = os.MkdirAll(dir, 0o755)
	return &Handler{db: db, cfg: cfg, auth: auth, uploadDir: dir}
}

// currentUser 从上下文获取当前登录用户
func currentUser(c *gin.Context) *middleware.UserContext {
	// 由 middleware 写入
	v, ok := c.Get("user")
	if !ok {
		return nil
	}
	return v.(*middleware.UserContext)
}

// RegisterRoutes 注册全部路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api/v1")

	// 公开接口
	api.POST("/auth/login", h.Login)
	api.POST("/auth/register", h.Register)
	api.POST("/auth/logout", h.Logout)
	api.GET("/auth/forgot", h.GetPasswordRecovery)
	api.POST("/auth/forgot/reset", h.ResetPassword)
	api.POST("/auth/forgot/send", h.SendForgotEmail)
	api.POST("/auth/reset", h.ResetPasswordByToken)
	api.GET("/auth/invite/info", h.InviteInfo)

	// 需要登录
	user := api.Group("", h.auth.RequireUser())
	user.GET("/auth/me", h.Me)
	user.PUT("/auth/password", h.ChangePassword)
	user.GET("/auth/security", h.GetSecurityInfo)
	user.PUT("/auth/security", h.SetSecurityInfo)
	user.PUT("/profile", h.UpdateProfile)
	user.PUT("/profile/avatar", h.UploadAvatar)
	user.GET("/users", h.ListUsers)
	user.GET("/tasks", h.ListTasks)
	user.GET("/tasks/deleted", h.ListDeletedTasks) // 需在 /tasks/:id 之前注册
	user.GET("/tasks/:id", h.GetTask)
	user.POST("/tasks", h.CreateTask)
	user.PATCH("/tasks/:id", h.UpdateTask)
	user.DELETE("/tasks/:id", h.DeleteTask)
	user.POST("/tasks/:id/restore", h.RestoreTask)
	user.POST("/tasks/:id/progress", h.AddProgress)
	user.GET("/reminders", h.ListReminders)
	user.GET("/reminders/unread-count", h.UnreadCount)
	user.POST("/reminders", h.CreateReminder)
	user.POST("/reminders/:id/read", h.MarkRead)
	user.GET("/stats/overview", h.Overview)

	// 需要管理员
	admin := api.Group("", h.auth.RequireAdmin())
	admin.POST("/users", h.CreateUser)
	admin.PATCH("/users/:id", h.UpdateUser)
	admin.GET("/admin/settings", h.GetSettings)
	admin.PUT("/admin/settings/registration", h.SetRegistrationEnabled)
	admin.POST("/admin/invites", h.CreateInvites)
	admin.GET("/admin/invites", h.ListInvites)
	admin.POST("/admin/invites/:id/revoke", h.RevokeInvite)
}
