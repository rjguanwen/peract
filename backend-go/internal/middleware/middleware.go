package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
)

// UserContext 认证后写入上下文的用户信息
type UserContext struct {
	ID       uint
	Username string
	Role     string
	Email    string
}

// userColumnAllowlist 认证路径只需要这几列，显式列出可避免把口令/安全答案哈希读进内存。
const userAuthColumns = "id, username, role, email, is_active, password_changed_at"

type Auth struct {
	cfg *config.Config
	db  *gorm.DB

	// 已使用/已撤销的重置令牌摘要（JWT 本身无状态，一次性靠服务端黑名单保证）
	revokedMu   sync.Mutex
	revoked     map[string]time.Time // sha256(token) -> 过期时间
	revokedStop chan struct{}
	revokedOnce sync.Once
}

func NewAuth(cfg *config.Config, db *gorm.DB) *Auth {
	a := &Auth{
		cfg:         cfg,
		db:          db,
		revoked:     make(map[string]time.Time),
		revokedStop: make(chan struct{}),
	}
	go a.sweepRevoked()
	return a
}

// Stop 回收重置令牌黑名单的清理协程。
func (a *Auth) Stop() {
	a.revokedOnce.Do(func() { close(a.revokedStop) })
}

// CORS 跨域中间件。默认只放通配置里列出的来源，并正确回显 Allow-Credentials 语义。
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
	methods := "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	headers := "Authorization, Content-Type, X-Requested-With"

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			key := strings.ToLower(strings.TrimRight(origin, "/"))
			if allowAll {
				// 通配仅在纯 Bearer 令牌、不依赖 Cookie 的场景下可接受
				c.Header("Access-Control-Allow-Origin", "*")
			} else if allowed[key] {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
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

// signingKey 供 jwt 解析回调使用，同时拒绝非 HMAC 家族算法（防 alg=none / RS256 混淆）。
func (a *Auth) signingKey(t *jwt.Token) (any, error) {
	if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
	}
	return []byte(a.cfg.SecretKey), nil
}

// CreateToken 生成访问 JWT
func (a *Auth) CreateToken(userID uint) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"iss": a.cfg.ProjectName,
		"exp": now.Add(time.Duration(a.cfg.AccessTokenExpireMinutes()) * time.Minute).Unix(),
		// 必须带 jti：HMAC-SHA256 对相同 claims 的签名是确定性的，
		// 同一秒内重新登录会拿到与上一条逐字节相同的令牌；
		// 若上一条已被吊销（登出/改密），新会话会莫名撞上黑名单。
		"jti": randomTokenString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(a.cfg.SecretKey))
}

// SignResetToken 生成一次性密码重置令牌（有效期 30 分钟）。
func (a *Auth) SignResetToken(email string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"purpose": "reset_password",
		"email":   email,
		"iat":     now.Unix(),
		"iss":     a.cfg.ProjectName,
		"exp":     now.Add(30 * time.Minute).Unix(),
		"jti":     randomTokenString(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(a.cfg.SecretKey))
}

// VerifyResetToken 校验重置令牌，返回其绑定的邮箱。已使用过的令牌会被拒绝。
func (a *Auth) VerifyResetToken(tokenStr string) (string, error) {
	claims, err := a.parse(tokenStr)
	if err != nil {
		return "", errors.New("invalid token")
	}
	if claims["purpose"] != "reset_password" {
		return "", errors.New("invalid purpose")
	}
	email, ok := claims["email"].(string)
	if !ok || email == "" {
		return "", errors.New("invalid email claim")
	}
	if a.isRevoked(tokenStr) {
		return "", errors.New("token already used")
	}
	return email, nil
}

// ConsumeResetToken 将重置令牌标记为已使用，使其在有效期内只能成功一次。
func (a *Auth) ConsumeResetToken(tokenStr string) {
	a.RevokeToken(tokenStr)
}

// RevokeToken 把令牌摘要加入黑名单，保留至其自身 exp。
//
// JWT 无状态意味着「登出」「改密」都管不住已签发的令牌——被窃取的访问令牌可以在
// 用户重置密码后继续用。这里用进程内黑名单补上这一环；多实例部署需换共享存储。
func (a *Auth) RevokeToken(tokenStr string) {
	claims, err := a.parse(tokenStr)
	if err != nil {
		return
	}
	expiry := time.Now().Add(30 * time.Minute)
	if exp, ok := claims["exp"].(float64); ok {
		expiry = time.Unix(int64(exp), 0)
	}
	sum := hashToken(tokenStr)
	a.revokedMu.Lock()
	a.revoked[sum] = expiry
	a.revokedMu.Unlock()
}

func (a *Auth) isRevoked(tokenStr string) bool {
	sum := hashToken(tokenStr)
	a.revokedMu.Lock()
	defer a.revokedMu.Unlock()
	expiry, ok := a.revoked[sum]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(a.revoked, sum)
		return false
	}
	return true
}

// sweepRevoked 丢弃已过期的黑名单条目，避免长期累积。
func (a *Auth) sweepRevoked() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.revokedStop:
			return
		case now := <-ticker.C:
			a.revokedMu.Lock()
			for sum, expiry := range a.revoked {
				if now.After(expiry) {
					delete(a.revoked, sum)
				}
			}
			a.revokedMu.Unlock()
		}
	}
}

func hashToken(tokenStr string) string {
	sum := sha256.Sum256([]byte(tokenStr))
	return hex.EncodeToString(sum[:])
}

func (a *Auth) parse(tokenStr string) (jwt.MapClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, jwt.MapClaims{}, a.signingKey,
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(a.cfg.ProjectName),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// RequireUser 要求登录
func (a *Auth) RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := a.currentUser(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "登录凭证无效或已过期"})
			return
		}
		c.Set(UserContextKey, user)
		c.Next()
	}
}

// RequireAdmin 要求管理员
func (a *Auth) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := a.currentUser(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "登录凭证无效或已过期"})
			return
		}
		if user.Role != model.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"detail": "需要管理员权限"})
			return
		}
		c.Set(UserContextKey, user)
		c.Next()
	}
}

// UserContextKey 是 gin 上下文中存放当前用户的键名。
const UserContextKey = "user"

func (a *Auth) currentUser(c *gin.Context) (*UserContext, bool) {
	tokenStr, ok := bearerToken(c)
	if !ok {
		return nil, false
	}
	claims, err := a.parse(tokenStr)
	if err != nil {
		return nil, false
	}
	if a.isRevoked(tokenStr) {
		return nil, false
	}
	userID, ok := claimUint(claims["sub"])
	if !ok {
		return nil, false
	}
	var user model.User
	// 只取鉴权所需列：既减少一次全行读取，也避免口令哈希进入应用内存
	if err := a.db.Select(userAuthColumns).First(&user, userID).Error; err != nil {
		return nil, false
	}
	if !user.IsActive {
		return nil, false
	}
	// 改密（含管理员重置）之前签发的令牌一律作废
	if user.PasswordChangedAt != nil && !claimIssuedAfter(claims, *user.PasswordChangedAt) {
		return nil, false
	}
	return &UserContext{
		ID:       user.ID,
		Username: user.Username,
		Role:     user.Role,
		Email:    user.Email,
	}, true
}

// BearerToken 取出请求携带的原始令牌字符串，供吊销场景使用。
func BearerToken(c *gin.Context) (string, bool) {
	return bearerToken(c)
}

// bearerToken 从 Authorization 头取出 Bearer 令牌。
func bearerToken(c *gin.Context) (string, bool) {
	header := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

// claimUint 兼容 JWT 中数字与字符串两种 sub 写法。
func claimUint(v any) (uint, bool) {
	switch n := v.(type) {
	case float64:
		return uint(n), n >= 0
	case int:
		return uint(n), n >= 0
	case uint:
		return n, true
	case string:
		var parsed uint64
		if _, err := fmt.Sscanf(n, "%d", &parsed); err != nil {
			return 0, false
		}
		return uint(parsed), true
	default:
		return 0, false
	}
}

// claimIssuedAfter 判断令牌的 iat 是否不早于 givenAt。缺失 iat 时视为改造前签发的旧令牌，保守拒绝。
func claimIssuedAfter(claims jwt.MapClaims, givenAt time.Time) bool {
	iat, ok := claims["iat"].(float64)
	if !ok {
		return false
	}
	// 按秒比较：与改密发生在同一秒内签发的令牌仍可用，这个窗口不影响安全性
	return int64(iat) >= givenAt.Unix()
}

// randomTokenString 生成 jti 用的随机标识，失败时退化为时间戳。
func randomTokenString() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
