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
- **身份与权限**：账号、口令、组织、菜单与角色授权全部由 **OneLink** 接管；躬行侧只保留一份本地档案与一份权限快照

## 身份由 OneLink 接管

躬行**没有登录页**。用户从 OneLink 门户点「躬行」卡片进来：

```
门户点卡片 → 302 带一次性票据 → 后端 /sso/landing 消费票据
          → 换出应用会话(种一张 HttpOnly cookie) → 前端开始跑
```

由此带来几条必须知道的约定：

- **会话是 cookie, 不是前端持有的令牌。** 前端 `stores/auth.js` 里连一个 token 字段都没有 —— 而这是有意的：凭据进了 localStorage 就等于交给每一个 XSS。
- **账号与口令在平台上。** 改密、找回、锁定、验证码都在门户；躬行的库里没有口令哈希（历史列还在，但没有写入方，见 `model.User` 的注释）。
- **权限点必须登记在 OneLink 上。** 归属是 `task-system` 这个应用，清单见 `deploy/perms.manifest.json`（人读 + `onelinkctl` 校验）与 `deploy/seed-perms.sql`（落库）。**不要**用 `POST /api/v1/admin/permissions` 登记 —— 那一条的归属取自会话（在门户里就是平台自己），建出来的行全落在 `app_id=1` 下，现象是"权限点在管理台看得见、应用里按钮不出现、且不出现的那一侧没有任何报错"。正确入口是应用作用域路由 `POST /api/v1/admin/apps/:id/permissions`，或这份种子 SQL。
- **角色的载体是平台的角色授权**（`sys_user_role.app_id`），应用侧没有 role 字段。给一个人授权是三步：建角色 → 勾权限点 → 把角色授给他；第三步最容易漏，少了它"角色建好了、权限勾上了、保存也成功，而那个人进去什么都没有"。
- **权限码分两类**：`M`（菜单）决定前端摆不摆入口，`B`（按钮/接口）是服务端 `RequirePerm` 的判据。两者的清单必须逐字对齐（`deploy/*` ↔ `internal/handler/handler.go` 顶部常量 ↔ `frontend/src/utils/menu.js` 与 `router/index.js`）。

## 技术栈

| 层级 | 技术 |
| --- | --- |
| 后端 | Go 1.25 · Gin · GORM · robfig/cron · OneLink Go SDK（`backend-go/`） |
| 前端 | Vue 3 · Vite · Element Plus · Pinia · Vue Router |
| MCP | Go + JSON-RPC 2.0（`task-mcp/`，供 AI agent 调用）**注：尚未适配 OneLink 认证，见文末** |
| 数据库 | SQLite（纯 Go 驱动，零依赖；架构上固定，不做多数据库适配） |

## 快速启动（本地/内网）

> 使用 **SQLite**，单文件落盘，不需要安装任何数据库服务。

### 1. 启动后端

```bash
cd task-system/backend-go

# 首次运行需下载依赖
go mod tidy

# 复制环境变量配置，然后填上 ONELINK_BASE_URL / ONELINK_PORTAL_URL / ONELINK_APP_SECRET
copy .env.example .env    # Windows
cp .env.example .env      # Linux/macOS

# 启动服务（首次启动自动建表；**不建账号** —— 账号在平台上）
go run ./cmd/server
```

> 三项 OneLink 配置缺一不可：业务路由全部在会话守卫之后，少了它们启动时会告警并**不挂任何业务路由**
> （只有 `/uploads` 静态资源可用）。生产环境缺配置直接拒绝启动。

后端监听 `http://localhost:8001`。

### 2. 启动前端

```bash
cd task-system/frontend
npm install
npm run dev
```

访问 http://localhost:5177

### 怎么进去

**从 OneLink 门户点「躬行」卡片。** 应用侧没有默认账号，也没有登录页。

第一次进来的人会在本地自动建档（按平台用户主键 upsert，见 `internal/onelink/profile.go`），
所以"应用里还没有这个人"不需要提前做什么 —— 但他能不能做事取决于平台侧有没有给他授权。

本地开发时（还没搭起 OneLink）可以把 `VITE_ONELINK_PORTAL_URL` 留空，
那样会话失效时前端只提示、不跳转；但业务路由本身仍然要求一张有效会话。

## 配置

全部配置项集中在 `backend-go/.env.example`，每一项都有注释和默认值，此处只列容易被忽略的：

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `APP_ENV` | `development` | `production` 下缺 OneLink 配置、或 CORS 配了 `*` 会直接拒绝启动 |
| `ONELINK_BASE_URL` | 空 | 平台根地址。**生产必填**，见上节 |
| `ONELINK_PORTAL_URL` | 空 | 门户地址。会话失效时把人送回这里。**生产必填** |
| `ONELINK_APP_SECRET` | 空 | 应用密钥。**只在服务端**，不进前端产物/日志/版本库。**生产必填** |
| `ONELINK_APP_CODE` | `task-system` | 必须与 OneLink 里登记的一致；它同时是权限码前缀 |
| `ONELINK_ALIVE_INTERVAL_SECONDS` | `60` | 会话存活轮询周期，**直接决定单点登出的可感知滞后**；下限 5 秒 |
| `DATABASE_URL` | `sqlite:///./task.db` | 只支持 SQLite，见下节 |
| `APP_BASE_URL` | `http://localhost:5177` | 对外前端地址（用于日志与运维对照） |
| `TRUSTED_PROXIES` | 空 | 放在 Nginx 之后**必须**填写，否则来源 IP 可被伪造 `X-Forwarded-For` 改写 |
| `CORS_ALLOW_ORIGINS` | `http://localhost:5177` | **不要用 `*`**：会话是凭据型 cookie，浏览器不会在带凭据的跨域请求上接受通配来源，跨域登录会静默失败 |

前端另有一个构建期变量 `VITE_ONELINK_PORTAL_URL`（门户地址，会话失效时跳转用）。
它**不是**密钥，但必须与后端的 `ONELINK_PORTAL_URL` 指向同一个门户。

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

> 登录失败与找回密码的限流随自建认证一起**搬到了 OneLink**（平台侧还有验证码与账号锁定）。
> 应用侧不再持有那两组计数器，因此也没有"多实例需要共享存储"这个问题。

## API 概览

统一前缀 `/api/v1`。错误响应统一为 `{"detail": "..."}`。

**没有「公开」那一组了。** 登录、注册、找回密码、邀请都在平台上；应用侧唯一不经认证的入口是
`/sso/landing`（它必须在守卫之外 —— 那正是"还没有会话"时唯一要能走到的地址）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/sso/landing` | 门户带票据送回来的落地页；消费票据、建会话、种 cookie。无票据时回 401 |
| POST | `/logout` | 登出：删除本地会话行并清 cookie。回 302（它给浏览器点，不回 JSON） |

### 需登录（会话 cookie）

下表每一行的**权限点**是它真正要求的判据。`M` 是菜单码（前端显隐），`B` 是接口码
（`onelink.RequirePerm`）—— 两者都在 `deploy/perms.manifest.json` 里登记。

| 方法 | 路径 | 权限点 | 说明 |
| --- | --- | --- | --- |
| GET | /auth/me | —（只要登录） | 当前档案 + 权限快照。**不能挂业务权限点**：前端靠 200/401 判断会话，403 会被当成会话失效而陷入跳转循环 |
| PUT | /profile | — | 只改个性签名。姓名/邮箱/头像由 OneLink 维护（改了会被同步覆盖回去） |
| PUT | /profile/avatar | — | 上传头像（校验魔数，不只信扩展名） |
| GET | /users | `task-system:user:view` | 本地档案列表（指派任务要选人，普通成员也需要） |
| PATCH | /users/{id} | `task-system:user:manage` | 改本地档案的展示字段与"能否被指派"；账号与角色不在这里 |
| GET | /tasks | `task-system:task:list` | 任务列表（`status`/`priority`/`assignee_id`/`keyword`/`overdue_only`/`mine`/分页） |
| GET | /tasks?visibility=all | `task-system:task:list-all` | 看全部任务（**数据范围开关**，没有它只看得到与自己相关的） |
| POST | /tasks | `task-system:task:create` | 创建任务 |
| GET | /tasks/deleted | `task-system:task:restore` | 回收站列表 |
| GET | /tasks/{id} | `task-system:task:list` | 任务详情（含进展） |
| PATCH | /tasks/{id} | `task-system:task:update` | 局部更新（契约见下）。数据侧仍要求创建者/负责人 |
| DELETE | /tasks/{id} | `task-system:task:delete` | 逻辑删除 |
| POST | /tasks/{id}/restore | `task-system:task:restore` | 从回收站恢复 |
| POST | /tasks/{id}/progress | `task-system:task:update` | 添加进展 |
| GET | /tasks/{id}/shares | `task-system:task:list` | 分享列表 |
| POST | /tasks/{id}/shares | `task-system:task:share` | 分享任务（仅创建者） |
| DELETE | /tasks/{id}/shares/{shareId} | `task-system:task:share` | 撤销分享（仅创建者） |
| GET | /reminders | `task-system:task:list` | 我的提醒 |
| GET | /reminders/unread-count | `task-system:task:list` | 未读数，返回 `{"count": n}` |
| POST | /reminders | `task-system:reminder:manage` | 设置提醒 |
| POST | /reminders/read-all | `task-system:task:list` | 全部标记已读，返回 `{"updated": n}` |
| POST | /reminders/{id}/read | `task-system:task:list` | 标记单条已读 |
| GET | /stats/overview | `task-system:dashboard:view` | 仪表盘统计 |

**功能权限与数据权限是两层，不要混**：上面每一行判的是"能不能用这个入口"（由 OneLink 的
角色授权决定）；而"这条任务是不是我的"仍留在本地（`creator_id` / `assignee_id` /
`task_shares`），只是判定依据换成了权限点（例如 `canModify` 先看有没有
`task-system:task:list-all`，再看是不是创建者/负责人）。

### 已删除的接口

`POST /auth/login`、`/auth/register`、`/auth/logout`、`/auth/password`、`/auth/security`、
`/auth/forgot*`、`/auth/reset`、`/auth/invite/*`、`POST /users`、`/admin/*` —— 它们
随"应用自己管账号"这件事一起搬到了平台上。留着任何一条都等于给同一个身份开第二道门。

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
│   ├── cmd/server/        # 入口（OneLink 装配 + 优雅停机）
│   ├── internal/
│   │   ├── config/        # 环境变量加载与校验
│   │   ├── database/      # SQLite 连接、PRAGMA、迁移
│   │   ├── handler/       # HTTP 处理器与路由注册（权限点挂在路由上）
│   │   ├── middleware/    # CORS / 体积限制（**不含认证**：认证在 onelink 包）
│   │   ├── model/         # GORM 模型（含软删除、自定义时间类型）
│   │   ├── onelink/       # OneLink 适配层：SQLite 会话存储 / Gin 守卫桥接 / 本地档案
│   │   ├── scheduler/     # 定时任务（提醒扫描、逾期生成）
│   │   └── service/       # 通知推送（异步队列、SMTP、Webhook）
│   ├── go.mod             # 用 replace 指向 OneLink SDK（本地路径，见文件内注释）
│   └── .env.example
├── frontend/
│   └── src/
│       ├── api/           # Axios 封装（withCredentials，无令牌注入）
│       ├── components/    # 通用组件
│       ├── layout/        # 主布局（菜单按权限快照过滤）
│       ├── router/        # 路由（meta.perm 用菜单码）
│       ├── stores/        # Pinia 状态（**不存任何凭据**）
│       ├── utils/         # 常量/格式化、menu.js（菜单清单）、portal.js（回门户）
│       └── views/         # 页面
├── deploy/                # OneLink 侧登记清单：perms.manifest.json + seed-perms.sql
├── task-mcp/              # MCP Server，把上述 API 暴露给 AI agent
└── README.md
```

## 开发与测试

```bash
cd task-system/backend-go
go build ./...
go vet ./...
go test -race -count=1 ./...     # handler 为 HTTP 级集成测试，耗时较长

cd ../frontend
npm run build
```

测试使用临时 SQLite 文件与全链路 `httptest`，不依赖外部服务。

**会话在测试里是怎么来的**：直接写进 SDK 的 `Store`，而不是走一次真实兑换 —— 走真实兑换
需要一个假平台（签票据、RS256、JWKS），而那会把 SDK 的协议细节搬进业务测试，那些细节已经由
SDK 自己的用例覆盖。跳过的是"会话最初怎么来的"，守卫之后走的路径（读 Store → 判到期 →
建 Principal → 挂权限）与生产完全一致。理由写在 `handler/testutil_test.go` 的 `sessionFor` 上。

## 生产部署提示

- 设置 `APP_ENV=production`，并配齐 `ONELINK_BASE_URL` / `ONELINK_PORTAL_URL` / `ONELINK_APP_SECRET`
  （缺任何一项都会拒绝启动 —— 起得来但谁都进不去比起不来更难排查）
- 在 OneLink 里登记 `deploy/perms.manifest.json` 里的权限点，并给使用者授角色
- **不要**给 `CORS_ALLOW_ORIGINS` 配 `*`：会话是凭据型 cookie，浏览器不会在带凭据的跨域请求上
  接受通配来源，跨域登录会静默失败（生产环境会直接拒绝启动）
- 正确配置 `TRUSTED_PROXIES`，否则来源 IP 可被伪造 `X-Forwarded-For` 改写
- 编译为单二进制：`cd backend-go && go build -o task-server ./cmd/server`
- Nginx 反向代理：前端 `npm run build` 产物由 Nginx 托管，**`/api`、`/sso`、`/logout` 三条都要转发至后端**。
  漏了后两条的表现是"从门户点卡片进来落在前端 404 页"，而那看起来像门户配错了应用入口地址。
  另外这两个地址必须与前端**同源**（会话 cookie 是按来源下发的），跨域部署时 cookie 的
  `SameSite` 需要一并调整
- 进程守护用 systemd / supervisor / Docker；停止时发送 `SIGTERM`，后端会按
  「停调度器 → 排空通知队列 → 等在途请求（上限 `SHUTDOWN_TIMEOUT_SECONDS`）」的顺序收尾
- SQLite 以 WAL 模式运行，备份需同时复制 `task.db`、`task.db-wal`、`task.db-shm`，
  或直接用 `sqlite3 task.db ".backup 目标路径"`，不要只拷主文件

## 未完成 / 已知影响

### 1. `task-mcp` 目前**不可用**

它用 `MCP_USERNAME` / `MCP_PASSWORD` 调 `POST /api/v1/auth/login` 换自签 JWT，而那条接口
已经删掉了（口令只存在于 OneLink，应用侧连校验它的能力都没有）。

按需求这一块**暂不改动**，所以现在的状态是：`task-mcp` 每次调用都会拿到 404。
修它的路有两条（见接入规范的"机器客户端"一节）：

- **推荐**：走 OAuth2 授权码，人工授权一次拿到 `refresh_token` 并持久化，每次续期后把轮换出的
  新 `refresh_token` 写回 —— 平台没有 `client_credentials`，也没有 `password` grant；
- 或者在 OneLink 建一个服务账号，管理员为它在躬行侧单独开一条认证通道（相当于维护两套认证）。

### 2. 菜单结构仍留在前端

`frontend/src/utils/menu.js` 是本地的菜单清单，可见性由权限码决定。这是接入规范允许的两种
用法之一（平台管"谁能看"、应用管"看什么"）。

要切成"菜单结构也由平台管"，需要三步：
1. 平台侧已经具备：`/api/v1/admin/apps/:id/permissions` 能录 `M` 型菜单（`deploy/perms.manifest.json` 里已经录了），
   `GET /open/v1/menu/tree` 能读回来（SDK 的 `Client.Menus(ctx, accessToken)`）；
2. 后端加一条 `GET /api/v1/auth/menus`，用会话里的访问令牌调 SDK 的 `Menus()`；
3. 前端把 `menu.js` 的清单换成那份树（并按 `path` 过滤掉路由里没有的项），
   路由表仍然要静态存在 —— Vue Router 的组件必须在构建期可解析。

现在没做的原因是第 3 步需要同时处理"平台一条菜单都没登记时侧边栏为空"这个状态，
而它容易被误读成权限配错了。

### 3. 库里那六列认证遗留字段

`users` 表上的 `hashed_password` / `role` / `password_hint` / `security_question` /
`security_answer` / `password_changed_at` 已经不再映射（见 `model.User` 的注释）。
GORM 的 AutoMigrate 从不删列，所以它们会一直留着 —— 确认不再需要回溯后手工 DROP 即可。
