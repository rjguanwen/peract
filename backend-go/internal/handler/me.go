package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/model"
	"taskbackend/internal/onelink"
)

// UserOut 本地用户档案的对外形状。
//
// 它是**档案**而不是身份: 账号与口令在 OneLink, 这里给的是业务上要展示的那几列,
// 外加一份本次会话的权限快照。没有 role 字段 —— 角色的载体是平台侧的角色授权
// (sys_user_role.app_id), 拿一个字符串表达它等于在应用侧再造一份会漂走的真值。
type UserOut struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	IsActive  bool      `json:"is_active"`
	AvatarURL *string   `json:"avatar_url"`
	Signature string    `json:"signature"`
	CreatedAt time.Time `json:"created_at"`

	// SuperAdmin / Permissions 来自**会话快照**, 不是本地库里的列。
	//
	// 它们是给界面显隐用的加速值: 前端据此决定摆不摆某个入口, 而每一条接口在服务端
	// 都有自己的权限点(见 RegisterRoutes)。权限被改后这份快照要等下一次登录才更新 ——
	// 那时界面会短暂地多摆或少摆一个按钮, 但**点不动**: 服务端那一道是权威的。
	SuperAdmin  bool     `json:"super_admin"`
	Permissions []string `json:"permissions"`
}

// toUserOut 转换。p 可以为 nil(例如后台任务构造响应时), 那时权限字段回空 ——
// 空权限集合的含义是"什么都点不动", 与"权限未知"在界面上是同一个处置(不摆入口),
// 所以不必为它另造一个三态。
func toUserOut(u *model.User, p *onelink.Principal) UserOut {
	out := UserOut{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		FullName:    u.FullName,
		IsActive:    u.IsActive,
		AvatarURL:   u.AvatarURL,
		Signature:   u.Signature,
		CreatedAt:   u.CreatedAt,
		Permissions: []string{},
	}
	if p != nil {
		out.SuperAdmin = p.IsSuper()
		if len(p.Identity.Permissions) > 0 {
			out.Permissions = p.Identity.Permissions
		}
	}
	return out
}

// Me GET /auth/me
//
// 前端启动时打这一条: 200 说明会话有效, 401 说明该回门户了。因此它**不能**要求任何
// 业务权限点 —— 只挂了登录守卫。一个只有"查看任务"权限的人如果在这一条上拿到 403,
// 前端会把它当成会话失效而陷入跳转循环。
func (h *Handler) Me(c *gin.Context) {
	ctx := currentUser(c)
	var user model.User
	if err := h.db.First(&user, ctx.ID).Error; err != nil {
		notFound(c, "用户档案不存在")
		return
	}
	c.JSON(http.StatusOK, toUserOut(&user, ctx.p))
}
