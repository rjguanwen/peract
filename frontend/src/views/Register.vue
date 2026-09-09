<template>
  <div class="auth-page">
    <el-card class="auth-card">
      <div class="auth-title">
        <el-icon :size="28"><List /></el-icon>
        <h2>注册账号</h2>
        <p class="sub">加入任务管理系统</p>
      </div>

      <el-alert
        v-if="inviteInfo"
        type="success"
        :closable="false"
        class="invite-alert"
        show-icon
      >
        <template #title>
          {{ inviteInfo.invitedByDisplayName ? inviteInfo.invitedByDisplayName + ' 邀请你加入' : '你收到了注册邀请' }}
          （邀请邮箱：{{ inviteInfo.email }}，有效期至 {{ inviteInfo.expiresAt }}）
        </template>
      </el-alert>

      <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="onSubmit">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名（登录名）" :prefix-icon="User" />
        </el-form-item>
        <el-form-item prop="full_name">
          <el-input v-model="form.full_name" placeholder="姓名 / 昵称（可选）" :prefix-icon="Avatar" />
        </el-form-item>
        <el-form-item prop="email">
          <el-input
            v-model="form.email"
            placeholder="邮箱"
            :prefix-icon="Message"
            :disabled="!!inviteInfo"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码（至少 6 位）"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-form-item prop="confirm">
          <el-input
            v-model="form.confirm"
            type="password"
            placeholder="确认密码"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-button type="primary" class="submit-btn" :loading="loading" @click="onSubmit">
          注 册
        </el-button>
        <div class="auth-actions">
          已有账号？
          <router-link to="/login" class="link">去登录</router-link>
        </div>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock, Message, Avatar } from '@element-plus/icons-vue'
import { useAuthStore } from '../stores/auth'
import { authApi } from '../api'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const formRef = ref()
const loading = ref(false)
const inviteInfo = ref(null)

const form = reactive({
  username: '',
  full_name: '',
  email: '',
  password: '',
  confirm: '',
})

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9_.-]+$/, message: '用户名只能包含字母、数字、下划线、点、横线', trigger: 'blur' },
  ],
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, message: '密码至少 6 位', trigger: 'blur' },
  ],
  confirm: [
    {
      validator: (_, value, cb) => {
        if (!value) return cb(new Error('请再次输入密码'))
        if (value !== form.password) return cb(new Error('两次输入的密码不一致'))
        cb()
      },
      trigger: 'blur',
    },
  ],
}

async function onSubmit() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  loading.value = true
  try {
    await auth.register({
      username: form.username,
      email: form.email,
      full_name: form.full_name,
      password: form.password,
      inviteToken: inviteInfo.value ? route.query.invite : '',
    })
    ElMessage.success('注册成功')
    router.push('/')
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  const token = route.query.invite
  if (!token) return
  try {
    const info = await authApi.inviteInfo(token)
    inviteInfo.value = info
    form.email = info.email
  } catch {
    /* 拦截器已提示 */
  }
})
</script>

<style scoped>
.auth-page {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1d2939 0%, #344e41 100%);
}
.auth-card {
  width: 400px;
  padding: 8px 16px 24px;
}
.auth-title {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  margin-bottom: 20px;
}
.auth-title h2 {
  margin: 0;
  font-size: 20px;
}
.sub {
  margin: -4px 0 0;
  color: #9ca3af;
  font-size: 13px;
}
.invite-alert {
  margin-bottom: 14px;
}
.submit-btn {
  width: 100%;
}
.auth-actions {
  margin-top: 14px;
  text-align: center;
  color: #6b7280;
  font-size: 13px;
}
.link {
  color: #409eff;
  font-size: 13px;
  text-decoration: none;
}
.link:hover {
  color: #66b1ff;
}
</style>
