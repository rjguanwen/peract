import { defineStore } from 'pinia'
import { authApi } from '../api'

const TOKEN_KEY = 'token'
const USER_KEY = 'user'

// localStorage 里的内容可能是旧版本残留、被手工改过，或被同域下的其他页面污染，
// 解析失败必须降级为「未登录」而不是抛异常——那会让整个 store 初始化失败、页面白屏。
function readStoredUser() {
  let raw = null
  try {
    raw = localStorage.getItem(USER_KEY)
  } catch {
    return null
  }
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : null
  } catch {
    removeStoredSession()
    return null
  }
}

function writeStoredSession(token, user) {
  try {
    localStorage.setItem(TOKEN_KEY, token)
    localStorage.setItem(USER_KEY, JSON.stringify(user ?? null))
  } catch {
    // 隐私模式或配额写满：会话退化为内存态，刷新后需重新登录，不影响当前这次使用
  }
}

function writeStoredUser(user) {
  try {
    localStorage.setItem(USER_KEY, JSON.stringify(user ?? null))
  } catch {
    /* 同上 */
  }
}

function removeStoredSession() {
  try {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(USER_KEY)
  } catch {
    /* ignore */
  }
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: (() => {
      try {
        return localStorage.getItem(TOKEN_KEY) || ''
      } catch {
        return ''
      }
    })(),
    user: readStoredUser(),
  }),
  getters: {
    isLoggedIn: (state) => !!state.token,
    isAdmin: (state) => state.user?.role === 'admin',
  },
  actions: {
    setSession(token, user) {
      this.token = token
      this.user = user
      writeStoredSession(token, user)
    },
    async login(username, password) {
      const data = await authApi.login(username, password)
      this.setSession(data.access_token, data.user)
      return data
    },
    async register(data) {
      const res = await authApi.register(data)
      this.setSession(res.access_token, res.user)
      return res
    },
    // 将后端返回的用户信息同步到本地
    applyUser(user) {
      this.user = user
      if (this.token) writeStoredUser(user)
    },
    // 启动时用令牌换回最新的用户信息：后端在改密或管理员重置后会作废旧令牌，
    // 本地缓存的角色、停用状态都可能已经过时。
    async restore() {
      if (!this.token) return false
      try {
        this.applyUser(await authApi.me({ silent: true }))
        return true
      } catch (e) {
        const status = e.response?.status
        // 只有服务端明确否认这个令牌时才清会话；网络抖动、后端重启
        // 都不该把用户莫名踢下线，那样连令牌都丢了
        if (status === 401 || status === 403) this.clear()
        return false
      }
    },
    async logout() {
      // 后端的 logout 会把当前令牌放进吊销名单，只清本地存储等于留下一个仍然可用的凭证
      try {
        await authApi.logout()
      } catch {
        /* 网络失败也要退出，本地清理照做 */
      }
      this.clear()
    },
    clear() {
      this.token = ''
      this.user = null
      removeStoredSession()
    },
  },
})
