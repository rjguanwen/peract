# 躬行（Peract）

> 纸上千言，不如躬行一件。

基于 **Vue 3 + Go + SQLite** 的任务管理系统，支持任务登记、任务分配、进展跟踪、状态查询、提醒通知。

## 功能特性

- **任务登记**：创建任务，填写标题、描述、优先级、负责人、截止时间
- **任务分配**：分配/改派负责人，自动通知被分配人
- **进展跟踪**：状态流转（待处理 → 进行中 → 已完成）、进度百分比、进展备注时间线
- **状态查询**：按状态/优先级/负责人/关键词筛选、逾期标记、仪表盘统计、我的待办
- **任务提醒**：手动设置提醒时间、自动逾期提醒，站内通知中心 + IM Webhook（钉钉/企业微信/飞书）+ 邮件推送
- **逻辑删除与回收站**：删除进回收站可恢复，回收站按用户隔离
- **账号安全**：JWT 会话吊销、改密即作废旧会话、安全问题答案哈希存储、找回密码限流与防枚举、登录失败锁定
- **权限与邀请**：管理员 / 普通用户两种角色，管理员可开关自助注册、签发一次性邀请链接

## 技术栈

| 层级 | 技术 |
| --- | --- |
| 后端 | Go 1.25 · Gin · GORM · golang-jwt · robfig/cron（`backend-go/`） |
| 前端 | Vue 3 · Vite · Element Plus · Pinia · Vue Router |
| MCP | Go + JSON-RPC 2.0（`task-mcp/`，供 AI agent 调用） |
| 数据库 | SQLite（纯 Go 驱动，零依赖；架构上固定，不做多数据库适配） |

## 快速启动（本地/内网）

> 使用 **SQLite**，单文件落盘，不需要安装任何数据库服务。

### 1. 启动后端

```bash
cd task-system/backend-go

# 首次运行需下载依赖
go mod tidy

# 复制环境变量配置（按需修改 SECRET_KEY 等）
copy .env.example .env    # Windows
cp .env.example .env      # Linux/macOS

# 启动服务（首次启动自动建表并创建默认管理员）
go run ./cmd/server
```

后端监听 `http://localhost:8001`。

### 2. 启动前端

```bash
cd task-system/frontend
npm install
npm run dev
```

访问 http://localhost:5173

### 默认账号

| 角色 | 用户名 | 密码 |
| --- | --- | --- |
| 管理员 | admin | admin123 |

由 `INIT_ADMIN_USERNAME` / `INIT_ADMIN_PASSWORD` / `INIT_ADMIN_EMAIL` 决定，仅在该用户名不存在时创建。
**首次登录后请尽快修改默认密码**；生产环境保留默认口令会在启动时告警。

## 配置

全部配置项集中在 `backend-go/.env.example`，每一项都有注释和默认值，此处只列容易被忽略的：

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` 下弱 `SECRET_KEY` 会直接拒绝启动 |
| `SECRET_KEY` | 占位值 | 必须改为强随机值（`openssl rand -hex 32`），长度 < 32 视为弱密钥 |
| `DATABASE_URL` | `sqlite:///./task.db` | 只支持 SQLite，见下节 |
| `APP_BASE_URL` | `http://localhost:5173` | 拼重置密码 / 邀请邮件里的链接，部署后必须改成对外地址 |
| `TRUSTED_PROXIES` | 空 | 放在 Nginx 之后**必须**填写，否则限流可被伪造 `X-Forwarded-For` 绕过 |
| `CORS_ALLOW_ORIGINS` | `*` | 生产请收敛为具体前端域名 |

### 数据库

本项目技术架构为 **Vue + Go + SQLite**。`DATABASE_URL` 支持三种写法：

```ini
DATABASE_URL=sqlite:///./task.db    # 推荐
DATABASE_URL=./task.db              # 裸路径亦可
DATABASE_URL=file:/data/task.db     # 直接透传给驱动
```

驱动为 `glebarez/sqlite`（基于 `modernc.org/sqlite`），纯 Go 实现，**无需 CGO、无需本地 C 工具链**，
可用 `CGO_ENABLED=0` 直接交叉编译。

填其他数据库的连接串（如 `postgres://`、`mysql://`）会在启动时**明确报错退出**，
而不是被当成裸路径静默建出一个同名的 SQLite 文件。

## 提醒与通知

- **到期提醒**：调度器每 30 秒扫描一次到点提醒，先抢占落库再投递，单轮最多 100 条（防积压撑爆内存）
- **逾期提醒**：每 10 分钟扫描逾期任务并生成提醒，同一任务每天最多一条
- **外部通道**：IM Webhook 与 SMTP 走**异步队列**（`NOTIFY_WORKERS` / `NOTIFY_QUEUE_SIZE`），不阻塞 HTTP 请求；队列满则丢弃并记日志
- **超时**：SMTP 建连 / 会话、Webhook 请求均有独立超时，外部服务卡死不会拖垮后端
- **限流**：登录失败按「来源 IP + 用户名」计数，找回密码按「来源 IP + 邮箱」计数，超限返回 `429` 并带 `Retry-After`

> 限流计数保存在**进程内存**，仅对单实例部署有效；多实例横向扩容需换成 Redis 等共享存储。

## API 概览

统一前缀 `/api/v1`。错误响应统一为 `{"detail": "..."}`。

### 公开

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | /auth/login | 登录（失败超限返回 429，带 `Retry-After`） |
| POST | /auth/register | 注册（可被管理员关闭，或需邀请码） |
| POST | /auth/logout | 登出并吊销当前令牌 |
| GET | /auth/forgot | 按邮箱查询找回题目（防枚举，不区分「不存在」与「已停用」） |
| POST | /auth/forgot/reset | 凭安全问题答案重置（答案比对走哈希） |
| POST | /auth/forgot/send | 发送重置邮件（防枚举；SMTP 未配置时生产返回 503，非生产环境直发链接仅供联调） |
| POST | /auth/reset | 凭一次性重置令牌改密（令牌用后即废） |
| GET | /auth/invite/info | 查询邀请链接信息 |

### 需登录

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /auth/me | 当前用户 |
| PUT | /auth/password | 修改密码（成功后旧会话全部作废） |
| GET | /auth/security | 查询安全问题设置状态 |
| PUT | /auth/security | 设置安全问题与答案（答案以 bcrypt 存储） |
| PUT | /profile | 修改资料 |
| PUT | /profile/avatar | 上传头像（校验魔数，不只信扩展名） |
| GET | /users | 用户列表 |
| GET | /tasks | 任务列表（`status`/`priority`/`assignee_id`/`keyword`/`overdue_only`/`mine`/分页） |
| POST | /tasks | 创建任务 |
| GET | /tasks/deleted | 回收站列表 |
| GET | /tasks/{id} | 任务详情（含进展） |
| PATCH | /tasks/{id} | 局部更新（契约见下） |
| DELETE | /tasks/{id} | 逻辑删除 |
| POST | /tasks/{id}/restore | 从回收站恢复（仅本人/管理员） |
| POST | /tasks/{id}/progress | 添加进展 |
| GET | /reminders | 我的提醒 |
| GET | /reminders/unread-count | 未读数，返回 `{"count": n}` |
| POST | /reminders | 设置提醒 |
| POST | /reminders/read-all | 全部标记已读，返回 `{"updated": n}` |
| POST | /reminders/{id}/read | 标记单条已读 |
| GET | /stats/overview | 仪表盘统计 |

### 需管理员

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | /users | 创建用户 |
| PATCH | /users/{id} | 修改用户（角色、启停、重置口令） |
| GET | /admin/settings | 读取系统设置 |
| PUT | /admin/settings/registration | 开关自助注册 |
| POST | /admin/invites | 生成邀请链接 |
| GET | /admin/invites | 邀请列表 |
| POST | /admin/invites/{id}/revoke | 撤销邀请 |

### PATCH 契约（重要）

`PATCH /tasks/{id}` 中字段为 `null` 或**不出现**都表示「本次不改」。要清空 `assignee_id` 或 `due_date`，必须显式传标志位：

```json
{ "clear_assignee": true }
{ "clear_due_date": true }
```

`clear_assignee` 与 `assignee_id` 同时出现（`clear_due_date` 同理）视为冲突，返回 `400`。

## 升级与安全注意事项

- **安全问题答案改为哈希存储**：升级后存量明文答案不再参与校验，相关账号需重新设置安全问题；找回接口会置 `answer_legacy: true` 且不再返回题目，前端据此提示用户
- **改密即吊销会话**：修改密码后，此前签发的所有访问令牌立即失效（含被泄露的旧令牌），需重新登录
- **令牌带 `jti`**：同一秒内重新登录不会撞上上一条令牌的吊销黑名单
- **找回密码依赖 SMTP**：`SMTP_HOST`/`SMTP_USER`/`SMTP_FROM` 未配齐时，生产环境的 `/auth/forgot/send` 一律返回 503，
  绝不会把带有效令牌的重置链接回给调用方；因此上线前必须配好 SMTP，否则用户只能由管理员重置口令

## 项目结构

```
task-system/
├── backend-go/            # Go 后端（Gin，端口 8001）
│   ├── cmd/server/        # 入口（含优雅停机装配）
│   ├── internal/
│   │   ├── config/        # 环境变量加载与校验
│   │   ├── database/      # SQLite 连接、PRAGMA、迁移、初始管理员
│   │   ├── handler/       # HTTP 处理器与路由注册
│   │   ├── middleware/    # CORS / JWT 认证 / 限流 / 体积限制
│   │   ├── model/         # GORM 模型（含软删除、自定义时间类型）
│   │   ├── scheduler/     # 定时任务（提醒扫描、逾期生成）
│   │   └── service/       # 通知推送（异步队列、SMTP、Webhook）
│   ├── go.mod
│   └── .env.example
├── frontend/
│   └── src/
│       ├── api/           # Axios 封装
│       ├── components/    # 通用组件
│       ├── layout/        # 主布局
│       ├── router/        # 路由
│       ├── stores/        # Pinia 状态
│       ├── utils/         # 常量/格式化
│       └── views/         # 页面
├── task-mcp/              # MCP Server，把上述 API 暴露给 AI agent
└── README.md
```

## 开发与测试

```bash
cd task-system/backend-go
go build ./...
go vet ./...
go test -race -count=1 ./...     # handler 为 HTTP 级集成测试，耗时较长
```

测试使用临时 SQLite 文件与全链路 `httptest`，不依赖外部服务。

## 生产部署提示

- 设置 `APP_ENV=production`，并修改 `SECRET_KEY` 为强随机值（如 `openssl rand -hex 32`）
- 修改 `INIT_ADMIN_PASSWORD`，不要沿用 `admin123`
- 收敛 `CORS_ALLOW_ORIGINS`，并正确配置 `TRUSTED_PROXIES`（否则限流与登录锁定形同虚设）
- 编译为单二进制：`cd backend-go && go build -o task-server ./cmd/server`
- Nginx 反向代理：前端 `npm run build` 产物由 Nginx 托管，`/api` 转发至后端
- 进程守护用 systemd / supervisor / Docker；停止时发送 `SIGTERM`，后端会按
  「停调度器 → 排空通知队列 → 等在途请求（上限 `SHUTDOWN_TIMEOUT_SECONDS`）」的顺序收尾
- SQLite 以 WAL 模式运行，备份需同时复制 `task.db`、`task.db-wal`、`task.db-shm`，
  或直接用 `sqlite3 task.db ".backup 目标路径"`，不要只拷主文件
