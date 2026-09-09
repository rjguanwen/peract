package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	ProjectName               string
	Port                      string
	SecretKey                 string
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
	SMTPUseTLS        bool

	AdminUsername string
	AdminPassword string
	AdminEmail    string
}

func (c *Config) AccessTokenExpireMinutes() int {
	if c.AccessTokenExpireMinutes_ <= 0 {
		return 10080 // 7 天
	}
	return c.AccessTokenExpireMinutes_
}

func Load() *Config {
	// 不存在 .env 时忽略，使用环境变量默认值
	_ = godotenv.Load()

	cfg := &Config{
		ProjectName:               getEnv("PROJECT_NAME", "任务管理系统"),
		Port:                      getEnv("PORT", "8001"),
		SecretKey:                 getEnv("SECRET_KEY", "please-change-me-to-a-random-secret"),
		DatabaseURL:               getEnv("DATABASE_URL", "sqlite:///./task.db"),
		AccessTokenExpireMinutes_: getEnvInt("ACCESS_TOKEN_EXPIRE_MINUTES", 10080),

		AppBaseURL: getEnv("APP_BASE_URL", "http://localhost:5173"),
		UploadDir:  getEnv("UPLOAD_DIR", "./uploads"),

		NotifyWebhookURL:  getEnv("NOTIFY_WEBHOOK_URL", ""),
		NotifyWebhookType: getEnv("NOTIFY_WEBHOOK_TYPE", "generic"),
		SMTPHost:          getEnv("SMTP_HOST", ""),
		SMTPPort:          getEnvInt("SMTP_PORT", 465),
		SMTPUser:          getEnv("SMTP_USER", ""),
		SMTPPassword:      getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:          getEnv("SMTP_FROM", ""),
		SMTPUseTLS:        getEnvBool("SMTP_USE_TLS", true),

		AdminUsername: getEnv("INIT_ADMIN_USERNAME", "admin"),
		AdminPassword: getEnv("INIT_ADMIN_PASSWORD", "admin123"),
		AdminEmail:    getEnv("INIT_ADMIN_EMAIL", "admin@example.com"),
	}

	if cfg.SecretKey == "please-change-me-to-a-random-secret" {
		log.Println("[warn] 请修改 SECRET_KEY 为强随机值")
	}
	return cfg
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
