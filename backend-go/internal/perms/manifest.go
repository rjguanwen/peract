// Package perms 是躬行在 OneLink 上登记的菜单与权限点清单 —— 运行期的那一份。
//
// 为什么清单要进代码, 而不是继续只留 deploy/perms.manifest.json:
//
//	平台有两条送达路径(应用在部署时上报 / 平台在管理台上拉取), 而拉取那条要求应用
//	**在运行期**能回答"我的清单是什么" —— 它要挂一个端点出去。那份 JSON 住在
//	deploy/ 下(它是部署产物, 与 seed-perms.sql 同处), 而它在 Go module 目录之外,
//	go:embed 够不到。于是运行期只有两种活法: 依赖"进程的工作目录里正好有那个文件",
//	或者把清单写进代码。后者更硬 —— 清单变成编译期常量, 它与二进制同版本, 而"部署时
//	少拷了一个文件"这种事故不会让权限清单悄悄变成空的。
//
// 代价是同一份清单有了两个载体, 而这个代价由 manifest_test.go 付清: 它逐条比对这里的
// 字面量与 ../../deploy/perms.manifest.json, 任何一边改了另一边没改都会红, 并且指名
// 是第几条的哪个字段。
//
// 两个载体各自的角色(改清单时两边都要动):
//
//	deploy/perms.manifest.json  部署产物与**给人读的那一份**; deploy/seed-perms.sql
//	                            由它生成, onelinkctl perm-export 校验它, 上报(CI)读它;
//	internal/perms/manifest.go  运行期副本, 供 /onelink/perm-manifest 端点应答平台拉取。
//
// 顺序也有含义: 清单里的顺序就是平台落库时的兜底排序(每条自带 sort, 这里只是让 diff
// 读起来与平台上的样子一致)。
package perms

import (
	onelinksdk "github.com/onelink/platform/sdk/go/onelink"
)

// Manifest 躬行登记的 15 条菜单与权限点。
//
// 类型分四档(D 目录 / M 菜单 / B 按钮 / A 纯接口), 这里只用到 M 与 B: 躬行没有独立
// 的目录层, 而每一个按钮型权限点都对应一道真实存在的接口守卫(见 handler 里那一组
// RequirePerm 调用) —— 登记一个没有接口的 A 型权限点等于给平台留一条查不到出处的记录。
//
// 三个开关(visible/keepAlive/isFrame)一个都不写: 留空时平台补成 1/1/0(可见、缓存、
// 非外链), 这正是躬行所有菜单的档位。显式写出来只会让这份清单里多出 15 行噪声。
var Manifest = []onelinksdk.PermDecl{
	{Key: "task-system:dashboard", Name: "仪表盘", Type: "M", Path: "/", Component: "Dashboard", Icon: "Odometer", Sort: sortOf(1)},
	{Key: "task-system:dashboard:view", Name: "查看仪表盘", Type: "B", Parent: "task-system:dashboard", Method: "GET", URI: "/api/v1/stats/overview", Sort: sortOf(1)},

	{Key: "task-system:task", Name: "任务管理", Type: "M", Path: "/tasks", Component: "TaskList", Icon: "Tickets", Sort: sortOf(2)},
	{Key: "task-system:task:list", Name: "查看任务", Type: "B", Parent: "task-system:task", Method: "GET", URI: "/api/v1/tasks", Sort: sortOf(1)},
	// list-all 是**数据范围**的开关而不是另一个入口: 列表还是同一个列表, 持有它的人
	// 看得到全部任务。它的 uri 因此带上了那个决定范围 query —— 平台侧"这个权限点管哪
	// 条接口"的答案就是它, 而一个只写 /api/v1/tasks 的声明会让两种范围看起来是同一件事。
	{Key: "task-system:task:list-all", Name: "查看全部任务", Type: "B", Parent: "task-system:task", Method: "GET", URI: "/api/v1/tasks?visibility=all", Sort: sortOf(2)},
	{Key: "task-system:task:create", Name: "创建任务", Type: "B", Parent: "task-system:task", Method: "POST", URI: "/api/v1/tasks", Sort: sortOf(3)},
	{Key: "task-system:task:update", Name: "编辑任务", Type: "B", Parent: "task-system:task", Method: "PATCH", URI: "/api/v1/tasks/:id", Sort: sortOf(4)},
	{Key: "task-system:task:delete", Name: "删除任务", Type: "B", Parent: "task-system:task", Method: "DELETE", URI: "/api/v1/tasks/:id", Sort: sortOf(5)},
	{Key: "task-system:task:share", Name: "分享任务", Type: "B", Parent: "task-system:task", Method: "POST", URI: "/api/v1/tasks/:id/shares", Sort: sortOf(6)},
	{Key: "task-system:reminder:manage", Name: "管理提醒", Type: "B", Parent: "task-system:task", Method: "POST", URI: "/api/v1/reminders", Sort: sortOf(7)},

	{Key: "task-system:task-trash", Name: "回收站", Type: "M", Path: "/tasks/deleted", Component: "TaskTrash", Icon: "Delete", Sort: sortOf(3)},
	{Key: "task-system:task:restore", Name: "查看与恢复已删任务", Type: "B", Parent: "task-system:task-trash", Method: "POST", URI: "/api/v1/tasks/:id/restore", Sort: sortOf(1)},

	{Key: "task-system:user", Name: "用户档案", Type: "M", Path: "/users", Component: "UserManage", Icon: "User", Sort: sortOf(4)},
	{Key: "task-system:user:view", Name: "查看用户档案", Type: "B", Parent: "task-system:user", Method: "GET", URI: "/api/v1/users", Sort: sortOf(1)},
	{Key: "task-system:user:manage", Name: "编辑用户档案", Type: "B", Parent: "task-system:user", Method: "PATCH", URI: "/api/v1/users/:id", Sort: sortOf(2)},
}

// sortOf 取一个 int 的地址。
//
// PermDecl.Sort 是指针: 平台侧那一列可以为空(空表示"按清单顺序落库"), 而 0 是一个
// 合法值(排最前) —— 两者必须能区分。这里的每一行都显式给了值, 所以这个 helper 只是
// 让 15 行字面量不必各写一个局部变量。
func sortOf(n int) *int { return &n }
