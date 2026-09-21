// Package middleware 放与身份无关的横切关注点。
//
// 这里**没有认证中间件**: 登录态由 OneLink 的应用会话承载, 守卫在 internal/onelink
// (guard.go)。原先那个自签 JWT 的 Auth(签发/校验/黑名单/改密作废)随接入一起删掉了 ——
// 留着它等于给同一个身份开第二道门, 而两道门的强度不一样时, 攻击面等于弱的那一道。
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"taskbackend/internal/config"
)

// CORS 跨域中间件。默认只放通配置里列出的来源，并正确回显 Allow-Credentials 语义。
//
// 通配 "*" 现在比接入前**更**危险, 所以这里额外拒掉它: 会话是一张 HttpOnly cookie,
// 而浏览器不允许 `Access-Control-Allow-Origin: *` 与凭据请求共存 —— 真配了通配的部署
// 会得到一个"跨域登录静默不生效"的现象, 而不是一个报错。宁可启动时出声。
func CORS(cfg *config.Config) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]bool, len(cfg.CORSAllowOrigins))
	for _, origin := range cfg.CORSAllowOrigins {
		if origin == "*" {
			allowAll = true
			continue
		}
		allowed[strings.ToLower(strings.TrimRight(origin, "/"))] = true
	}
	if allowAll {
		gin.DefaultWriter.Write([]byte(
			"[warn] CORS_ALLOW_ORIGINS 含 \"*\", 但会话是凭据型 cookie: 浏览器不会在带凭据的" +
				"跨域请求上接受通配来源, 跨域登录会静默失败。请列出确切来源。\n"))
	}
	methods := "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	// Authorization 不再需要, 但留着它没有代价; X-Requested-With 是前端历史约定。
	headers := "Authorization, Content-Type, X-Requested-With"

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			key := strings.ToLower(strings.TrimRight(origin, "/"))
			switch {
			case allowed[key]:
				c.Header("Access-Control-Allow-Origin", origin)
				// 带凭据的跨域响应必须按来源分缓存, 否则中间缓存会把 A 站点的响应给 B 站点。
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Credentials", "true")
			case allowAll:
				// 不回 Allow-Credentials: 与 "*" 一起发出去浏览器会直接拒掉整条响应,
				// 而"拒掉"在现场看起来是"跨域请求没反应"。
				c.Header("Access-Control-Allow-Origin", "*")
			}
		}
		c.Header("Access-Control-Allow-Methods", methods)
		c.Header("Access-Control-Allow-Headers", headers)
		c.Header("Access-Control-Max-Age", "600")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodySizeLimit 限制请求体大小，防止超大载荷打满内存。
func BodySizeLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"detail": "请求体过大"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
