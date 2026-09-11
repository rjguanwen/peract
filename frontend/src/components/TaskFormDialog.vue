<template>
  <el-dialog
    :model-value="modelValue"
    :title="task ? '编辑任务' : '新建任务'"
    width="560px"
    @update:model-value="$emit('update:modelValue', $event)"
    @close="reset"
  >
    <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
      <el-form-item label="标题" prop="title">
        <el-input v-model="form.title" maxlength="255" placeholder="请输入任务标题" />
      </el-form-item>
      <el-form-item label="描述" prop="description">
        <el-input
          v-model="form.description"
          type="textarea"
          :rows="4"
          maxlength="5000"
          placeholder="任务描述、验收标准等"
        />
      </el-form-item>
      <el-form-item label="负责人" prop="assignee_id">
        <el-select v-model="form.assignee_id" clearable filterable placeholder="选择负责人" style="width: 100%">
          <el-option v-for="u in users" :key="u.id" :label="u.full_name || u.username" :value="u.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="优先级" prop="priority">
        <el-radio-group v-model="form.priority">
          <el-radio-button v-for="p in TASK_PRIORITIES" :key="p.value" :value="p.value">
            {{ p.label }}
          </el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="截止时间">
        <el-date-picker
          v-model="form.due_date"
          type="datetime"
          placeholder="选择截止时间"
          style="width: 100%"
          value-format="YYYY-MM-DDTHH:mm:ss"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { taskApi, userApi } from '../api'
import { TASK_PRIORITIES } from '../utils/constants'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  task: { type: Object, default: null },
})
const emit = defineEmits(['update:modelValue', 'saved'])

const formRef = ref()
const saving = ref(false)
const users = ref([])

const form = reactive({
  title: '',
  description: '',
  assignee_id: null,
  priority: 'medium',
  due_date: null,
})

const rules = {
  title: [{ required: true, message: '请输入任务标题', trigger: 'blur' }],
}

// el-select / el-date-picker 清空后可能是 null、undefined 或 ''，一视同仁
function isEmpty(value) {
  return value === null || value === undefined || value === ''
}

watch(
  () => props.modelValue,
  async (visible) => {
    if (visible) {
      if (!users.value.length) {
        try {
          users.value = await userApi.list()
        } catch (e) {
          users.value = []
        }
      }
      if (props.task) {
        Object.assign(form, {
          title: props.task.title,
          description: props.task.description,
          assignee_id: props.task.assignee_id,
          priority: props.task.priority,
          due_date: props.task.due_date ? props.task.due_date.slice(0, 19) : null,
        })
      } else {
        // 新建时必须先复位，否则紧接在编辑之后打开会留下上一条任务的字段
        reset()
      }
    }
  },
)

function reset() {
  Object.assign(form, {
    title: '',
    description: '',
    assignee_id: null,
    priority: 'medium',
    due_date: null,
  })
}

async function submit() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  saving.value = true
  try {
    const payload = {
      title: form.title.trim(),
      description: form.description,
      priority: form.priority,
    }
    if (props.task) {
      // PATCH 语义下 assignee_id / due_date 传 null 意为「本次不改」，
      // 清空必须用显式的 clear_* 标志，否则删掉负责人再保存又会变回原样
      if (isEmpty(form.assignee_id)) {
        if (!isEmpty(props.task.assignee_id)) payload.clear_assignee = true
      } else {
        payload.assignee_id = form.assignee_id
      }
      if (isEmpty(form.due_date)) {
        if (!isEmpty(props.task.due_date)) payload.clear_due_date = true
      } else {
        payload.due_date = form.due_date
      }
      await taskApi.update(props.task.id, payload)
      ElMessage.success('任务已更新')
    } else {
      payload.assignee_id = isEmpty(form.assignee_id) ? null : form.assignee_id
      payload.due_date = isEmpty(form.due_date) ? null : form.due_date
      await taskApi.create(payload)
      ElMessage.success('任务已创建')
    }
    emit('update:modelValue', false)
    emit('saved')
  } catch {
    /* 拦截器已提示，保持弹窗打开以便用户修正 */
  } finally {
    saving.value = false
  }
}
</script>
