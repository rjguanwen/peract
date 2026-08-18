import axios from 'axios'
import { ElMessage } from 'element-plus'
import router from '../router'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
})

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

api.interceptors.response.use(
  (response) => response.data,
  (error) => {
    const status = error.response?.status
    const detail = error.response?.data?.detail
    if (status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('user')
      if (router.currentRoute.value.path !== '/login') {
        router.push('/login')
      }
    }
    const msg = typeof detail === 'string' ? detail : error.message || '请求失败'
    ElMessage.error(msg)
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
  me: () => api.get('/auth/me'),
  register: (data) => api.post('/auth/register', data),
}

// ===== 用户 =====
export const userApi = {
  list: () => api.get('/users'),
  create: (data) => api.post('/users', data),
  update: (id, data) => api.patch(`/users/${id}`, data),
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
  list: (params) => api.get('/reminders', { params }),
  unreadCount: () => api.get('/reminders/unread-count'),
  create: (data) => api.post('/reminders', data),
  markRead: (id) => api.post(`/reminders/${id}/read`),
}

// ===== 统计 =====
export const statsApi = {
  overview: () => api.get('/stats/overview'),
}
