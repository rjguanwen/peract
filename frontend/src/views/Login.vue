<template>
  <div class="login-page">
    <el-card class="login-card">
      <div class="login-title">
        <LogoMark :size="52" />
        <div class="brand-block">
          <h2>躬行</h2>
          <div class="slogan">纸上千言，不如躬行一件。</div>
          <div class="brand-en">PERACT</div>
        </div>
      </div>
      <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="onSubmit">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名" :prefix-icon="User" />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-button type="primary" class="login-btn" :loading="loading" @click="onSubmit">
          登 录
        </el-button>
        <div class="login-actions">
          <router-link to="/forgot-password" class="link">忘记密码？</router-link>
          <span class="divider">|</span>
          <router-link to="/register" class="link">注册账号</router-link>
        </div>
        <div class="login-tip">默认管理员：admin / admin123</div>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { useAuthStore } from '../stores/auth'
import LogoMark from '../components/LogoMark.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const formRef = ref()
const loading = ref(false)

const form = reactive({ username: '', password: '' })
const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function onSubmit() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    ElMessage.success('登录成功')
    router.push(route.query.redirect || '/')
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1d2939 0%, #344e41 100%);
}
.login-card {
  width: 380px;
  padding: 8px 16px 24px;
}
.login-title {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-bottom: 20px;
}
.brand-block {
  display: flex;
  flex-direction: column;
  align-items: center;
  margin-top: 10px;
}
.brand-block h2 {
  margin: 0;
  font-size: 22px;
  letter-spacing: 6px;
}
.slogan {
  margin-top: 6px;
  color: #6b7280;
  font-size: 12px;
  letter-spacing: 0.5px;
}
.brand-en {
  margin-top: 2px;
  color: #9ca3af;
  font-size: 10px;
  letter-spacing: 4px;
}
.login-btn {
  width: 100%;
}
.login-actions {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}
.link {
  color: #409eff;
  font-size: 13px;
  text-decoration: none;
}
.link:hover {
  color: #66b1ff;
}
.divider {
  color: #d1d5db;
  font-size: 12px;
}
.login-tip {
  margin-top: 16px;
  text-align: center;
  color: #9ca3af;
  font-size: 13px;
}
</style>
