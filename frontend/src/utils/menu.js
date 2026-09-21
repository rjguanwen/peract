// 侧边栏菜单。
//
// **结构留在前端, 可见性由 OneLink 决定** —— 这不是偷懒, 而是接入协议定下来的分工:
// 平台给业务应用的开放接口只到"权限点"这一层(兑换响应里的 permissions, 以及
// GET /open/v1/menu/tree 那棵由平台登记的树)。哪一条菜单指向哪个前端路由, 属于应用的
// UI 结构, 不属于身份平台 —— 把它塞进平台的 sys_permission 只会让平台的门户菜单树里
// 多出一批点不开的项。
//
// 判据是**权限码**, 而且用的是**菜单码(M 型)**, 不是接口码(B 型)。
//
// 这两者是平台上两种不同的东西, 而不是同一个码的两种写法:
//   M(菜单) —— 能不能看见这个入口;
//   B(按钮/接口) —— 能不能做这件事。
// 一个人可以"看得见任务列表但不能新建"(只有 task-system:task 而没有 :create), 那正是
// 权限点的意义所在。拿 B 码来判菜单可见性会把这种组合压扁成"要么全有要么全无"。
//
// 三份清单必须逐字对齐, 改一处就要改三处:
//   1. 这个文件(前端渲染哪些入口);
//   2. router/index.js 的 meta.perm(能不能进这个页面);
//   3. deploy/perms.manifest.json 与 deploy/seed-perms.sql(平台实际认了哪些码)。
// 第 3 处漏了的表现最隐蔽: 前端在判一个平台上不存在的码 —— 于是那个菜单**永远不出现**,
// 而服务端不会报任何错(它压根没被请求过)。
//
// 两条纪律:
//   1. 空快照 = 什么都不显示。它不是"没有权限"的兜底 —— 兜底是服务端每个接口自己的
//      RequirePerm, 这里只决定摆不摆入口;
//   2. path 必须与 router/index.js 里的路径逐字一致。写错一个字符的表现是
//      "菜单点进去是空白页", 而它看起来像权限问题(因为另一个菜单是好的)。

/** @type {{path: string, title: string, icon: string, perm: string}[]} */
export const menus = [
  { path: '/', title: '仪表盘', icon: 'Odometer', perm: 'task-system:dashboard' },
  { path: '/tasks', title: '任务管理', icon: 'Tickets', perm: 'task-system:task' },
  { path: '/tasks/deleted', title: '回收站', icon: 'Delete', perm: 'task-system:task-trash' },
  { path: '/users', title: '用户档案', icon: 'User', perm: 'task-system:user' },
]

/**
 * 按权限码过滤出可见菜单。
 *
 * 超级管理员短路成"全部可见": 平台侧对超管的判定是"该应用全部启用的权限点", 而快照里
 * 可能**恰好**不含某个码(超管本就不需要授权行)。不短路的话, 超管会看不到自己的菜单,
 * 而他能调用的接口其实一个都不少。
 *
 * @param {string[]} permissions /auth/me 回来的权限快照
 * @param {boolean} isSuper
 */
export function visibleMenus(permissions, isSuper) {
  if (isSuper) return menus
  const held = new Set(permissions || [])
  return menus.filter((m) => held.has(m.perm))
}
