<template>
  <div v-loading="loading">
    <el-page-header @back="$router.push('/tasks')">
      <template #content>
        <span class="page-title">{{ task?.title }}</span>
        <el-tag v-if="task?.is_overdue" type="danger" effect="dark" class="ml">已逾期</el-tag>
      </template>
      <template #extra>
        <el-button v-if="canShare" @click="showShareDialog = true">分享</el-button>
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
              <el-button @click="openMilestones">
                <el-icon><Flag /></el-icon>设定里程碑
              </el-button>
              <el-button @click="showReminderDialog = true">
                <el-icon><Bell /></el-icon>设置提醒
              </el-button>
            </div>
          </el-card>

          <!--
            计划与进展合并成**一条**时间轴, 而不是两张并排的表。
            用户要的是"方便比对", 而比对的动作是"这条计划对应的实际发生在什么时候" ——
            分成两列之后, 那个动作要人在两个时间刻度之间来回换算, 那正是这个功能要消掉的工作。
            蓝色是计划, 绿色是实际。
          -->
          <el-card shadow="never" class="row">
            <template #header>
              <div class="card-head">
                <span>计划与进展</span>
                <span v-if="milestoneSummary" class="card-head__note">{{ milestoneSummary }}</span>
              </div>
            </template>

            <el-timeline v-if="timeline.length">
              <el-timeline-item
                v-for="item in timeline"
                :key="item.key"
                :timestamp="formatDateTime(item.at)"
                placement="top"
                :type="item.kind === 'milestone' ? 'primary' : timelineType(item.raw.action)"
              >
                <template v-if="item.kind === 'milestone'">
                  <div class="progress-line">
                    <el-tag size="small" type="primary" effect="plain">计划</el-tag>
                    <span class="ms-title">{{ item.raw.title }}</span>
                    <el-tag size="small" :type="item.state.type" effect="plain">{{ item.state.label }}</el-tag>
                  </div>
                  <div v-if="item.raw.note" class="progress-comment">{{ item.raw.note }}</div>
                </template>

                <template v-else>
                  <div class="progress-line">
                    <el-tag size="small" type="success" effect="plain">实际</el-tag>
                    <el-tag size="small" effect="plain">{{ ACTION_LABELS[item.raw.action] || item.raw.action }}</el-tag>
                    <el-tag v-if="item.raw.progress != null" size="small" type="primary">
                      进度 {{ item.raw.progress }}%
                    </el-tag>
                    <span class="progress-user">{{ item.raw.user_name }}</span>
                  </div>
                  <div v-if="item.raw.comment" class="progress-comment">{{ item.raw.comment }}</div>
                  <div v-if="item.raw.old_status" class="progress-flow">
                    状态：{{ statusLabel(item.raw.old_status) }} → {{ statusLabel(item.raw.new_status) }}
                  </div>
                </template>
              </el-timeline-item>
            </el-timeline>
            <div v-else class="empty">暂无计划与进展记录</div>
          </el-card>
        </el-col>

        <el-col :span="8">
          <el-card shadow="never">
            <template #header>任务分享</template>
            <div v-if="!shares.length && !canShare" class="empty">暂无分享</div>
            <div v-if="shares.length" class="share-list">
              <div v-for="s in shares" :key="s.id" class="share-item">
                <UserAvatar :src="s.avatar_url" :name="s.full_name || s.username" :size="26" />
                <div class="share-info">
                  <div class="share-name">{{ s.full_name || s.username }}</div>
                  <div class="share-email">{{ s.email }}</div>
                </div>
                <el-button
                  v-if="canShare"
                  link
                  type="danger"
                  size="small"
                  :loading="revokingId === s.id"
                  @click="revokeShare(s)"
                >取消</el-button>
              </div>
            </div>
            <el-button v-if="canShare" size="small" class="share-add-btn" @click="showShareDialog = true">
              + 分享给其他人
            </el-button>
          </el-card>

          <el-card shadow="never" class="row">
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

    <!-- 设定里程碑 -->
    <el-dialog v-model="showMilestoneDialog" title="设定里程碑" width="620px">
      <p class="share-hint">
        里程碑是这个任务的<b>计划</b>节点。它们会与进展记录合并成详情页上那条时间轴，
        计划与实际差在哪里就在那里比 —— 所以这里只需要填"打算什么时候到哪一步"，
        实际时间由进展记录自己带。
      </p>

      <div v-if="task?.milestones?.length" class="ms-list">
        <div v-for="m in task.milestones" :key="m.id" class="ms-item">
          <div class="ms-item__main">
            <div class="ms-item__title">
              {{ m.title }}
              <el-tag size="small" :type="milestoneState(m).type" effect="plain">
                {{ milestoneState(m).label }}
              </el-tag>
            </div>
            <div class="ms-item__meta">
              {{ formatDateTime(m.planned_at) }}
              <span v-if="m.note"> · {{ m.note }}</span>
            </div>
          </div>
          <el-button link size="small" @click="editMilestone(m)">编辑</el-button>
          <el-popconfirm title="删除这个里程碑？" @confirm="removeMilestone(m)">
            <template #reference>
              <el-button link type="danger" size="small">删除</el-button>
            </template>
          </el-popconfirm>
        </div>
      </div>
      <div v-else class="empty">还没有计划节点</div>

      <el-divider content-position="left">
        {{ milestoneForm.id ? '编辑节点' : '新增节点' }}
      </el-divider>
      <el-form label-width="80px">
        <el-form-item label="节点名称">
          <el-input
            v-model="milestoneForm.title"
            maxlength="128"
            placeholder="如：需求评审、提测、上线"
          />
        </el-form-item>
        <el-form-item label="计划时间">
          <el-date-picker
            v-model="milestoneForm.planned_at"
            type="datetime"
            placeholder="选择计划达成时间"
            style="width: 100%"
            value-format="YYYY-MM-DDTHH:mm:ss"
          />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="milestoneForm.note" maxlength="500" placeholder="可选" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button v-if="milestoneForm.id" @click="resetMilestoneForm">取消编辑</el-button>
        <el-button @click="showMilestoneDialog = false">关闭</el-button>
        <el-button type="primary" :loading="savingMilestone" @click="submitMilestone">
          {{ milestoneForm.id ? '保存修改' : '添加' }}
        </el-button>
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

    <!-- 分享任务 -->
    <el-dialog v-model="showShareDialog" title="分享任务" width="420px">
      <p class="share-hint">被分享者将可以在其任务列表中查看此任务。</p>
      <el-form label-width="80px">
        <el-form-item label="被分享者邮箱">
          <el-input
            v-model="shareEmail"
            placeholder="输入对方的注册邮箱"
            @keyup.enter="submitShare"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showShareDialog = false">取消</el-button>
        <el-button type="primary" :loading="sharing" @click="submitShare">确认分享</el-button>
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
import UserAvatar from '../components/UserAvatar.vue'
import { useAuthStore } from '../stores/auth'
import { ACTION_LABELS, formatDateTime, statusMeta } from '../utils/constants'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const task = ref(null)
const reminders = ref([])
const shares = ref([])
const loading = ref(false)
const saving = ref(false)
const savingReminder = ref(false)
const editVisible = ref(false)
const showProgressDialog = ref(false)
const showReminderDialog = ref(false)
const showShareDialog = ref(false)
const shareEmail = ref('')
const sharing = ref(false)
const revokingId = ref(null)

const showMilestoneDialog = ref(false)
const savingMilestone = ref(false)
// id 为 null 表示"在新增"。用一个表单同时承担新增与编辑, 而不是两个弹窗:
// 两者的字段完全一样, 而分成两个地方之后,"为什么编辑时改不了计划时间"这类问题
// 会变成要去比对两个表单的差异才能回答。
const milestoneForm = reactive({ id: null, title: '', planned_at: '', note: '' })

const progressForm = reactive({ status: '', progress: 0, comment: '' })
const reminderForm = reactive({ remind_at: '', message: '' })

// 计划与进展合并成一条按时间排好的轴。见模板里那段注释: 比对的动作是"这条计划对应的
// 实际发生在什么时候", 而分两列摆着会把这个动作推给人。
//
// 并列时的次序由插入顺序决定(先计划后实际) —— Array.prototype.sort 是稳定排序,
// 而"先看打算怎样, 再看实际怎样"正是读一条时间轴的自然顺序。
const timeline = computed(() => {
  const items = []
  for (const m of task.value?.milestones || []) {
    items.push({ key: `m${m.id}`, kind: 'milestone', at: m.planned_at, raw: m, state: milestoneState(m) })
  }
  for (const p of task.value?.progresses || []) {
    items.push({ key: `p${p.id}`, kind: 'progress', at: p.created_at, raw: p })
  }
  return items.sort((a, b) => new Date(a.at) - new Date(b.at))
})

// 计划侧的一行小结语。只数"计划时间已经过去的节点", **不判断达成** ——
// 达成与否是计划与进展比出来的结论, 服务端不替它下(见后端 TaskMilestoneOut 的注释),
// 前端也不该替它下: 一个"已完成"的绿标会让人不再去看那条时间轴, 而时间轴才是这个功能。
const milestoneSummary = computed(() => {
  const list = task.value?.milestones || []
  if (!list.length) return ''
  const past = list.filter((m) => new Date(m.planned_at) < new Date()).length
  return past ? `计划 ${list.length} 个节点 · ${past} 个已过期` : `计划 ${list.length} 个节点`
})

// 单个计划节点的状态。它是与**当前时刻**比出来的, 所以任务已完成时不再显示"已过期" ——
// 那会是一句假警报: 计划已经收口了, 而"已过期"读起来像还有事没做。
function milestoneState(m) {
  if (task.value?.status === 'done') return { label: '任务已完成', type: 'info' }
  const days = Math.ceil((new Date(m.planned_at) - new Date()) / 86400000)
  if (days < 0) return { label: `已过期 ${-days} 天`, type: 'danger' }
  if (days === 0) return { label: '今天', type: 'warning' }
  if (days <= 3) return { label: `${days} 天后`, type: 'warning' }
  return { label: `${days} 天后`, type: 'info' }
}

function openMilestones() {
  resetMilestoneForm()
  showMilestoneDialog.value = true
}

function editMilestone(m) {
  Object.assign(milestoneForm, {
    id: m.id,
    title: m.title,
    planned_at: m.planned_at,
    note: m.note,
  })
}

function resetMilestoneForm() {
  Object.assign(milestoneForm, { id: null, title: '', planned_at: '', note: '' })
}

async function submitMilestone() {
  if (!milestoneForm.title.trim()) {
    ElMessage.warning('请填写节点名称')
    return
  }
  if (!milestoneForm.planned_at) {
    ElMessage.warning('请选择计划时间')
    return
  }
  savingMilestone.value = true
  const payload = {
    title: milestoneForm.title,
    planned_at: milestoneForm.planned_at,
    note: milestoneForm.note,
  }
  try {
    if (milestoneForm.id) {
      await taskApi.updateMilestone(task.value.id, milestoneForm.id, payload)
    } else {
      await taskApi.addMilestone(task.value.id, payload)
    }
    ElMessage.success('已保存')
    resetMilestoneForm()
    // 回读而不是把响应拼进本地数组: 合并时间轴要重排, 而"计划时间"是排序键 ——
    // 在本地插一条会让顺序在一个不显眼的地方出错。
    await load()
  } catch {
    /* 拦截器已提示，保持表单内容以便修改后重试 */
  } finally {
    savingMilestone.value = false
  }
}

async function removeMilestone(m) {
  try {
    await taskApi.removeMilestone(task.value.id, m.id)
    ElMessage.success('已删除')
    // 正在编辑的那一条被删了就把表单退回"新增", 否则接下来那一次保存会去 PATCH 一个
    // 已经不存在的 id, 而用户看到的是"保存失败"却不知道原因。
    if (milestoneForm.id === m.id) {
      resetMilestoneForm()
    }
    await load()
  } catch {
    /* 拦截器已提示 */
  }
}

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
    auth.canSeeAllTasks ||
    task.value.creator_id === auth.user?.id ||
    task.value.assignee_id === auth.user?.id
  )
})

// 删除权限：仅"能看全部任务"的人或任务创建者
//
// 判据从"角色是不是 admin"换成了权限点。名字也从"管理员"改掉: 它要回答的是
// "他能不能看全部任务", 而"躬行管理员"这个角色里可以只勾一半权限点。
const canDelete = computed(() => {
  if (!task.value) return false
  return auth.canSeeAllTasks || task.value.creator_id === auth.user?.id
})

// 编辑权限：仅"能看全部任务"的人或任务创建者
const canEdit = computed(() => {
  if (!task.value) return false
  return auth.canSeeAllTasks || task.value.creator_id === auth.user?.id
})

// 分享权限：仅任务创建者（能看全部任务的人也不能替别人分享）
const canShare = computed(() => {
  if (!task.value) return false
  return task.value.creator_id === auth.user?.id
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

async function loadShares() {
  if (!task.value) return
  try {
    shares.value = await taskApi.listShares(task.value.id)
  } catch {
    shares.value = []
  }
}

async function submitShare() {
  if (!shareEmail.value.trim()) {
    ElMessage.warning('请输入被分享者的邮箱')
    return
  }
  sharing.value = true
  try {
    await taskApi.addShare(task.value.id, shareEmail.value.trim())
    ElMessage.success('分享成功')
    shareEmail.value = ''
    showShareDialog.value = false
    await loadShares()
  } catch (e) {
    // 拦截器已提示
  } finally {
    sharing.value = false
  }
}

async function revokeShare(share) {
  revokingId.value = share.id
  try {
    await taskApi.revokeShare(task.value.id, share.id)
    ElMessage.success('已取消分享')
    await loadShares()
  } catch {
    /* 拦截器已提示 */
  } finally {
    revokingId.value = null
  }
}

onMounted(() => {
  load()
  loadShares()
})
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
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.card-head__note {
  font-size: 12px;
  font-weight: 400;
  color: #9ca3af;
}
.ms-title {
  font-weight: 600;
  color: #111827;
}
.ms-list {
  display: flex;
  flex-direction: column;
}
.ms-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  border-bottom: 1px solid #f3f4f6;
}
.ms-item__main {
  flex: 1;
  min-width: 0;
}
.ms-item__title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: #374151;
}
.ms-item__meta {
  margin-top: 2px;
  font-size: 12px;
  color: #9ca3af;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
.share-hint {
  margin: 0 0 16px;
  color: #6b7280;
  font-size: 13px;
}
.share-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 12px;
}
.share-item {
  display: flex;
  align-items: center;
  gap: 10px;
}
.share-info {
  flex: 1;
  min-width: 0;
}
.share-name {
  font-size: 13px;
  font-weight: 500;
  color: #374151;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.share-email {
  font-size: 11px;
  color: #9ca3af;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.share-add-btn {
  width: 100%;
  border-style: dashed;
}
</style>
