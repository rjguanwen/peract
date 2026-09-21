<template>
  <div class="user-mgmt">
    <el-alert type="info" show-icon :closable="false">
      <template #title>这里是「用户档案」，不是账号管理</template>
      <template #default>
        账号、口令、以及"谁能做什么"的角色授权都在 OneLink 上。这一页只保留业务上要用的
        那部分：谁能被指派任务，以及他在这个应用里显示成什么名字。
        新增人员请到 OneLink 建账号并授权，他从门户进来时会自动出现在这里。
      </template>
    </el-alert>

    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span class="card-title">用户档案（{{ users.length }}）</span>
          <el-button size="small" text type="primary" @click="load">
            <el-icon class="mr-1"><Refresh /></el-icon>刷新
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
        <el-table-column label="用户名" width="140">
          <template #default="{ row }">{{ row.username }}</template>
        </el-table-column>
        <el-table-column label="可否被指派" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.is_active" size="small" type="success">正常</el-tag>
            <el-tag v-else size="small" type="danger">已停用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="加入时间" width="140">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column v-if="canManage" label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <template v-if="row.id === auth.user?.id">
              <span class="dim">（本人）</span>
            </template>
            <div v-else class="op-btns">
              <el-button
                size="small"
                :type="row.is_active ? 'danger' : 'success'"
                plain
                @click="toggleActive(row)"
              >
                {{ row.is_active ? '停用' : '启用' }}
              </el-button>
              <el-button size="small" text type="primary" @click="openEdit(row)">编辑</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 编辑档案 -->
    <el-dialog v-model="dialogVisible" title="编辑用户档案" width="460px">
      <el-form label-position="top">
        <el-form-item label="用户名">
          <el-input :model-value="editing?.username" disabled />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input :model-value="editing?.email" disabled />
        </el-form-item>
        <el-form-item label="姓名 / 昵称">
          <el-input v-model="form.full_name" placeholder="全站显示的名称" />
        </el-form-item>
        <el-form-item label="个性签名">
          <el-input v-model="form.signature" maxlength="80" show-word-limit />
        </el-form-item>
        <p class="dialog-hint">
          用户名、邮箱与角色由 OneLink 维护，改这里没有意义 —— 下一次这个人从门户进来时
          会被平台那份覆盖回去。
        </p>
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
import { userApi } from '../api'
import { useAuthStore } from '../stores/auth'
import { formatDateTime } from '../utils/constants'
import UserAvatar from '../components/UserAvatar.vue'

const auth = useAuthStore()
const users = ref([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editing = ref(null)

const form = reactive({ full_name: '', signature: '' })

// 没有 PermUserManage 的人进来只能看。判据是权限码而不是"是不是管理员" ——
// 服务端那一条接口挂的就是这个码, 而界面这一份只是提前把话说清楚(点下去会 403)。
const canManage = computed(() => auth.hasPerm('task-system:user:manage'))

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

function openEdit(row) {
  editing.value = row
  form.full_name = row.full_name || ''
  form.signature = row.signature || ''
  dialogVisible.value = true
}

async function submit() {
  if (!editing.value) return
  saving.value = true
  try {
    await userApi.update(editing.value.id, {
      full_name: form.full_name,
      signature: form.signature,
    })
    ElMessage.success('用户档案已更新')
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
      `${action}「${row.full_name || row.username}」？` +
        (row.is_active
          ? '停用后他仍然能登录，但不会再出现在任务负责人的候选里。要真正禁止他进入躬行，请到 OneLink 收回角色授权。'
          : ''),
      '提示',
      { type: row.is_active ? 'warning' : 'info' },
    )
  } catch {
    return
  }
  try {
    await userApi.update(row.id, { is_active: !row.is_active })
    ElMessage.success(`已${action}`)
    load()
  } catch {
    /* 拦截器已提示 */
  }
}

onMounted(load)
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
.dialog-hint {
  margin: 0;
  color: #9ca3af;
  font-size: 12px;
  line-height: 1.6;
}
</style>
