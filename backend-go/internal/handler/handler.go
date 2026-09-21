package handler

import (
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	onelinksdk "github.com/onelink/platform/sdk/go/onelink"

	"taskbackend/internal/config"
	"taskbackend/internal/middleware"
	"taskbackend/internal/onelink"
	"taskbackend/internal/service"
)

// 躬行的权限点。
//
// 它们全部登记在 OneLink 上, 归属是 task-system 这个应用(见 deploy/perms.manifest.json
// 与 deploy/seed-perms.sql)。前缀必须是应用编码 —— `platform:` 是平台保留前缀, 业务应用
// 用它会被 service 层直接拒(见 service.Perm 的保留前缀校验)。
//
// 命名按「能不能用这个入口」而不是「能改哪张表」: 权限点的判据是人的职责, 而表结构会变。
const (
	PermDashboardView = "task-system:dashboard:view"
	PermTaskList      = "task-system:task:list"
	// PermTaskListAll 是**数据范围**的开关: 持有它的人看得到全部任务, 否则只看得到
	// 自己创建的、被分配的、以及被分享的。它不是"另一个查看入口" —— 列表还是同一个列表。
	PermTaskListAll = "task-system:task:list-all"
	PermTaskCreate  = "task-system:task:create"
	PermTaskUpdate  = "task-system:task:update"
	PermTaskDelete  = "task-system:task:delete"
	// PermTaskRestore 同时管回收站的查看与恢复: "能看但只能看"是一种没人要的权力,
	// 而把它拆成两个码只会让人在两个地方都勾上才敢点。
	PermTaskRestore = "task-system:task:restore"
	PermTaskShare   = "task-system:task:share"
	PermReminderMgr = "task-system:reminder:manage"
	PermUserView    = "task-system:user:view"
	PermUserManage  = "task-system:user:manage"
)

// 菜单码(M 型)。它们**不参与服务端判定** —— 判"能不能进这个页面"是前端的事
// (见 frontend/src/utils/menu.js 与 router/index.js 的 meta.perm)。
//
// 仍然放在这里, 是因为"躬行在平台上登记了哪些码"必须只有一处可查: 两份清单
// (deploy/perms.manifest.json 与 seed-perms.sql)与这两组常量、以及前端那两份,
// 一共五处, 而它们分叉时最隐蔽的一种是"前端在判一个平台上不存在的菜单码" ——
// 那个菜单永远不出现, 而服务端不会报任何错(它压根没被请求过)。
const (
	PermMenuDashboard = "task-system:dashboard"
	PermMenuTask      = "task-system:task"
	PermMenuTrash     = "task-system:task-trash"
	PermMenuUser      = "task-system:user"
)

type Handler struct {
	db       *gorm.DB
	cfg      *config.Config
	guard    *onelinksdk.Guard
	profiles *onelink.Profiles
	notify   *service.Notifier

	uploadDir string
}

// New 构造。guard 与 profiles 由 cmd/server 装配好传进来 —— 它们是接入层的东西,
// 让 handler 自己按配置去建会在测试里多出一个"假平台", 而这一层要测的是业务语义。
func New(db *gorm.DB, cfg *config.Config, guard *onelinksdk.Guard, profiles *onelink.Profiles, notify *service.Notifier) *Handler {
	dir, err := filepath.Abs(cfg.UploadDir)
	if err != nil {
		dir = cfg.UploadDir
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		// 上传目录不可用只影响头像，不阻断启动，但必须显式暴露原因
		gin.DefaultWriter.Write([]byte("[warn] 创建上传目录失败，头像上传将不可用: " + err.Error() + "\n"))
	}
	return &Handler{
		db:        db,
		cfg:       cfg,
		guard:     guard,
		profiles:  profiles,
		notify:    notify,
		uploadDir: dir,
	}
}

// Close 回收后台资源。接入 OneLink 之后没有进程内限流协程了, 保留这个方法是为了让
// 调用方(main / 测试)不必因为"以后可能加"而改一次签名。
func (h *Handler) Close() {}

// userCtx 当前请求背后的**本地**用户。
//
// 只带业务代码真正要用的两样: 本地主键(业务表外键)与账号(展示与日志)。平台身份另在
// onelink.Principal 里, 通过 can() 取 —— 混成一个结构会诱使人拿 Identity.Username
// 去关联业务数据, 而账号在平台侧是可以被改的。
type userCtx struct {
	ID       uint
	Username string
	p        *onelink.Principal
}

// currentUser 从上下文取当前用户; 中间件保证受保护路由上一定非空。
func currentUser(c *gin.Context) *userCtx {
	p, ok := onelink.PrincipalOf(c)
	if !ok {
		return nil
	}
	return &userCtx{ID: p.LocalUserID, Username: p.Identity.Username, p: p}
}

// can 判"当前人有没有这个功能权限"。
//
// 用的是**登录那一刻的快照**(见 onelink.Principal.HasPermission): 权限被改后, 这里
// 的结果最迟要等下一次登录才变。所以它只用来决定"查询要不要放宽数据范围"这类口径 ——
// 真正的入口管控在路由上(RequirePerm), 那一条读的是同一份快照, 但它的失败是显式的 403。
func can(c *gin.Context, code string) bool {
	p, ok := onelink.PrincipalOf(c)
	return ok && (p.IsSuper() || p.HasPermission(code))
}

// RegisterRoutes 注册全部路由。
//
// 认证只有一条链路: OneLink 的应用会话 cookie。没有登录页、没有注册、没有找回密码 ——
// 那些都在平台上, 而应用侧再放一个入口等于给同一个身份开了第二道门。
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// 没配 OneLink 时整族跳过。这不是"降级运行" —— 业务路由全部在会话守卫之后, 没有守卫
	// 就没有"当前人", 挂上去只会得到一堆 500。那种状态只该出现在本地开发: 生产环境缺
	// 配置会在 config.validate 里直接启动失败。
	if h.guard == nil || h.profiles == nil {
		gin.DefaultWriter.Write([]byte(
			"[warn] 未配置 OneLink, 业务路由未挂载(只有 /uploads 静态资源可用)\n"))
		return
	}

	// 落地页与登出必须在守卫**之外**: 它们正是"还没有会话"时唯一要能走到的入口。
	r.GET("/sso/landing", gin.WrapF(h.guard.HandleTicket))
	r.POST("/logout", gin.WrapF(h.guard.Logout))

	api := r.Group("/api/v1", middleware.BodySizeLimit(h.cfg.MaxBodyBytes))

	// 需要登录。权限点一律挂在**路由级**而不是分组级: 分组级会让"查看任务"与
	// "删除任务"变成同一个权力, 而那两件事对人的要求完全不同。
	user := api.Group("", onelink.RequireLogin(h.guard, h.profiles))

	user.GET("/auth/me", h.Me)
	user.PUT("/profile", h.UpdateProfile)
	user.PUT("/profile/avatar", h.UploadAvatar)

	user.GET("/users", onelink.RequirePerm(PermUserView), h.ListUsers)
	user.PATCH("/users/:id", onelink.RequirePerm(PermUserManage), h.UpdateUser)

	// /tasks/deleted 必须注册在 /tasks/:id 之前, 否则 "deleted" 会被当成 id。
	user.GET("/tasks", onelink.RequirePerm(PermTaskList), h.ListTasks)
	user.GET("/tasks/deleted", onelink.RequirePerm(PermTaskRestore), h.ListDeletedTasks)
	user.GET("/tasks/:id", onelink.RequirePerm(PermTaskList), h.GetTask)
	user.POST("/tasks", onelink.RequirePerm(PermTaskCreate), h.CreateTask)
	user.PATCH("/tasks/:id", onelink.RequirePerm(PermTaskUpdate), h.UpdateTask)
	user.DELETE("/tasks/:id", onelink.RequirePerm(PermTaskDelete), h.DeleteTask)
	user.POST("/tasks/:id/restore", onelink.RequirePerm(PermTaskRestore), h.RestoreTask)
	user.POST("/tasks/:id/progress", onelink.RequirePerm(PermTaskUpdate), h.AddProgress)
	user.GET("/tasks/:id/shares", onelink.RequirePerm(PermTaskList), h.ListShares)
	user.POST("/tasks/:id/shares", onelink.RequirePerm(PermTaskShare), h.AddShare)
	user.DELETE("/tasks/:id/shares/:shareId", onelink.RequirePerm(PermTaskShare), h.RevokeShare)

	// 提醒是任务的一部分: 看得到任务就该看得到它的提醒(否则列表里会冒出"有提醒但我
	// 打不开"的行), 而"建提醒"改的是任务的触发行为, 所以它单独一个码。
	user.GET("/reminders", onelink.RequirePerm(PermTaskList), h.ListReminders)
	user.GET("/reminders/unread-count", onelink.RequirePerm(PermTaskList), h.UnreadCount)
	user.POST("/reminders", onelink.RequirePerm(PermReminderMgr), h.CreateReminder)
	user.POST("/reminders/read-all", onelink.RequirePerm(PermTaskList), h.MarkAllRead)
	user.POST("/reminders/:id/read", onelink.RequirePerm(PermTaskList), h.MarkRead)

	user.GET("/stats/overview", onelink.RequirePerm(PermDashboardView), h.Overview)
}
