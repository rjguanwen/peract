import axios from 'axios'
import { ElMessage } from 'element-plus'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
})

// 会话失效时的回调由 main.js 注册。这里不直接 import store，
// 是为了断开 api → router → store → api 的循环依赖（此前已埋着 TDZ 风险）。
let unauthorizedHandler = null
export function onUnauthorized(handler) {
  unauthorizedHandler = handler
}

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 同一段错误在两秒内只提示一次：一次页面加载往往并发好几个请求，
// 令牌失效时会同时撞上 401，不合并就会叠出一屏重复弹窗。
let lastText = ''
let lastAt = 0
function notifyError(text) {
  const now = Date.now()
  if (text === lastText && now - lastAt < 2000) return
  lastText = text
  lastAt = now
  ElMessage.error(text)
}

const STATUS_TEXT = {
  400: '请求参数有误',
  401: '登录已过期，请重新登录',
  403: '没有操作权限',
  404: '资源不存在',
  409: '数据冲突，请刷新后重试',
  423: '该操作暂不可用，请按提示处理',
  500: '服务器繁忙，请稍后重试',
}

function describe(error) {
  const resp = error.response
  if (!resp) {
    if (error.code === 'ECONNABORTED' || /timeout/i.test(error.message || '')) {
      return '请求超时，请稍后重试'
    }
    return '无法连接服务器，请检查后端服务是否已启动'
  }
  const detail = resp.data?.detail
  const text = typeof detail === 'string' && detail ? detail : ''
  if (resp.status === 429) {
    if (text) return text
    const retryAfter = parseInt(resp.headers?.['retry-after'], 10)
    return retryAfter > 0 ? `操作过于频繁，请 ${retryAfter} 秒后再试` : '操作过于频繁，请稍后再试'
  }
  return text || STATUS_TEXT[resp.status] || `请求失败（${resp.status}）`
}

api.interceptors.response.use(
  (response) => response.data,
  (error) => {
    if (error.response?.status === 401) {
      if (unauthorizedHandler) unauthorizedHandler()
    } else if (!error.config?.silent && !axios.isCancel(error)) {
      // config.silent 为 true 时把错误原样抛给调用方，交给组件自己决定怎么提示，
      // 避免「拦截器弹一次、组件 catch 再弹一次」
      notifyError(describe(error))
    }
    return Promise.reject(error)
  },
)

export default api

// ===== 认证 =====
export const authApi = {
  login: (username, password) => {
    const form = new URLSearchParams()
    form.append('username', username)
    form.append('password', password)
    return api.post('/auth/login', form)
  },
  me: (config) => api.get('/auth/me', config),
  register: (data) => api.post('/auth/register', data),
  // 退出时的 401 与报错都不该再打扰用户，本地清理照做即可
  logout: () => api.post('/auth/logout', null, { silent: true }),
  changePassword: (data) => api.put('/auth/password', data),
  getSecurity: () => api.get('/auth/security'),
  setSecurity: (data) => api.put('/auth/security', data),
  getRecovery: (email) => api.get('/auth/forgot', { params: { email } }),
  resetPassword: (data) => api.post('/auth/forgot/reset', data),
  sendForgotEmail: (email) => api.post('/auth/forgot/send', { email }),
  resetByToken: (data) => api.post('/auth/reset', data),
  inviteInfo: (token) => api.get('/auth/invite/info', { params: { token } }),
}

// ===== 用户 =====
export const userApi = {
  list: () => api.get('/users'),
  create: (data) => api.post('/users', data),
  update: (id, data) => api.patch(`/users/${id}`, data),
}

// ===== 个人资料 =====
export const profileApi = {
  update: (data) => api.put('/profile', data),
  uploadAvatar: (file) => {
    const form = new FormData()
    form.append('file', file)
    return api.put('/profile/avatar', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 30000,
    })
  },
}

// ===== 管理员（设置与邀请注册） =====
export const adminApi = {
  settings: () => api.get('/admin/settings'),
  setRegistration: (enabled) => api.put('/admin/settings/registration', { enabled }),
  invites: () => api.get('/admin/invites'),
  createInvites: (emails) => api.post('/admin/invites', { emails }),
  revokeInvite: (id) => api.post(`/admin/invites/${id}/revoke`),
}

// ===== 任务 =====
export const taskApi = {
  list: (params) => api.get('/tasks', { params }),
  get: (id) => api.get(`/tasks/${id}`),
  create: (data) => api.post('/tasks', data),
  update: (id, data) => api.patch(`/tasks/${id}`, data),
  remove: (id) => api.delete(`/tasks/${id}`),
  addProgress: (id, data) => api.post(`/tasks/${id}/progress`, data),
  deleted: (params) => api.get('/tasks/deleted', { params }),
  restore: (id) => api.post(`/tasks/${id}/restore`),
}

// ===== 提醒 =====
export const reminderApi = {
  list: (params, config) => api.get('/reminders', { params, ...config }),
  unreadCount: (config) => api.get('/reminders/unread-count', config),
  create: (data) => api.post('/reminders', data),
  markRead: (id) => api.post(`/reminders/${id}/read`),
  // 后端的一次性接口，替代前端 Promise.all 逐条 markRead
  readAll: () => api.post('/reminders/read-all', null, { silent: true }),
}

// ===== 统计 =====
export const statsApi = {
  overview: () => api.get('/stats/overview'),
}
