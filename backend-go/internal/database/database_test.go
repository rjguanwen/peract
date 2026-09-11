package database

import (
	"os"
	"strings"
	"testing"

	"taskbackend/internal/config"
)

// 本项目架构上固定 SQLite，这几份测试就是该决策的边界：
// SQLite 的各种写法都要能解析出路径，其他引擎的连接串必须在启动阶段就被拒。

func TestResolveSQLitePath(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{"留空回落默认文件", "", "./task.db", false},
		{"三斜杠形式", "sqlite:///./task.db", "./task.db", false},
		{"双斜杠形式", "sqlite://./task.db", "./task.db", false},
		{"内存库", "sqlite:///:memory:", ":memory:", false},
		{"file 前缀原样透传", "file:/data/task.db?_journal_mode=WAL", "file:/data/task.db?_journal_mode=WAL", false},
		{"裸的 Unix 路径", "/var/lib/peract/task.db", "/var/lib/peract/task.db", false},
		// 含冒号与反斜杠的 Windows 路径不能被误当成 URL scheme 而拒掉
		{"裸的 Windows 路径", `D:\data\task.db`, `D:\data\task.db`, false},
		{"带盘符的绝对路径", `D:/data/task.db`, `D:/data/task.db`, false},
		{"首尾空白被裁掉", "  sqlite:///./task.db  ", "./task.db", false},
		{"PostgreSQL 必须被拒", "postgres://u:p@localhost:5432/db", "", true},
		{"postgresql 全称同样被拒", "postgresql://u:p@localhost:5432/db", "", true},
		{"MySQL 必须被拒", "mysql://u:p@localhost:3306/db", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSQLitePath(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("非 SQLite 的 DSN 应被拒绝，实际解析成 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("合法 SQLite 路径被误拒: %v", err)
			}
			if got != tc.want {
				t.Fatalf("解析结果 = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestOpenRejectsOtherEngines 盯的是失败模式本身：非 SQLite 的 DSN 必须报错，
// 而且不能退化成「把整串连接当成文件名」就地建库——那会让服务看起来启动成功了，
// 数据却全写进一个名字古怪的 SQLite 文件里。
func TestOpenRejectsOtherEngines(t *testing.T) {
	dsn := "postgres://u:p@localhost:5432/peractdb"

	db, err := Open(&config.Config{DatabaseURL: dsn})
	if err == nil {
		t.Fatalf("必须拒绝非 SQLite 的 DATABASE_URL，实际拿到了连接 %v", db)
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("错误信息应点明被拒的引擎，方便运维排查: %v", err)
	}
	if _, statErr := os.Stat(dsn); statErr == nil {
		t.Fatalf("在磁盘上建出了名为 %q 的 SQLite 文件，说明连接串被当成了裸路径", dsn)
	}
}
