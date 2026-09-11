<template>
  <div v-loading="loading">
    <el-page-header @back="$router.push('/tasks')">
      <template #content>
        <span class="page-title">{{ task?.title }}</span>
        <el-tag v-if="task?.is_overdue" type="danger" effect="dark" class="ml">已逾期</el-tag>
      </template>
      <template #extra>
        <el-button v-if="canEdit" @click="openEdit">编辑</el-button>
        <el-popconfirm v-if="canDelete" title="确定删除该任务吗？删除后可在回收站中恢复" @confirm="remove">
          <template #reference>
            <el-button type="danger" plain>删除</el-button>
          </template>
        </el-popconfirm>
      </template>
    </el-page-header>

    <template v-if="task">
      <el-row :gutter="16" class="row">
        <el-col :span="16">
          <el-card shadow="never">
            <template #header>任务信息</template>
            <el-descriptions :column="2" border>
              <el-descriptions-item label="状态">
                <StatusTag :status="task.status" />
              </el-descriptions-item>
              <el-descriptions-item label="优先级">
                <PriorityTag :priority="task.priority" />
              </el-descriptions-item>
              <el-descriptions-item label="负责人">
                {{ task.assignee_name || '未分配' }}
              </el-descriptions-item>
              <el-descriptions-item label="创建人">
                {{ task.creator_name || '-' }}
              </el-descriptions-item>
              <el-descriptions-item label="创建时间">
                {{ formatDateTime(task.created_at) }}
              </el-descriptions-item>
              <el-descriptions-item label="更新时间">
                {{ formatDateTime(task.updated_at) }}
              </el-descriptions-item>
              <el-descriptions-item label="截止时间">
                <span :class="{ 'due-overdue': task.is_overdue }">
                  {{ formatDateTime(task.due_date) }}
                </span>
              </el-descriptions-item>
              <el-descriptions-item label="完成进度">
                <el-progress :percentage="task.progress" :stroke-width="12" style="width: 180px" />
              </el-descriptions-item>
              <el-descriptions-item label="描述" :span="2">
                <pre class="desc">{{ task.description || '暂无描述' }}</pre>
              </el-descriptions-item>
            </el-descriptions>

            <el-divider content-position="left">操作</el-divider>
            <div v-if="canModify" class="actions">
              <el-button
                v-if="task.status === 'todo'"
                type="warning"
                @click="changeStatus('in_progress')"
              >开始处理</el-button>
              <el-button
                v-if="task.status === 'in_progress'"
                type="success"
                @click="changeStatus('done')"
              >标记完成</el-button>
              <el-button
                v-if="task.status === 'done'"
                type="info"
                @click="changeStatus('in_progress')"
              >重新打开</el-button>
              <el-button @click="showProgressDialog = true">
                <el-icon><EditPen /></el-icon>添加进展
              </el-button>
              <el-button @click="showReminderDialog = true">
                <el-icon><Bell /></el-icon>设置提醒
              </el-button>
            </div>
          </el-card>

          <el-card shadow="never" class="row">
            <template #header>进展记录</template>
            <el-timeline v-if="task.progresses?.length">
              <el-timeline-item
                v-for="p in task.progresses"
                :key="p.id"
                :timestamp="formatDateTime(p.created_at)"
                placement="top"
                :type="timelineType(p.action)"
              >
                <div class="progress-line">
                  <el-tag size="small" effect="plain">{{ ACTION_LABELS[p.action] || p.action }}</el-tag>
                  <el-tag v-if="p.progress != null" size="small" type="primary">进度 {{ p.progress }}%</el-tag>
                  <span class="progress-user">{{ p.user_name }}</span>
                </div>
                <div v-if="p.comment" class="progress-comment">{{ p.comment }}</div>
                <div v-if="p.old_status" class="progress-flow">
                  状态：{{ statusLabel(p.old_status) }} → {{ statusLabel(p.new_status) }}
                </div>
              </el-timeline-item>
            </el-timeline>
            <div v-else class="empty">暂无进展记录</div>
          </el-card>
        </el-col>

        <el-col :span="8">
          <el-card shadow="never">
            <template #header>提醒</template>
            <div v-if="!reminders.length" class="empty">暂无提醒</div>
            <div v-for="r in reminders" :key="r.id" class="reminder-item">
              <div class="reminder-msg">{{ r.message }}</div>
              <div class="reminder-meta">
                <el-tag size="small" effect="plain">{{ typeLabel(r.remind_type) }}</el-tag>
                <span>{{ formatDateTime(r.remind_at) }}</span>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </template>

    <TaskFormDialog v-model="editVisible" :task="task" @saved="load" />

    <!-- 添加进展 -->
    <el-dialog v-model="showProgressDialog" title="添加进展" width="480px">
      <el-form label-width="70px">
        <el-form-item label="新状态">
          <el-radio-group v-model="progressForm.status">
            <el-radio-button value="">保持不变</el-radio-button>
            <el-radio-button value="todo">待处理</el-radio-button>
            <el-radio-button value="in_progress">进行中</el-radio-button>
            <el-radio-button value="done">已完成</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="进度">
          <el-slider v-model="progressForm.progress" :min="0" :max="100" :step="5" show-input />
        </el-form-item>
        <el-form-item label="备注">
          <el-input
            v-model="progressForm.comment"
            type="textarea"
            :rows="3"
            maxlength="2000"
            placeholder="本次进展说明"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showProgressDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitProgress">提交</el-button>
      </template>
    </el-dialog>

    <!-- 设置提醒 -->
    <el-dialog v-model="showReminderDialog" title="设置任务提醒" width="480px">
      <el-form label-width="70px">
        <el-form-item label="提醒时间">
          <el-date-picker
            v-model="reminderForm.remind_at"
            type="datetime"
            placeholder="选择提醒时间"
            style="width: 100%"
            value-format="YYYY-MM-DDTHH:mm:ss"
          />
        </el-form-item>
        <el-form-item label="提醒内容">
          <el-input v-model="reminderForm.message" maxlength="2000" placeholder="可选，默认任务提醒" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showReminderDialog = false">取消</el-button>
        <el-button type="primary" :loading="savingReminder" @click="submitReminder">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { reminderApi, taskApi } from '../api'
import StatusTag from '../components/StatusTag.vue'
import PriorityTag from '../components/PriorityTag.vue'
import TaskFormDialog from '../components/TaskFormDialog.vue'
import { useAuthStore } from '../stores/auth'
import { ACTION_LABELS, formatDateTime, statusMeta } from '../utils/constants'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const task = ref(null)
const reminders = ref([])
const loading = ref(false)
const saving = ref(false)
const savingReminder = ref(false)
const editVisible = ref(false)
const showProgressDialog = ref(false)
const showReminderDialog = ref(false)

const progressForm = reactive({ status: '', progress: 0, comment: '' })
const reminderForm = reactive({ remind_at: '', message: '' })

watch(showProgressDialog, (visible) => {
  if (visible && task.value) {
    progressForm.progress = task.value.progress
    progressForm.status = ''
    progressForm.comment = ''
  }
})

const canModify = computed(() => {
  if (!task.value) return false
  return (
    auth.isAdmin ||
    task.value.creator_id === auth.user?.id ||
    task.value.assignee_id === auth.user?.id
  )
})

// 删除权限：仅管理员或任务创建者
const canDelete = computed(() => {
  if (!task.value) return false
  return auth.isAdmin || task.value.creator_id === auth.user?.id
})

// 编辑权限：仅管理员或任务创建者
const canEdit = computed(() => {
  if (!task.value) return false
  return auth.isAdmin || task.value.creator_id === auth.user?.id
})

function statusLabel(value) {
  return statusMeta(value).label
}

function typeLabel(type) {
  const map = { manual: '手动', overdue: '逾期', assign: '分配', status: '状态' }
  return map[type] || type
}

function timelineType(action) {
  return { created: 'primary', assigned: 'warning', status_changed: 'success', progress_updated: 'success', comment: 'info' }[action] || 'info'
}

function openEdit() {
  editVisible.value = true
}

async function load() {
  loading.value = true
  try {
    const data = await taskApi.get(route.params.id)
    task.value = data
    const reminderData = await reminderApi.list({ page_size: 50 })
    reminders.value = (Array.isArray(reminderData?.items) ? reminderData.items : []).filter(
      (r) => r.task_id === data.id,
    )
  } catch (e) {
    /* 404 已由拦截器提示 */
  } finally {
    loading.value = false
  }
}

async function changeStatus(status) {
  try {
    await taskApi.update(task.value.id, { status })
  } catch {
    /* 拦截器已提示；失败时不能报「已更新」并留下假状态 */
    return
  }
  ElMessage.success('状态已更新')
  load()
}

async function remove() {
  try {
    await taskApi.remove(task.value.id)
  } catch {
    /* 拦截器已提示 */
    return
  }
  ElMessage.success('任务已删除')
  router.push('/tasks')
}

async function submitProgress() {
  saving.value = true
  try {
    await taskApi.addProgress(task.value.id, {
      comment: progressForm.comment,
      progress: progressForm.status ? undefined : progressForm.progress,
      status: progressForm.status || undefined,
    })
    ElMessage.success('进展已记录')
    showProgressDialog.value = false
    Object.assign(progressForm, { status: '', progress: 0, comment: '' })
    load()
  } catch {
    /* 拦截器已提示，保持弹窗打开 */
  } finally {
    saving.value = false
  }
}

async function submitReminder() {
  if (!reminderForm.remind_at) {
    ElMessage.warning('请选择提醒时间')
    return
  }
  savingReminder.value = true
  try {
    await reminderApi.create({
      task_id: task.value.id,
      remind_at: reminderForm.remind_at,
      message: reminderForm.message,
    })
    ElMessage.success('提醒已设置')
    showReminderDialog.value = false
    Object.assign(reminderForm, { remind_at: '', message: '' })
    load()
  } catch {
    /* 拦截器已提示，保持弹窗打开 */
  } finally {
    savingReminder.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.page-title {
  font-size: 18px;
  font-weight: 600;
}
.ml {
  margin-left: 10px;
}
.row {
  margin-top: 16px;
}
.desc {
  margin: 0;
  white-space: pre-wrap;
  font-family: inherit;
  color: #374151;
}
.due-overdue {
  color: #f56c6c;
  font-weight: 600;
}
.actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.progress-line {
  display: flex;
  align-items: center;
  gap: 8px;
}
.progress-user {
  color: #6b7280;
  font-size: 12px;
}
.progress-comment {
  margin-top: 4px;
  color: #374151;
}
.progress-flow {
  margin-top: 2px;
  color: #6b7280;
  font-size: 12px;
}
.empty {
  color: #9ca3af;
  text-align: center;
  padding: 24px 0;
}
.reminder-item {
  padding: 8px 0;
  border-bottom: 1px solid #f3f4f6;
}
.reminder-msg {
  font-size: 13px;
  margin-bottom: 4px;
}
.reminder-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: #9ca3af;
}
</style>
