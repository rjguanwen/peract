package handler

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/middleware"
)

type Handler struct {
	db   *gorm.DB
	cfg  *config.Config
	auth *middleware.Auth
}

func New(db *gorm.DB, cfg *config.Config, auth *middleware.Auth) *Handler {
	return &Handler{db: db, cfg: cfg, auth: auth}
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

	// 需要登录
	user := api.Group("", h.auth.RequireUser())
	user.GET("/auth/me", h.Me)
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
}
