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

// weakSecretKey 是 .env.example 中给出的占位值，生产环境禁止使用。
const weakSecretKey = "please-change-me-to-a-random-secret"

type Config struct {
	ProjectName string
	// Env 运行环境：development（默认）/ production。production 下弱密钥等不安全配置会阻断启动。
	Env       string
	Port      string
	SecretKey string

	DatabaseURL               string
	AccessTokenExpireMinutes_ int

	AppBaseURL string // 对外前端地址，用于拼邀请/重置链接
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

	AdminUsername string
	AdminPassword string
	AdminEmail    string

	// 运行期保护参数
	CORSAllowOrigins   []string      // 允许跨域的来源；含 "*" 表示放通任意站点
	TrustedProxies     []string      // 受信代理/CIDR；留空则不采信任何 X-Forwarded-For
	MaxBodyBytes       int64         // 单个请求体上限
	ShutdownTimeout    time.Duration // 优雅停机等待在途请求的上限
	LoginFailLimit     int           // 登录失败窗口内允许的最大次数
	LoginFailWindow    time.Duration // 登录失败统计窗口
	NotifyLimit        int           // 找回密码等敏感接口窗口内允许次数
	NotifyWindow       time.Duration // 敏感接口限流窗口
	NotifyWorkers      int           // 外部通知后台 worker 数
	NotifyQueueSize    int           // 通知队列长度，满则丢弃并记日志
	SMTPDialTimeout    time.Duration // SMTP 建连超时
	SMTPCommandTimeout time.Duration // SMTP 单次会话总超时
	WebhookTimeout     time.Duration // IM webhook 请求超时
}

func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// AccessTokenExpireMinutes 返回令牌有效期，非正数回落到 7 天。
func (c *Config) AccessTokenExpireMinutes() int {
	if c.AccessTokenExpireMinutes_ <= 0 {
		return 10080 // 7 天
	}
	return c.AccessTokenExpireMinutes_
}

// SMTPConfigured 判断邮件通道是否可用。
func (c *Config) SMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPFrom != ""
}

func Load() *Config {
	// 不存在 .env 时忽略，使用环境变量默认值
	_ = godotenv.Load()

	cfg := &Config{
		ProjectName:               getEnv("PROJECT_NAME", "躬行"),
		Env:                       getEnv("APP_ENV", "development"),
		Port:                      getEnv("PORT", "8001"),
		SecretKey:                 getEnv("SECRET_KEY", weakSecretKey),
		DatabaseURL:               getEnv("DATABASE_URL", "sqlite:///./task.db"),
		AccessTokenExpireMinutes_: getEnvInt("ACCESS_TOKEN_EXPIRE_MINUTES", 10080),

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

		AdminUsername: getEnv("INIT_ADMIN_USERNAME", "admin"),
		AdminPassword: getEnv("INIT_ADMIN_PASSWORD", "admin123"),
		AdminEmail:    getEnv("INIT_ADMIN_EMAIL", "admin@example.com"),

		CORSAllowOrigins:   getEnvList("CORS_ALLOW_ORIGINS", []string{"*"}),
		TrustedProxies:     getEnvList("TRUSTED_PROXIES", nil),
		MaxBodyBytes:       int64(getEnvInt("MAX_BODY_BYTES", 12<<20)), // 12MB，容纳 5MB 头像表单
		ShutdownTimeout:    secondsEnv("SHUTDOWN_TIMEOUT_SECONDS", 10),
		LoginFailLimit:     getEnvInt("LOGIN_FAIL_LIMIT", 5),
		LoginFailWindow:    secondsEnv("LOGIN_FAIL_WINDOW_SECONDS", 600),
		NotifyLimit:        getEnvInt("NOTIFY_LIMIT", 3),
		NotifyWindow:       secondsEnv("NOTIFY_WINDOW_SECONDS", 300),
		NotifyWorkers:      getEnvInt("NOTIFY_WORKERS", 2),
		NotifyQueueSize:    getEnvInt("NOTIFY_QUEUE_SIZE", 256),
		SMTPDialTimeout:    secondsEnv("SMTP_DIAL_TIMEOUT_SECONDS", 10),
		SMTPCommandTimeout: secondsEnv("SMTP_COMMAND_TIMEOUT_SECONDS", 20),
		WebhookTimeout:     secondsEnv("WEBHOOK_TIMEOUT_SECONDS", 5),
	}

	if err := cfg.validate(); err != nil {
		log.Fatalf("配置不合法: %v", err)
	}
	return cfg
}

// validate 校验关键安全配置，生产环境对弱密钥从严。
func (c *Config) validate() error {
	if c.SecretKey == "" {
		return fmt.Errorf("SECRET_KEY 不能为空")
	}
	if (c.SecretKey == weakSecretKey || len(c.SecretKey) < 32) && c.IsProduction() {
		return fmt.Errorf("SECRET_KEY 仍为占位值或长度不足 32，生产环境请设置为强随机值（如 openssl rand -hex 32）")
	}
	if c.SecretKey == weakSecretKey || len(c.SecretKey) < 32 {
		log.Println("[warn] SECRET_KEY 为占位值/弱密钥，仅可在开发环境使用")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("MAX_BODY_BYTES 必须为正数")
	}
	if c.IsProduction() && len(c.CORSAllowOrigins) == 1 && c.CORSAllowOrigins[0] == "*" {
		log.Println("[warn] 生产环境 CORS_ALLOW_ORIGINS 仍为 *，建议收敛为具体前端域名")
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
