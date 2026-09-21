package onelink

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	sdk "github.com/onelink/platform/sdk/go/onelink"
)

// PrincipalKey 是 gin 上下文中存放当前身份的键。
const PrincipalKey = "onelink_principal"

// errNoIdentity SDK 放行了请求却没把身份写进上下文。这是一次 SDK 契约变更或本包用错了
// 方法(例如把 HandleTicket 当守卫用), 不是用户的问题 —— 因此它走 500 而不是 401。
var errNoIdentity = errors.New("平台放行了请求但没有写入身份")

// Principal 一次请求背后的身份。
//
// 它把两件事绑在一起: 平台的身份快照(权限码、超管标志、平台用户主键)与躬行本地的
// 档案主键。业务代码只该用 LocalUserID 关联业务表, 用 Identity/HasPermission 判权限 ——
// 不要拿 Identity.Username 去关联任何东西(账号在平台侧可以被改)。
type Principal struct {
	Identity sdk.Identity
	// LocalUserID 躬行 user.id。业务表外键用的就是它。
	LocalUserID uint
	// LocalID 应用会话主键(cookie 里那个), 不是平台的 sid。
	LocalID string
}

// HasPermission 快照里有没有这个权限码。
//
// 快照是**兑换那一刻**的值: 权限被改后不会自动变。用它做界面显隐没问题; 做访问控制时
// 要记住平台侧仍然是权威的 —— 每一次带令牌回平台的调用都会按最新权限重判, 所以这里
// 放行的东西到了平台那边仍然可能被拒, 反过来则不会(这里拒了就是拒了)。
func (p *Principal) HasPermission(code string) bool {
	if p == nil {
		return false
	}
	for _, k := range p.Identity.Permissions {
		if k == code {
			return true
		}
	}
	return false
}

// IsSuper 平台超级管理员。
//
// 平台侧对超管的权限判定短路成"该应用全部启用的权限点"(Authz.PermKeysOf), 因此这里
// 也必须短路一次: 平台的兑换响应里 superAdmin=true 而 permissions 可能**恰好**不含
// 某个码(超管本就不需要授权行), 不短路会让超管被自己应用的门卫挡在门外。
func (p *Principal) IsSuper() bool { return p != nil && p.Identity.SuperAdmin }

// PrincipalOf 取当前身份。中间件保证受保护路由上一定有。
func PrincipalOf(c *gin.Context) (*Principal, bool) {
	v, ok := c.Get(PrincipalKey)
	if !ok {
		return nil, false
	}
	p, ok := v.(*Principal)
	return p, ok
}

// MustPrincipal 取当前身份, 取不到直接终止请求。
//
// 它存在的意义是让业务 handler 不必写"取不到就 500"这一段: 取不到只可能是路由挂错了
// (少了 RequireLogin), 而那种错必须在**装配期**被看见 —— 见 RequirePerm 的同一条理由。
func MustPrincipal(c *gin.Context) (*Principal, bool) {
	p, ok := PrincipalOf(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusInternalServerError,
			gin.H{"detail": "服务端鉴权配置错误"})
		return nil, false
	}
	return p, true
}

// RequireLogin 把 SDK 的守卫桥接成 gin 中间件。
//
// SDK 的方法是 net/http 形状的(RequireLogin(next http.Handler) http.Handler), 而躬行用
// gin。桥接的全部要点只有一个: Guard 成功时会调用我们传进去的 next, 失败时自己写响应
// 并直接返回 —— 用一个标志位区分这两种情况, 成功才继续 gin 的链。
//
// 这里**不改 SDK**: 它要服务所有语言与框架的接入方, 为 gin 开一个口子等于让它替接入方
// 决定框架。
//
// 桥接里还要做一件事: 把平台身份落成躬行的本地档案(见 Profiles.Upsert)。放在这里而不是
// 放在某个 handler 里, 是因为"每个受保护请求背后都有一个本地 user.id"是这一层的承诺 ——
// 承诺散到各个 handler 去兑现时, 漏掉一个的表现是那条接口上所有业务外键写进 0。
func RequireLogin(g *sdk.Guard, profiles *Profiles) gin.HandlerFunc {
	return func(c *gin.Context) {
		passed := false
		var bridgeErr error

		g.RequireLogin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident, ok := sdk.FromContext(r.Context())
			if !ok {
				// Guard 放行时一定写过身份; 没写说明 SDK 的契约变了, 不能放行。
				bridgeErr = errNoIdentity
				return
			}
			user, err := profiles.Upsert(r.Context(), ident.Identity)
			if err != nil {
				bridgeErr = err
				return
			}
			c.Set(PrincipalKey, &Principal{
				Identity:    ident.Identity,
				LocalUserID: user.ID,
				LocalID:     ident.LocalID,
			})
			passed = true
		})).ServeHTTP(c.Writer, c.Request)

		switch {
		case bridgeErr != nil:
			// 走到这里说明身份已经验过、但本地这一侧没接上。回 500 而不是 401:
			// 401 会把人送回门户重新登录, 而重登一万次也还是同一个错。
			c.AbortWithStatusJSON(http.StatusInternalServerError,
				gin.H{"detail": "本地用户档案不可用, 请联系管理员: " + bridgeErr.Error()})
			return
		case !passed:
			// Guard 已经写过响应(302 到门户, 或 401 JSON), 这里只负责别再往下走。
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequirePerm 在 RequireLogin 之上再要求一个权限码。
//
// 挂在**路由级**而不是分组级: 分组级会让所有接口都要求同一个码, 于是"查看任务"与
// "删除任务"变成同一个权力。链序上它必须排在 RequireLogin 之后 —— 中间件在分组上、
// 权限点在路由上, gin 的分组链天然排在路由链前面, 所以顺序是对的; 而反过来(把权限点
// 挂到分组、登录挂到路由)会得到一个人人可过的门, 且它不报任何错。
func RequirePerm(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := MustPrincipal(c)
		if !ok {
			return
		}
		if p.IsSuper() || p.HasPermission(code) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden,
			gin.H{"detail": "没有该操作的权限(" + code + ")"})
	}
}

// RequirePermAny 要求给定权限码里的任意一个。
//
// 给"同一件事在界面上的两个入口"用(例如回收站的查看与恢复), 它们背后是同一个动作,
// 而给它们各挂一个码只会让人在两个地方都勾上才敢点。
func RequirePermAny(codes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := MustPrincipal(c)
		if !ok {
			return
		}
		if p.IsSuper() {
			c.Next()
			return
		}
		for _, code := range codes {
			if p.HasPermission(code) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"detail": "没有该操作的权限"})
	}
}
