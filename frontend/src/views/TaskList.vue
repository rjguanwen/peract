<template>
  <div>
    <el-card shadow="never">
      <div class="toolbar">
        <el-form inline>
          <el-form-item label="状态">
            <el-select v-model="filters.status" placeholder="全部" clearable style="width: 120px" @change="reload">
              <el-option v-for="s in TASK_STATUSES" :key="s.value" :label="s.label" :value="s.value" />
            </el-select>
          </el-form-item>
          <el-form-item label="优先级">
            <el-select v-model="filters.priority" placeholder="全部" clearable style="width: 120px" @change="reload">
              <el-option v-for="p in TASK_PRIORITIES" :key="p.value" :label="p.label" :value="p.value" />
            </el-select>
          </el-form-item>
          <el-form-item label="负责人">
            <el-select v-model="filters.assignee_id" placeholder="全部" clearable filterable style="width: 140px" @change="reload">
              <el-option v-for="u in users" :key="u.id" :label="u.full_name || u.username" :value="u.id" />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="filters.keyword"
              placeholder="搜索标题/描述"
              clearable
              style="width: 180px"
              @keyup.enter="reload"
              @clear="reload"
            />
          </el-form-item>
          <el-form-item>
            <el-checkbox v-model="filters.overdue_only" @change="reload">只看逾期</el-checkbox>
          </el-form-item>
          <el-form-item>
            <el-button type="primary" @click="reload">
              <el-icon><Search /></el-icon>查询
            </el-button>
          </el-form-item>
        </el-form>
        <el-button type="primary" @click="openCreate">
          <el-icon><Plus /></el-icon>新建任务
        </el-button>
      </div>

      <el-table :data="items" v-loading="loading" @row-click="(row) => $router.push(`/tasks/${row.id}`)" style="cursor: pointer">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="标题" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span :class="{ 'overdue-title': row.is_overdue }">{{ row.title }}</span>
            <el-tag v-if="row.is_overdue" type="danger" size="small" effect="dark" class="overdue-tag">逾期</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }"><StatusTag :status="row.status" /></template>
        </el-table-column>
        <el-table-column label="优先级" width="90">
          <template #default="{ row }"><PriorityTag :priority="row.priority" /></template>
        </el-table-column>
        <el-table-column label="负责人" width="120">
          <template #default="{ row }">{{ row.assignee_name || '-' }}</template>
        </el-table-column>
        <el-table-column label="进度" width="150">
          <template #default="{ row }">
            <el-progress :percentage="row.progress" :stroke-width="10" />
          </template>
        </el-table-column>
        <el-table-column label="截止时间" width="160">
          <template #default="{ row }">
            <span :class="{ 'due-overdue': row.is_overdue }">{{ formatDateTime(row.due_date) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="160">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="pager"
        layout="total, prev, pager, next"
        :total="total"
        :page-size="filters.page_size"
        :current-page="filters.page"
        @current-change="onPageChange"
      />
    </el-card>

    <TaskFormDialog v-model="dialogVisible" :task="editingTask" @saved="reload" />
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { taskApi, userApi } from '../api'
import StatusTag from '../components/StatusTag.vue'
import PriorityTag from '../components/PriorityTag.vue'
import TaskFormDialog from '../components/TaskFormDialog.vue'
import { formatDateTime, TASK_PRIORITIES, TASK_STATUSES } from '../utils/constants'

const route = useRoute()
const items = ref([])
const users = ref([])
const total = ref(0)
const loading = ref(false)
const dialogVisible = ref(false)
const editingTask = ref(null)

const filters = reactive({
  status: '',
  priority: '',
  assignee_id: null,
  keyword: '',
  overdue_only: false,
  mine: route.query.mine === 'true',
  page: 1,
  page_size: 20,
})

async function load() {
  loading.value = true
  try {
    const data = await taskApi.list({ ...filters, mine: filters.mine || undefined })
    items.value = Array.isArray(data?.items) ? data.items : []
    total.value = Number(data?.total) || 0
  } catch {
    /* 拦截器已提示；不接住就是一条未处理的 Promise 异常 */
  } finally {
    loading.value = false
  }
}

function reload() {
  filters.page = 1
  load()
}

function onPageChange(page) {
  filters.page = page
  load()
}

function openCreate() {
  editingTask.value = null
  dialogVisible.value = true
}

onMounted(async () => {
  load()
  try {
    users.value = await userApi.list()
  } catch (e) {
    users.value = []
  }
})
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}
.overdue-title {
  color: #f56c6c;
  font-weight: 600;
}
.overdue-tag {
  margin-left: 6px;
}
.due-overdue {
  color: #f56c6c;
  font-weight: 600;
}
.pager {
  margin-top: 16px;
  justify-content: flex-end;
}
</style>
