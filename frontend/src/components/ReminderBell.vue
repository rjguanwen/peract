<template>
  <el-popover placement="bottom-end" :width="360" trigger="click" @show="load()">
    <template #reference>
      <el-badge :value="unread" :hidden="unread === 0" :max="99" class="bell">
        <el-icon :size="20" class="bell-icon"><Bell /></el-icon>
      </el-badge>
    </template>

    <div class="reminder-panel">
      <div class="reminder-header">
        <span>消息提醒</span>
        <el-button
          v-if="unread > 0"
          link
          type="primary"
          size="small"
          :loading="marking"
          @click="markAllRead"
        >
          全部已读
        </el-button>
      </div>
      <el-scrollbar max-height="360px">
        <div v-if="!items.length" class="empty">暂无提醒</div>
        <div
          v-for="item in items"
          :key="item.id"
          class="reminder-item"
          :class="{ unread: !item.read_at }"
          @click="openTask(item)"
        >
          <div class="reminder-message">{{ item.message || item.task_title }}</div>
          <div class="reminder-meta">
            <el-tag size="small" effect="plain">{{ typeLabel(item.remind_type) }}</el-tag>
            <span>{{ formatDateTime(item.remind_at) }}</span>
          </div>
        </div>
      </el-scrollbar>
    </div>
  </el-popover>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { reminderApi } from '../api'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/constants'

// 逾期提醒由后端调度器生成，不轮询的话用户永远得刷新页面才能看到
const POLL_INTERVAL_MS = 60000

const router = useRouter()
const auth = useAuthStore()
const unread = ref(0)
const items = ref([])
const marking = ref(false)
let timer = null

const TYPE_LABELS = {
  manual: '手动提醒',
  overdue: '逾期提醒',
  assign: '任务分配',
  status: '状态变更',
}

function typeLabel(type) {
  return TYPE_LABELS[type] || type
}

// silent 用于轮询：后端重启或网络抖动时不该每分钟弹一次错误提示
async function load({ silent = false } = {}) {
  if (!auth.isLoggedIn) return
  try {
    const config = silent ? { silent: true } : undefined
    const [count, data] = await Promise.all([
      reminderApi.unreadCount(config),
      reminderApi.list({ page_size: 50 }, config),
    ])
    // 后端返回的是 { count: n }，不能直接把响应体当数字用
    unread.value = Number(count?.count) || 0
    items.value = Array.isArray(data?.items) ? data.items : []
  } catch {
    /* 拦截器已提示；保留上一次的结果，避免面板闪成空列表 */
  }
}

function openTask(item) {
  if (!item.read_at) {
    // 乐观减一后再拉一次真实值：这条本来也可能已被「全部已读」清掉
    item.read_at = new Date().toISOString()
    unread.value = Math.max(0, unread.value - 1)
    reminderApi.markRead(item.id).then(() => load({ silent: true })).catch(() => {
      /* 拦截器已提示 */
    })
  }
  router.push(`/tasks/${item.task_id}`)
}

async function markAllRead() {
  marking.value = true
  try {
    // 一次请求完成后端批量更新，不再 Promise.all 逐条发（提醒多时会打出一排请求）
    await reminderApi.readAll()
  } catch {
    /* 拦截器已提示 */
  } finally {
    marking.value = false
  }
  await load({ silent: true })
}

function tick() {
  // 页面在后台时不轮询，免得标签页积一堆请求
  if (document.visibilityState === 'visible') load({ silent: true })
}

onMounted(() => {
  load({ silent: true })
  timer = setInterval(tick, POLL_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
  timer = null
})
</script>

<style scoped>
.bell {
  cursor: pointer;
  display: flex;
  align-items: center;
}
.bell-icon {
  font-size: 20px;
}
.reminder-panel {
  min-height: 120px;
}
.reminder-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
  font-weight: 600;
}
.empty {
  text-align: center;
  color: #9ca3af;
  padding: 24px 0;
}
.reminder-item {
  padding: 10px 8px;
  border-bottom: 1px solid #f3f4f6;
  cursor: pointer;
}
.reminder-item:hover {
  background: #f9fafb;
}
.reminder-item.unread {
  background: #eff6ff;
}
.reminder-message {
  font-size: 14px;
  margin-bottom: 6px;
}
.reminder-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: #9ca3af;
  font-size: 12px;
}
</style>
