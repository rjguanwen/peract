import axios from 'axios'
import { ElMessage } from 'element-plus'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
  // 会话是一张 HttpOnly cookie(OneLink 的应用会话)。跨域部署时, 少了它浏览器不会带上
  // 凭据, 而现象是"每个请求都 401", 看起来像会话过期 —— 于是人会去反复重新登录,
  // 而重登一万次也不会好。
  //
  // 这里**没有**请求拦截器去拼 Authorization 头: 那段代码随自签 JWT 一起删掉了。
  // 留着它(哪怕只是读一个空值)会让下一个人以为"这个应用还持有令牌"。
  withCredentials: true,
})

// 会话失效时的回调由 main.js 注册。这里不直接 import store，
// 是为了断开 api → router → store → api 的循环依赖（此前已埋着 TDZ 风险）。
let unauthorizedHandler = null
export function onUnauthorized(handler) {
  unauthorizedHandler = handler
}

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
//
// 只剩三条, 而且都不是"登录": 登录发生在门户(用户从 OneLink 点卡片进来, 票据由后端在
// /sso/landing 上消费), 应用侧没有登录页, 也没有注册与找回密码。
//
// me 是唯一一条"问我是谁"的接口: 200 说明会话有效, 401 说明该回门户了。
//
// logout 打的是 /logout 而不是 /api/v1/... —— 它由 OneLink 的守卫处理(要同时删掉本地
// 会话行并清 cookie), 不在业务路由前缀下。响应是 302, axios 会跟着跳, 因此这里用
// validateStatus 把 3xx 也当成成功, 否则每次登出都会多出一条"请求失败"的提示。
export const authApi = {
  me: (config) => api.get('/auth/me', config),
  logout: () =>
    api.post('/logout', null, {
      silent: true,
      baseURL: '',
      validateStatus: (s) => s >= 200 && s < 400,
    }),
}

// ===== 用户 =====
//
// 没有 create, 也没有"改角色/改口令"。成员归属现在由平台的角色授权表达(在 OneLink 里
// 给角色勾权限点、再把角色授给人), 而应用侧自动建账号或改口令都会造出一份与平台不一致
// 的档案 —— 下一次这个人从门户进来, 平台那份会把它覆盖回去。
export const userApi = {
  list: () => api.get('/users'),
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
  // 任务分享
  listShares: (taskId) => api.get(`/tasks/${taskId}/shares`),
  addShare: (taskId, inviteeEmail) => api.post(`/tasks/${taskId}/shares`, { invitee_email: inviteeEmail }),
  revokeShare: (taskId, shareId) => api.delete(`/tasks/${taskId}/shares/${shareId}`),
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
