<template>
  <div>
    <el-card shadow="never">
      <div class="toolbar">
        <el-form inline>
          <el-form-item label="优先级">
            <el-select v-model="filters.priority" placeholder="全部" clearable style="width: 120px" @change="reload">
              <el-option v-for="p in TASK_PRIORITIES" :key="p.value" :label="p.label" :value="p.value" />
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
            <el-button type="primary" @click="reload">
              <el-icon><Search /></el-icon>查询
            </el-button>
          </el-form-item>
        </el-form>
      </div>

      <el-table :data="items" v-loading="loading">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="标题" min-width="240" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="deleted-title">{{ row.title }}</span>
          </template>
        </el-table-column>
        <el-table-column label="优先级" width="90">
          <template #default="{ row }"><PriorityTag :priority="row.priority" /></template>
        </el-table-column>
        <el-table-column label="负责人" width="120">
          <template #default="{ row }">{{ row.assignee_name || '-' }}</template>
        </el-table-column>
        <el-table-column label="创建人" width="120">
          <template #default="{ row }">{{ row.creator_name || '-' }}</template>
        </el-table-column>
        <el-table-column label="删除时间" width="170">
          <template #default="{ row }">{{ formatDateTime(row.deleted_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-popconfirm
              :title="`确定恢复任务「${row.title}」吗？`"
              @confirm="restore(row)"
            >
              <template #reference>
                <el-button type="primary" link>
                  <el-icon><RefreshLeft /></el-icon>恢复
                </el-button>
              </template>
            </el-popconfirm>
          </template>
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
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi } from '../api'
import PriorityTag from '../components/PriorityTag.vue'
import { formatDateTime, TASK_PRIORITIES } from '../utils/constants'

const items = ref([])
const total = ref(0)
const loading = ref(false)

const filters = reactive({
  priority: '',
  keyword: '',
  page: 1,
  page_size: 20,
})

async function load() {
  loading.value = true
  try {
    const data = await taskApi.deleted(filters)
    items.value = data.items
    total.value = data.total
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

async function restore(row) {
  await taskApi.restore(row.id)
  ElMessage.success(`任务「${row.title}」已恢复`)
  reload()
}

onMounted(load)
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
.deleted-title {
  color: #9ca3af;
  text-decoration: line-through;
}
.pager {
  margin-top: 16px;
  justify-content: flex-end;
}
</style>
