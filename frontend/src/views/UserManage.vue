<template>
  <div class="user-mgmt">
    <!-- 注册开关 -->
    <el-card shadow="never">
      <div class="switch-row">
        <div>
          <div class="card-title">允许新用户注册</div>
          <p class="card-desc">关闭后注册入口将提示「系统已暂停注册」，现有用户不受影响</p>
        </div>
        <el-switch v-model="registrationEnabled" :loading="savingSetting" @change="toggleRegistration" />
      </div>
    </el-card>

    <!-- 邀请注册 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <div>
            <span class="card-title">邀请注册</span>
            <span class="card-desc-inline">向指定邮箱发送邀请链接，关闭公开注册后受邀邮箱仍可注册</span>
          </div>
          <el-button size="small" text type="primary" @click="loadInvites">
            <el-icon class="mr-1"><Refresh /></el-icon>刷新
          </el-button>
        </div>
      </template>

      <div class="invite-input-row">
        <el-input
          v-model="inviteEmails"
          type="textarea"
          :rows="3"
          placeholder="输入受邀邮箱，每行一个或以逗号分隔（单次最多 20 个）"
        />
        <el-button type="primary" class="invite-send" :loading="sendingInvite" :disabled="!inviteEmails.trim()" @click="sendInvites">
          发送邀请
        </el-button>
      </div>

      <div v-if="inviteResults.length" class="invite-results">
        <div v-for="(r, i) in inviteResults" :key="i" class="invite-result-item">
          <span :class="r.ok ? 'ok' : 'err'">{{ r.ok ? '✓' : '✗' }}</span>
          <div class="min-w-0">
            <div class="result-email">{{ r.email }}</div>
            <div class="result-reason">
              {{ r.reason || (r.dev ? '开发模式：SMTP 未配置，以下为邀请链接（点击可打开注册页）' : '邀请邮件已发送') }}
            </div>
            <a
              v-if="r.ok && r.invite_url"
              :href="r.invite_url"
              target="_blank"
              rel="noopener"
              class="invite-url"
            >{{ r.invite_url }}</a>
          </div>
        </div>
      </div>

      <div class="invite-list" v-if="invites.length || loadingInvites" v-loading="loadingInvites">
        <el-table :data="invites" stripe size="small">
          <el-table-column label="受邀邮箱" min-width="190">
            <template #default="{ row }">{{ row.email }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag v-if="row.status === 'pending' && !row.expired" size="small" type="warning" effect="plain">待接受</el-tag>
              <el-tag v-else-if="row.status === 'pending' && row.expired" size="small" type="info" effect="plain">已过期</el-tag>
              <el-tag v-else-if="row.status === 'registered'" size="small" type="success" effect="plain">已注册</el-tag>
              <el-tag v-else size="small" type="danger" effect="plain">已撤销</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="邀请人" width="120">
            <template #default="{ row }">{{ row.invitedBy?.display_name || '—' }}</template>
          </el-table-column>
          <el-table-column label="邀请时间" width="120">
            <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
          </el-table-column>
          <el-table-column label="有效至" width="120">
            <template #default="{ row }">{{ formatDate(row.expiresAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="90" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.status === 'pending' && !row.expired" size="small" type="danger" plain @click="revokeInvite(row)">撤销</el-button>
              <el-button v-else-if="row.status === 'pending' && row.expired" size="small" type="primary" text @click="resendInvite(row)">重发</el-button>
              <span v-else class="dim">—</span>
            </template>
          </el-table-column>
        </el-table>
      </div>
      <el-empty v-else-if="!loadingInvites" description="暂无邀请记录" :image-size="70" />
    </el-card>

    <!-- 用户列表 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span class="card-title">用户列表（{{ users.length }}）</span>
          <el-button type="primary" size="small" @click="openCreate">
            <el-icon class="mr-1"><Plus /></el-icon>新增用户
          </el-button>
        </div>
      </template>

      <el-table :data="users" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column label="用户" min-width="200">
          <template #default="{ row }">
            <div class="user-cell">
              <UserAvatar :src="row.avatar_url" :name="row.full_name || row.username" :size="30" />
              <div class="user-meta">
                <div class="user-name">{{ row.full_name || row.username }}</div>
                <div class="user-email">{{ row.email }}</div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="用户名" width="120">
          <template #default="{ row }">{{ row.username }}</template>
        </el-table-column>
        <el-table-column label="角色" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.role === 'admin'" size="small" type="danger" effect="dark">管理员</el-tag>
            <el-tag v-else size="small" type="info" effect="plain">普通用户</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.is_active" size="small" type="success">正常</el-tag>
            <el-tag v-else size="small" type="danger">已停用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="注册时间" width="140">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <template v-if="row.id === auth.user?.id">
              <span class="dim">（本人）</span>
            </template>
            <div v-else class="op-btns">
              <el-button v-if="row.role !== 'admin'" size="small" :type="row.is_active ? 'danger' : 'success'" plain @click="toggleActive(row)">
                {{ row.is_active ? '停用' : '启用' }}
              </el-button>
              <el-button size="small" text type="primary" @click="openEdit(row)">编辑</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新增/编辑用户 -->
    <el-dialog v-model="dialogVisible" :title="editing ? '编辑用户' : '新增用户'" width="480px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" :disabled="!!editing" placeholder="登录名，2-64字符" />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="form.full_name" placeholder="真实姓名" />
        </el-form-item>
        <el-form-item label="邮箱" prop="email">
          <el-input v-model="form.email" placeholder="user@example.com" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            :placeholder="editing ? '留空则不修改' : '至少6位'"
          />
        </el-form-item>
        <el-form-item v-if="canChangePrivilege" label="角色">
          <el-radio-group v-model="form.role">
            <el-radio value="user">普通用户</el-radio>
            <el-radio value="admin">管理员</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="editing && canChangePrivilege" label="状态">
          <el-switch v-model="form.is_active" active-text="启用" inactive-text="停用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { userApi, adminApi } from '../api'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/constants'
import UserAvatar from '../components/UserAvatar.vue'

const auth = useAuthStore()
const users = ref([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editing = ref(null)
const formRef = ref()

// 注册开关
const registrationEnabled = ref(true)
const savingSetting = ref(false)
// 邀请
const inviteEmails = ref('')
const sendingInvite = ref(false)
const inviteResults = ref([])
const invites = ref([])
const loadingInvites = ref(false)

const form = reactive({
  username: '',
  full_name: '',
  email: '',
  password: '',
  role: 'user',
  is_active: true,
})

// 当前编辑行是否允许改角色/启用状态（管理员或自己不可改）
const canChangePrivilege = computed(() => {
  if (!editing.value) return true
  return editing.value.role !== 'admin' && editing.value.id !== auth.user?.id
})

const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  email: [{ required: true, message: '请输入邮箱', trigger: 'blur' }],
  password: [
    {
      validator: (_, value, cb) => {
        if (!editing.value && !value) return cb(new Error('请输入密码'))
        if (value && value.length < 6) return cb(new Error('密码至少6位'))
        cb()
      },
      trigger: 'blur',
    },
  ],
}

function formatDate(value) {
  return value ? String(value).slice(0, 10) : '-'
}

// ===== 用户列表 =====
async function load() {
  loading.value = true
  try {
    users.value = await userApi.list()
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, { username: '', full_name: '', email: '', password: '', role: 'user', is_active: true })
  dialogVisible.value = true
}

function openEdit(row) {
  editing.value = row
  Object.assign(form, {
    username: row.username,
    full_name: row.full_name,
    email: row.email,
    password: '',
    role: row.role,
    is_active: row.is_active,
  })
  dialogVisible.value = true
}

async function submit() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  saving.value = true
  try {
    const payload = { ...form }
    if (!payload.password) delete payload.password
    if (editing.value) {
      // 管理员/本人行的角色与状态后端禁止修改，前端一并剔除
      if (editing.value.role === 'admin' || editing.value.id === auth.user?.id) {
        delete payload.role
        delete payload.is_active
      }
      await userApi.update(editing.value.id, payload)
      ElMessage.success('用户已更新')
    } else {
      await userApi.create(payload)
      ElMessage.success('用户已创建')
    }
    dialogVisible.value = false
    load()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function toggleActive(row) {
  const action = row.is_active ? '停用' : '启用'
  try {
    await ElMessageBox.confirm(
      `${action}用户「${row.full_name || row.username}」？${action === '停用' ? '该用户将立即无法登录和使用。' : ''}`,
      '提示',
      { type: action === '停用' ? 'warning' : 'info' },
    )
  } catch {
    return
  }
  try {
    await userApi.update(row.id, { is_active: !row.is_active })
    ElMessage.success(`已${action}用户`)
    load()
  } catch {
    /* 拦截器已提示 */
  }
}

// ===== 注册开关 =====
async function loadSettings() {
  try {
    const data = await adminApi.settings()
    registrationEnabled.value = !!data.registration_enabled
  } catch {
    /* 忽略 */
  }
}

async function toggleRegistration(value) {
  savingSetting.value = true
  try {
    await adminApi.setRegistration(value)
    ElMessage.success(value ? '已开放注册' : '已暂停注册')
  } catch {
    registrationEnabled.value = !value
  } finally {
    savingSetting.value = false
  }
}

// ===== 邀请注册 =====
async function loadInvites() {
  loadingInvites.value = true
  try {
    const data = await adminApi.invites()
    invites.value = data.items || []
  } catch {
    /* 拦截器已提示 */
  } finally {
    loadingInvites.value = false
  }
}

function parseEmails(text) {
  return [...new Set(text.split(/[\n,;，；、\s]+/).map((s) => s.trim()).filter(Boolean))]
}

async function sendInvitesFor(emailList) {
  sendingInvite.value = true
  try {
    const data = await adminApi.createInvites(emailList)
    const results = data.results || []
    inviteResults.value = results
    const okCount = results.filter((r) => r.ok).length
    // 取第一条成功结果判断发信模式：results[0] 可能是一条失败项，
    // 用它会让开发模式下的文案错报成「已发送邮件」
    const firstOk = results.find((r) => r.ok)
    if (okCount > 0) {
      ElMessage.success(`已为 ${okCount} 个邮箱${firstOk?.dev ? '生成邀请链接（未配置 SMTP）' : '发送邀请邮件'}`)
    }
    if (okCount === emailList.length) inviteEmails.value = ''
    loadInvites()
  } catch {
    /* 拦截器已提示 */
  } finally {
    sendingInvite.value = false
  }
}

async function sendInvites() {
  const emailList = parseEmails(inviteEmails.value)
  if (!emailList.length) return ElMessage.warning('请先输入受邀邮箱')
  if (emailList.length > 20) return ElMessage.warning('单次最多邀请 20 个邮箱')
  await sendInvitesFor(emailList)
}

async function resendInvite(row) {
  inviteResults.value = []
  await sendInvitesFor([row.email])
}

async function revokeInvite(row) {
  try {
    await ElMessageBox.confirm(`撤销对「${row.email}」的邀请？撤销后该邀请链接将立即失效。`, '提示', { type: 'warning' })
  } catch {
    return
  }
  try {
    await adminApi.revokeInvite(row.id)
    ElMessage.success('邀请已撤销')
    loadInvites()
  } catch {
    /* 拦截器已提示 */
  }
}

onMounted(() => {
  load()
  loadSettings()
  loadInvites()
})
</script>

<style scoped>
.user-mgmt {
  max-width: 1180px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.card-title {
  font-weight: 600;
}
.card-desc-inline {
  color: #9ca3af;
  font-size: 12px;
  margin-left: 8px;
}
.switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.card-desc {
  margin: 4px 0 0;
  color: #9ca3af;
  font-size: 12px;
}
.invite-input-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.invite-send {
  flex-shrink: 0;
  margin-top: 2px;
}
.invite-results {
  margin-top: 12px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  overflow: hidden;
}
.invite-result-item {
  display: flex;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid #f3f4f6;
  font-size: 13px;
}
.invite-result-item:last-child {
  border-bottom: none;
}
.invite-result-item .ok {
  color: #059669;
}
.invite-result-item .err {
  color: #dc2626;
}
.result-email {
  font-weight: 500;
}
.result-reason {
  color: #9ca3af;
  font-size: 12px;
  margin-top: 2px;
}
.invite-url {
  color: #2563eb;
  font-size: 12px;
  word-break: break-all;
  text-decoration: none;
  display: inline-block;
  margin-top: 2px;
}
.invite-url:hover {
  text-decoration: underline;
}
.invite-list {
  margin-top: 12px;
}
.user-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}
.user-meta {
  min-width: 0;
}
.user-name {
  font-weight: 500;
  font-size: 14px;
}
.user-email {
  color: #9ca3af;
  font-size: 12px;
}
.dim {
  color: #c3c8d0;
  font-size: 12px;
}
.op-btns {
  display: flex;
  align-items: center;
  gap: 4px;
}
</style>
