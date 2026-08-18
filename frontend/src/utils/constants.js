export const TASK_STATUSES = [
  { value: 'todo', label: '待处理', type: 'info' },
  { value: 'in_progress', label: '进行中', type: 'warning' },
  { value: 'done', label: '已完成', type: 'success' },
]

export const TASK_PRIORITIES = [
  { value: 'low', label: '低', type: 'info' },
  { value: 'medium', label: '中', type: 'primary' },
  { value: 'high', label: '高', type: 'warning' },
  { value: 'urgent', label: '紧急', type: 'danger' },
]

export const ACTION_LABELS = {
  created: '创建任务',
  assigned: '任务分配',
  status_changed: '状态变更',
  progress_updated: '进度更新',
  comment: '进展备注',
}

export function statusMeta(value) {
  return TASK_STATUSES.find((s) => s.value === value) || { label: value, type: 'info' }
}

export function priorityMeta(value) {
  return TASK_PRIORITIES.find((s) => s.value === value) || { label: value, type: 'info' }
}

export function formatDateTime(value) {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '-'
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
