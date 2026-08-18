# 任务管理系统

基于 **Vue 3 + Go + SQLite** 的任务管理系统，支持任务登记、任务分配、进展跟踪、状态查询、提醒通知。

## 功能特性

- **任务登记**：创建任务，填写标题、描述、优先级、负责人、截止时间
- **任务分配**：分配/改派负责人，自动通知被分配人
- **进展跟踪**：状态流转（待处理 → 进行中 → 已完成）、进度百分比、进展备注时间线
- **状态查询**：按状态/优先级/负责人/关键词筛选、逾期标记、仪表盘统计、我的待办
- **任务提醒**：手动设置提醒时间、自动逾期提醒，站内通知中心 + IM Webhook（钉钉/企业微信/飞书）+ 邮件推送
- **逻辑删除与回收站**：仅管理员/创建者可删除任务，删除进回收站可恢复
- **权限管理**：管理员 / 普通用户两种角色

## 技术栈

| 层级 | 技术 |
| --- | --- |
| 后端 | Go 1.25 · Gin · GORM · golang-jwt · robfig/cron（`backend-go/`） |
| 前端 | Vue 3 · Vite · Element Plus · Pinia · Vue Router |
| 数据库 | SQLite（默认，零依赖，可切换 PostgreSQL） |

## 快速启动（本地/内网）

> 默认使用 **SQLite**，无需安装数据库。

### 1. 启动后端

```bash
cd task-system/backend-go

# 首次运行需下载依赖
go mod tidy        # 若本机 Go < 1.25，先执行 go get github.com/gin-gonic/gin@v1.10.1

# 复制环境变量配置（按需修改 SECRET_KEY 等）
copy .env.example .env    # Windows
cp .env.example .env      # Linux/macOS

# 启动服务（首次启动自动建表并创建默认管理员）
go run ./cmd/server
```

### 2. 启动前端

```bash
cd task-system/frontend
npm install
npm run dev
```

访问 http://localhost:5173

### 3. 切换为 PostgreSQL（可选）

```bash
cd task-system
docker compose up -d            # 启动 PostgreSQL 16
```

然后修改 `backend-go/.env`：

```ini
DATABASE_URL=postgresql+psycopg2://taskuser:taskpass@localhost:5432/taskdb
```

重启后端即可。

### 默认账号

| 角色 | 用户名 | 密码 |
| --- | --- | --- |
| 管理员 | admin | admin123 |

> 首次登录后请尽快修改默认密码。

## 提醒配置（可选）

在 `backend-go/.env` 中配置：

```ini
# IM Webhook（钉钉/企业微信/飞书）
NOTIFY_WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=xxx
NOTIFY_WEBHOOK_TYPE=dingtalk        # generic / dingtalk / wecom / feishu

# SMTP 邮件
SMTP_HOST=smtp.example.com
SMTP_PORT=465
SMTP_USER=noreply@example.com
SMTP_PASSWORD=xxx
SMTP_FROM=任务系统 <noreply@example.com>
```

## 项目结构

```
task-system/
├── backend-go/            # Go 后端（Gin，端口 8001）
│   ├── cmd/server/       # 入口
│   ├── internal/
│   │   ├── config/       # 配置
│   │   ├── database/     # SQLite 连接与初始化
│   │   ├── handler/      # HTTP 处理器
│   │   ├── middleware/   # CORS / JWT 认证
│   │   ├── model/        # GORM 模型（含软删除）
│   │   ├── scheduler/    # 定时任务（提醒扫描、逾期生成）
│   │   └── service/      # 通知推送
│   ├── go.mod
│   └── .env.example
├── frontend/
│   └── src/
│       ├── api/          # Axios 封装
│       ├── components/   # 通用组件
│       ├── layout/       # 主布局
│       ├── router/       # 路由
│       ├── stores/       # Pinia 状态
│       ├── utils/        # 常量/格式化
│       └── views/        # 页面
├── docker-compose.yml    # PostgreSQL（可选）
└── README.md
```

## API 概览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | /api/v1/auth/login | 登录 |
| POST | /api/v1/auth/register | 注册 |
| GET | /api/v1/users | 用户列表 |
| POST | /api/v1/tasks | 创建任务 |
| GET | /api/v1/tasks | 任务列表（筛选/分页） |
| GET | /api/v1/tasks/deleted | 已删除任务列表（回收站） |
| GET | /api/v1/tasks/{id} | 任务详情（含进展） |
| PATCH | /api/v1/tasks/{id} | 更新任务/状态/进度 |
| DELETE | /api/v1/tasks/{id} | 逻辑删除任务 |
| POST | /api/v1/tasks/{id}/restore | 恢复已删除任务 |
| POST | /api/v1/tasks/{id}/progress | 添加进展 |
| GET | /api/v1/reminders | 我的提醒 |
| POST | /api/v1/reminders | 设置提醒 |
| GET | /api/v1/stats/overview | 仪表盘统计 |

## 生产部署提示

- 修改 `SECRET_KEY` 为强随机值（如 `openssl rand -hex 32`）
- Go 后端可编译为单二进制：`cd backend-go && go build -o task-server ./cmd/server`
- 使用 Nginx 反向代理，前端构建产物 `npm run build` 后由 Nginx 托管，`/api` 转发至后端
- 通过 systemd / supervisor / Docker 守护后端进程
