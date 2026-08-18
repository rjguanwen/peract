<template>
  <el-popover placement="bottom-end" :width="360" trigger="click" @show="load">
    <template #reference>
      <el-badge :value="unread" :hidden="unread === 0" :max="99" class="bell">
        <el-icon :size="20" class="bell-icon"><Bell /></el-icon>
      </el-badge>
    </template>

    <div class="reminder-panel">
      <div class="reminder-header">
        <span>消息提醒</span>
        <el-button v-if="unread > 0" link type="primary" size="small" @click="markAllRead">
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
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { reminderApi } from '../api'
import { formatDateTime } from '../utils/constants'

const router = useRouter()
const unread = ref(0)
const items = ref([])

const TYPE_LABELS = {
  manual: '手动提醒',
  overdue: '逾期提醒',
  assign: '任务分配',
  status: '状态变更',
}

function typeLabel(type) {
  return TYPE_LABELS[type] || type
}

async function load() {
  const [count, data] = await Promise.all([
    reminderApi.unreadCount(),
    reminderApi.list({ page_size: 50 }),
  ])
  unread.value = count
  items.value = data.items
}

function openTask(item) {
  if (!item.read_at) {
    reminderApi.markRead(item.id).then(() => {
      unread.value = Math.max(0, unread.value - 1)
    })
  }
  router.push(`/tasks/${item.task_id}`)
}

async function markAllRead() {
  await Promise.all(items.value.filter((i) => !i.read_at).map((i) => reminderApi.markRead(i.id)))
  await load()
}

onMounted(load)
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
