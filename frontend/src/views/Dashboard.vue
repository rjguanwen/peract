<template>
  <div>
    <div class="cards">
      <el-card shadow="hover" class="stat-card">
        <div class="stat-label">任务总数</div>
        <div class="stat-value">{{ stats.total || 0 }}</div>
      </el-card>
      <el-card shadow="hover" class="stat-card">
        <div class="stat-label">待处理</div>
        <div class="stat-value" style="color: #909399">{{ stats.todo || 0 }}</div>
      </el-card>
      <el-card shadow="hover" class="stat-card">
        <div class="stat-label">进行中</div>
        <div class="stat-value" style="color: #e6a23c">{{ stats.in_progress || 0 }}</div>
      </el-card>
      <el-card shadow="hover" class="stat-card">
        <div class="stat-label">已完成</div>
        <div class="stat-value" style="color: #67c23a">{{ stats.done || 0 }}</div>
      </el-card>
      <el-card shadow="hover" class="stat-card danger">
        <div class="stat-label">已逾期</div>
        <div class="stat-value">{{ stats.overdue || 0 }}</div>
      </el-card>
    </div>

    <el-row :gutter="16" class="row">
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>
            <div class="card-header">
              <span>我的待办</span>
              <el-button link type="primary" @click="$router.push('/tasks?mine=true')">查看全部</el-button>
            </div>
          </template>
          <div v-if="!myTasks.length" class="empty">暂无待办任务，享受轻松时光</div>
          <div v-for="t in myTasks" :key="t.id" class="task-item" @click="$router.push(`/tasks/${t.id}`)">
            <div class="task-title">
              <span :class="['dot', { overdue: t.is_overdue }]"></span>
              {{ t.title }}
            </div>
            <div class="task-meta">
              <PriorityTag :priority="t.priority" />
              <StatusTag :status="t.status" />
              <span v-if="t.due_date" class="due" :class="{ overdue: t.is_overdue }">
                {{ formatDateTime(t.due_date) }}
              </span>
            </div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card shadow="never">
          <template #header>
            <div class="card-header"><span>优先级分布</span></div>
          </template>
          <div v-for="p in priorityList" :key="p.value" class="priority-row">
            <span class="priority-label">{{ p.label }}</span>
            <el-progress
              :percentage="priorityPercent(p.value)"
              :color="p.color"
              :stroke-width="14"
            />
            <span class="priority-count">{{ priorityDist[p.value] || 0 }}</span>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { statsApi, taskApi } from '../api'
import StatusTag from '../components/StatusTag.vue'
import PriorityTag from '../components/PriorityTag.vue'
import { formatDateTime, TASK_PRIORITIES } from '../utils/constants'

const stats = ref({})
const myTasks = ref([])

const priorityList = [
  { value: 'urgent', label: '紧急', color: '#f56c6c' },
  { value: 'high', label: '高', color: '#e6a23c' },
  { value: 'medium', label: '中', color: '#409eff' },
  { value: 'low', label: '低', color: '#909399' },
]
const priorityDist = computed(() => stats.value.priority_dist || {})

function priorityPercent(value) {
  const total = Math.max(1, stats.value.total || 1)
  return Math.round(((priorityDist.value[value] || 0) / total) * 100)
}

async function load() {
  const [s, t] = await Promise.all([
    statsApi.overview(),
    taskApi.list({ mine: true, page_size: 10 }),
  ])
  stats.value = s
  myTasks.value = t.items
}

onMounted(load)
</script>

<style scoped>
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}
.stat-card {
  text-align: center;
}
.stat-card.danger {
  border-top: 3px solid #f56c6c;
}
.stat-label {
  color: #6b7280;
  font-size: 14px;
}
.stat-value {
  font-size: 30px;
  font-weight: 700;
  margin-top: 6px;
  color: #1d2939;
}
.row {
  margin-top: 16px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.empty {
  color: #9ca3af;
  text-align: center;
  padding: 32px 0;
}
.task-item {
  padding: 10px 4px;
  border-bottom: 1px solid #f3f4f6;
  cursor: pointer;
}
.task-item:hover {
  background: #f9fafb;
}
.task-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
  margin-bottom: 6px;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #409eff;
  flex-shrink: 0;
}
.dot.overdue {
  background: #f56c6c;
}
.task-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-left: 16px;
}
.due {
  color: #6b7280;
  font-size: 13px;
}
.due.overdue {
  color: #f56c6c;
  font-weight: 600;
}
.priority-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.priority-label {
  width: 40px;
  color: #4b5563;
}
.priority-count {
  width: 32px;
  text-align: right;
  color: #6b7280;
}
</style>
