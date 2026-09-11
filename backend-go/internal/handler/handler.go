package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/middleware"
	"taskbackend/internal/service"
)

type Handler struct {
	db        *gorm.DB
	cfg       *config.Config
	auth      *middleware.Auth
	notify    *service.Notifier
	uploadDir string

	// loginLimiter 按「IP + 用户名」统计失败次数，抵御口令爆破
	loginLimiter *middleware.RateLimiter
	// notifyLimiter 按「IP + 邮箱」限制找回密码等敏感接口的调用频次
	notifyLimiter *middleware.RateLimiter
}

func New(db *gorm.DB, cfg *config.Config, auth *middleware.Auth, notify *service.Notifier) *Handler {
	dir, err := filepath.Abs(cfg.UploadDir)
	if err != nil {
		dir = cfg.UploadDir
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		// 上传目录不可用只影响头像，不阻断启动，但必须显式暴露原因
		gin.DefaultWriter.Write([]byte("[warn] 创建上传目录失败，头像上传将不可用: " + err.Error() + "\n"))
	}
	return &Handler{
		db:            db,
		cfg:           cfg,
		auth:          auth,
		notify:        notify,
		uploadDir:     dir,
		loginLimiter:  middleware.NewRateLimiter(cfg.LoginFailLimit, cfg.LoginFailWindow),
		notifyLimiter: middleware.NewRateLimiter(cfg.NotifyLimit, cfg.NotifyWindow),
	}
}

// Close 回收后台限流协程。
func (h *Handler) Close() {
	h.loginLimiter.Stop()
	h.notifyLimiter.Stop()
}

// currentUser 从上下文获取当前登录用户；中间件保证受保护路由上一定非空。
func currentUser(c *gin.Context) *middleware.UserContext {
	v, ok := c.Get(middleware.UserContextKey)
	if !ok {
		return nil
	}
	u, ok := v.(*middleware.UserContext)
	return u
}

// clientID 取真实来源标识。gin 的 ClientIP 只在配置了受信代理时才采信 X-Forwarded-For。
func clientID(c *gin.Context) string {
	return c.ClientIP()
}

// throttle 消费一次限流配额，超限则写入 429 并返回 false。
func throttle(c *gin.Context, lim *middleware.RateLimiter, key string) bool {
	if lim.Allow(key) {
		return true
	}
	retry := int(lim.RetryAfter(key).Seconds())
	if retry > 0 {
		c.Header("Retry-After", strconv.Itoa(retry))
	}
	fail(c, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
	return false
}

// RegisterRoutes 注册全部路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api/v1", middleware.BodySizeLimit(h.cfg.MaxBodyBytes))

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
	user.POST("/reminders/read-all", h.MarkAllRead)
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
