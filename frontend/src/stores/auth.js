import { defineStore } from 'pinia'
import { authApi } from '../api'

// 会话不再由前端持有。
//
// 接入前这里存着一段自签 JWT(localStorage), 每次请求手动拼 Authorization 头。现在凭据
// 是一张 HttpOnly 的会话 cookie: JS 读不到它, 也就偷不走它 —— 而"读不到"这件事必须由
// **不存它**来保证, 所以这个 store 里连一个 token 字段都不该有。谁把它加回来, 谁就把
// localStorage 那条路重新打开了一次。
//
// 于是 state 里的 user 只是**缓存**(用于首屏直接渲染与读权限快照), 权威值每次启动都由
// /auth/me 拉一遍。
export const useAuthStore = defineStore('auth', {
  state: () => ({
    user: null,
    // ready 表示"已经问过一次服务端"。
    //
    // 路由守卫必须靠它区分"还没问"与"问了但没登录": 少了它, 每次刷新页面守卫都会在
    // /auth/me 回来之前把用户当成未登录, 表现是"刷新一下就跳门户"。
    ready: false,
  }),

  getters: {
    isLoggedIn: (state) => !!state.user,

    /** 本次会话的权限码快照。空数组的含义是"什么都点不动", 不是"权限未知"。 */
    permissions: (state) => state.user?.permissions || [],

    isSuper: (state) => !!state.user?.super_admin,

    /**
     * 看得到全部任务。
     *
     * 这个名字取代了原来的 isAdmin。判据从"角色字符串是不是 admin"换成了权限点 ——
     * 而它要回答的从来不是"这个人是不是管理员", 而是"他能不能看见全部任务":
     * 前者在角色只有两个值的年代恰好等价, 现在"躬行管理员"这个角色里可以只勾一半权限点。
     */
    canSeeAllTasks: (state) =>
      !!state.user?.super_admin ||
      (state.user?.permissions || []).includes('task-system:task:list-all'),
  },

  actions: {
    /**
     * 有没有某个权限码。
     *
     * 它读的是**登录那一刻的快照**: 权限被改后, 这里最迟要等下一次登录才变。所以它只
     * 用来决定摆不摆入口, 不能当安全边界 —— 每一条接口在服务端都有自己的权限点, 那一道
     * 才是权威的(而且它失败时是显式的 403, 不是"按钮没出现")。
     */
    hasPerm(code) {
      if (this.isSuper) return true
      return this.permissions.includes(code)
    },

    applyUser(user) {
      this.user = user || null
    },

    /**
     * 问一次服务端"我是谁"。启动与路由守卫都用它。
     *
     * 401 由拦截器统一收敛(清 store + 跳门户), 这里只兜住"网络抖动": 那种情况下不能把
     * user 清掉, 否则一次后端重启会让所有人白屏 —— 而他们手里的会话其实还好好的。
     */
    async load() {
      try {
        this.applyUser(await authApi.me({ silent: true }))
      } catch (e) {
        const status = e.response?.status
        if (status === 401 || status === 403) this.applyUser(null)
      } finally {
        this.ready = true
      }
    },

    async logout() {
      // 服务端会把这条会话从库里删掉。只清本地 state 等于留下一个仍然可用的凭证 ——
      // 而那张 cookie 是 HttpOnly 的, 前端连"自己清掉它"都做不到。
      try {
        await authApi.logout()
      } catch {
        /* 网络失败也要退出, 本地清理照做 */
      }
      this.applyUser(null)
    },

    clear() {
      this.applyUser(null)
    },
  },
})
