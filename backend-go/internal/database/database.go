package database

import (
	"fmt"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"taskbackend/internal/config"
	"taskbackend/internal/model"
)

// Open 建立数据库连接。本项目架构上固定 SQLite，
// 传入其他引擎的连接串会在启动时直接报错退出，而不是被当成裸文件路径静默建出一个同名 SQLite 库。
func Open(cfg *config.Config) (*gorm.DB, error) {
	path, err := resolveSQLitePath(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
		// 让上层完全掌控事务边界，避免默认事务掩盖错误
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	// WAL 模式下读读、读写可并发；写写仍串行，交由 busy_timeout 重试。
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	if err := applyPragmas(db); err != nil {
		return nil, err
	}
	return db, nil
}

// resolveSQLitePath 从 DATABASE_URL 中提取 SQLite 文件路径。
// 保留对其他引擎前缀的显式拦截：删掉这个分支不算「精简」，
// 否则 postgres://u:p@host/db 会落进 default 分支被当作裸路径，
// 于是启动“成功”，数据全写进一个名字古怪的 SQLite 文件里。
func resolveSQLitePath(databaseURL string) (string, error) {
	raw := strings.TrimSpace(databaseURL)
	switch {
	case raw == "":
		return "./task.db", nil
	case strings.HasPrefix(raw, "sqlite:///"):
		return strings.TrimPrefix(raw, "sqlite:///"), nil
	case strings.HasPrefix(raw, "sqlite://"):
		return strings.TrimPrefix(raw, "sqlite://"), nil
	case strings.HasPrefix(raw, "file:"):
		return raw, nil
	case strings.HasPrefix(raw, "postgres://"),
		strings.HasPrefix(raw, "postgresql://"),
		strings.HasPrefix(raw, "mysql://"):
		return "", fmt.Errorf("本项目固定使用 SQLite，不支持 %s 引擎，DATABASE_URL 请改为 sqlite:///<路径>", firstSegment(raw))
	default:
		// 裸路径，如 ./task.db
		return raw, nil
	}
}

func firstSegment(url string) string {
	if i := strings.Index(url, "://"); i > 0 {
		return url[:i]
	}
	return url
}

// applyPragmas 设置 SQLite 运行参数：WAL 提升并发、busy_timeout 抗写锁冲突、外键约束保引用完整。
func applyPragmas(db *gorm.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA wal_autocheckpoint = 1000",
	}
	for _, p := range pragmas {
		if err := db.Exec(p).Error; err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}
	return nil
}

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&model.User{},
		&model.Task{},
		&model.TaskProgress{},
		&model.Reminder{},
		&model.SystemSetting{},
		&model.TaskShare{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	// 联合唯一约束：同一任务同一用户不允许重复分享
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_task_shares_task_user ON task_shares (task_id, user_id)").Error; err != nil {
		return fmt.Errorf("create task_shares unique index: %w", err)
	}
	return nil
}

// InitAdmin 没有了, 而且**不应该**再有一个替代品。
//
// 它的作用是"库里没有管理员就建一个, 并给一个默认口令" —— 接入 OneLink 之后这件事
// 在平台上做: 建一个人、给他一个勾了躬行权限点的角色。应用侧再自动建一个管理员,
// 等于在平台的账号体系之外留一个后门账号, 而它的口令是默认值、且没有任何一处会提醒
// 有人去改它(原实现那句 warn 是唯一的提醒, 而它只出现在启动日志里)。
//
// 本地 user 表里现在只会有一种来源: 某个人从门户第一次进来时由 internal/onelink 建档。
