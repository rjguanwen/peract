import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { goToPortal } from '../utils/portal'

// 没有 /login、/register、/forgot-password、/reset-password 这四条了。
//
// 它们对应的后端接口随自建认证一起删掉了 —— 账号在 OneLink 上, 登录发生在门户
// (用户点应用卡片 → 带票据回到 /sso/landing → 后端换出应用会话)。前端再留一个登录页,
// 就等于给同一个身份摆出第二道门, 而那道门后面什么都没有。
//
// meta.perm 用**菜单码(M 型)**, 与 utils/menu.js 里的逐字一致。它是界面显隐的判据,
// 不是安全边界: 每一条接口在服务端都有自己的权限点(那一组是 B 型码), 这里只决定
// "能不能进这个页面", 免得用户点进一个只会报 403 的空白页。
const routes = [
  {
    path: '/',
    component: () => import('../layout/MainLayout.vue'),
    children: [
      {
        path: '',
        name: 'dashboard',
        component: () => import('../views/Dashboard.vue'),
        meta: { title: '仪表盘', perm: 'task-system:dashboard' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../views/Settings.vue'),
        // 个人设置**不挂菜单码**: 它不是一个业务入口, 而是"看自己的档案" ——
        // 任何登进来的人都该能看, 而他的档案里本来就只有自己的东西。
        meta: { title: '个人设置' },
      },
      {
        path: 'tasks',
        name: 'tasks',
        component: () => import('../views/TaskList.vue'),
        meta: { title: '任务管理', perm: 'task-system:task' },
      },
      {
        path: 'tasks/deleted',
        name: 'task-trash',
        component: () => import('../views/TaskTrash.vue'),
        meta: { title: '回收站', perm: 'task-system:task-trash' },
      },
      {
        path: 'tasks/:id',
        name: 'task-detail',
        component: () => import('../views/TaskDetail.vue'),
        // 详情页**不挂权限码**: 它的可见性由数据决定(创建者/负责人/被分享者),
        // 而那件事只有服务端知道。挂一个"查看任务"的码在这里会挡住一个合法的
        // 被分享者 —— 他没有这个码, 但他确实看得见这一条。
        meta: { title: '任务详情' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('../views/UserManage.vue'),
        meta: { title: '用户档案', perm: 'task-system:user' },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 启动时**先**问一次服务端再放行首次导航。
//
// 判据不能是"store 里有没有 user": 刷新页面后 store 是空的, 而会话其实还好好的 ——
// 那样每次 F5 都会把人赶回门户。所以这里等 auth.load() 落地(它自己会在 401 时清 store),
// 再决定去留。
let bootstrapped = null

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!bootstrapped) {
    bootstrapped = auth.load()
  }
  await bootstrapped

  if (!auth.isLoggedIn) {
    // 未登录一律回门户, 而不是回一个本地登录页(已经没有那个页面了)。
    // 用 location 而不是 router.push: 目标在另一个域上, 交给浏览器更省事,
    // 而且这一步之后本应用的前端路由状态已经没有意义了。
    goToPortal()
    return false
  }

  // 有权限点却不在快照里 -> 回仪表盘, 而不是给一个空白页。
  // 注意这里**不能**把"快照为空"当成"没权限": 超管的快照可能就是空的(平台对超管的
  // 判定是"该应用全部启用的权限点", 他本就不需要授权行), 所以 hasPerm 里对超管短路。
  if (to.meta.perm && !auth.hasPerm(to.meta.perm)) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
