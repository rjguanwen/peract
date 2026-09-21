package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	ProjectName string
	// Env 运行环境：development（默认）/ production。
	Env  string
	Port string

	// ---- OneLink 集成 ----
	//
	// 身份与治理层(账号、口令、组织、菜单、角色授权)由 OneLink 接管, 躬行只留业务数据
	// 与一份本地用户档案。这四项是接入的全部配置, 也是**唯一**的身份来源 ——
	// 这里没有 SECRET_KEY, 因为应用侧不再签发任何凭据。
	OnelinkAppCode   string
	OnelinkBaseURL   string
	OnelinkPortalURL string
	// OnelinkAppSecret 只在服务端。不进前端产物、不进日志、不进版本库 ——
	// 它是平台侧那道签名四头的唯一凭据(见《接入规范》P0 第 2 条)。
	OnelinkAppSecret string
	// OnelinkAliveInterval 多久回平台问一次会话存活。它直接决定单点登出的可感知滞后:
	// 平台登出后, 躬行最多滞后一个周期才跟着下线。SDK 的下限是 5 秒, 默认 60 秒。
	OnelinkAliveInterval time.Duration

	DatabaseURL string

	AppBaseURL string // 对外前端地址
	UploadDir  string // 上传文件根目录（头像等）

	NotifyWebhookURL  string
	NotifyWebhookType string
	SMTPHost          string
	SMTPPort          int
	SMTPUser          string
	SMTPPassword      string
	SMTPFrom          string
	SMTPFromName      string
	SMTPUseTLS        bool

	// 运行期保护参数
	CORSAllowOrigins   []string      // 允许跨域的来源；含 "*" 表示放通任意站点
	TrustedProxies     []string      // 受信代理/CIDR；留空则不采信任何 X-Forwarded-For
	MaxBodyBytes       int64         // 单个请求体上限
	ShutdownTimeout    time.Duration // 优雅停机等待在途请求的上限
	NotifyWorkers      int           // 外部通知后台 worker 数
	NotifyQueueSize    int           // 通知队列长度，满则丢弃并记日志
	SMTPDialTimeout    time.Duration // SMTP 建连超时
	SMTPCommandTimeout time.Duration // SMTP 单次会话总超时
	WebhookTimeout     time.Duration // IM webhook 请求超时
}

func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// OnelinkConfigured 三项接入配置齐了没有。
//
// 没齐时**不阻断启动**, 只是不挂业务路由: 本地开发(只想跑业务接口、或还没搭起平台)
// 不该因为缺一个环境变量而起不来。而"配了一半"必须在启动日志里点名 —— 那种状态最容易
// 表现为"点了门户卡片没反应", 它既不像配置错误也不像代码错误(见 validate)。
func (c *Config) OnelinkConfigured() bool {
	return c.OnelinkBaseURL != "" && c.OnelinkPortalURL != "" && c.OnelinkAppSecret != ""
}

// SMTPConfigured 判断邮件通道是否可用。
func (c *Config) SMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPFrom != ""
}

func Load() *Config {
	// 不存在 .env 时忽略，使用环境变量默认值
	_ = godotenv.Load()

	cfg := &Config{
		ProjectName: getEnv("PROJECT_NAME", "躬行"),
		Env:         getEnv("APP_ENV", "development"),
		Port:        getEnv("PORT", "8001"),
		DatabaseURL: getEnv("DATABASE_URL", "sqlite:///./task.db"),

		OnelinkAppCode:       getEnv("ONELINK_APP_CODE", "task-system"),
		OnelinkBaseURL:       getEnv("ONELINK_BASE_URL", ""),
		OnelinkPortalURL:     getEnv("ONELINK_PORTAL_URL", ""),
		OnelinkAppSecret:     getEnv("ONELINK_APP_SECRET", ""),
		OnelinkAliveInterval: secondsEnv("ONELINK_ALIVE_INTERVAL_SECONDS", 60),

		AppBaseURL: getEnv("APP_BASE_URL", "http://localhost:5173"),
		UploadDir:  getEnv("UPLOAD_DIR", "./uploads"),

		NotifyWebhookURL:  getEnv("NOTIFY_WEBHOOK_URL", ""),
		NotifyWebhookType: getEnv("NOTIFY_WEBHOOK_TYPE", "generic"),
		SMTPHost:          getEnv("SMTP_HOST", ""),
		SMTPPort:          getEnvInt("SMTP_PORT", 465),
		SMTPUser:          getEnv("SMTP_USER", ""),
		// 兼容两种密码键名：SMTP_PASSWORD 优先，其次 SMTP_PASS（与飞光/其他项目一致）
		SMTPPassword: getEnv("SMTP_PASSWORD", getEnv("SMTP_PASS", "")),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		SMTPFromName: getEnv("SMTP_FROM_NAME", "躬行"),
		SMTPUseTLS:   getEnvBool("SMTP_USE_TLS", true),

		CORSAllowOrigins: getEnvList("CORS_ALLOW_ORIGINS", []string{"http://localhost:5173"}),
		TrustedProxies:   getEnvList("TRUSTED_PROXIES", nil),
		MaxBodyBytes:     int64(getEnvInt("MAX_BODY_BYTES", 12<<20)), // 12MB，容纳 5MB 头像表单
		ShutdownTimeout:  secondsEnv("SHUTDOWN_TIMEOUT_SECONDS", 10),
		NotifyWorkers:    getEnvInt("NOTIFY_WORKERS", 2),
		NotifyQueueSize:  getEnvInt("NOTIFY_QUEUE_SIZE", 256),
		SMTPDialTimeout:  secondsEnv("SMTP_DIAL_TIMEOUT_SECONDS", 10),
		// SMTPCommandTimeout: 单次会话总超时
		SMTPCommandTimeout: secondsEnv("SMTP_COMMAND_TIMEOUT_SECONDS", 20),
		WebhookTimeout:     secondsEnv("WEBHOOK_TIMEOUT_SECONDS", 5),
	}

	if err := cfg.validate(); err != nil {
		log.Fatalf("配置不合法: %v", err)
	}
	return cfg
}

// validate 校验关键配置。
//
// 生产环境对 OneLink 三项**从严**: 少了它们应用连一个人都进不来(所有业务路由都在
// 会话守卫之后), 而"起得来但谁都进不去"比"起不来"难排查得多 —— 后者至少有一行日志。
func (c *Config) validate() error {
	set := 0
	for _, v := range []string{c.OnelinkBaseURL, c.OnelinkPortalURL, c.OnelinkAppSecret} {
		if v != "" {
			set++
		}
	}
	switch {
	case set == 0:
		if c.IsProduction() {
			return fmt.Errorf("生产环境必须配置 ONELINK_BASE_URL / ONELINK_PORTAL_URL / ONELINK_APP_SECRET" +
				"：身份由 OneLink 接管, 缺了它们所有业务路由都进不去")
		}
		log.Println("[warn] 未配置 OneLink(ONELINK_BASE_URL / ONELINK_PORTAL_URL / ONELINK_APP_SECRET), 业务路由不会挂载")
	default:
		if set < 3 {
			// 三缺一的表现是"门户点了卡片没反应", 它既不像配置错误也不像代码错误 ——
			// 启动日志里说一句能省掉一次这样的排查。
			return fmt.Errorf("OneLink 配置不完整: ONELINK_BASE_URL / ONELINK_PORTAL_URL / "+
				"ONELINK_APP_SECRET 三项里只填了 %d 项", set)
		}
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("MAX_BODY_BYTES 必须为正数")
	}
	// CORS 的通配与会话 cookie 不能共存(浏览器不接受带凭据的通配响应), 而它的现象是
	// "跨域登录静默不生效"。生产环境直接拒绝, 开发环境交给 middleware.CORS 出声。
	if c.IsProduction() {
		for _, origin := range c.CORSAllowOrigins {
			if origin == "*" {
				return fmt.Errorf("生产环境 CORS_ALLOW_ORIGINS 不能含 *：会话是凭据型 cookie, " +
					"浏览器不会在带凭据的跨域请求上接受通配来源, 跨域登录会静默失败")
			}
		}
	}
	if c.NotifyWorkers < 1 {
		c.NotifyWorkers = 1
	}
	if c.NotifyQueueSize < 1 {
		c.NotifyQueueSize = 64
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

// secondsEnv 读取以秒为单位的环境变量并转为 Duration。
func secondsEnv(key string, def int) time.Duration {
	return time.Duration(getEnvInt(key, def)) * time.Second
}

// getEnvList 解析逗号分隔列表，自动去空白与空项。
func getEnvList(key string, def []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	out := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
