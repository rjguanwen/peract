<template>
  <div class="auth-page">
    <el-card class="auth-card">
      <div class="auth-title">
        <el-icon :size="28"><Lock /></el-icon>
        <h2>设置新密码</h2>
        <p class="sub">通过邮件链接重置密码</p>
      </div>

      <el-form label-position="top" size="large">
        <el-form-item label="新密码" required>
          <el-input v-model="form.newPassword" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input
            v-model="form.confirm"
            type="password"
            show-password
            placeholder="再次输入新密码"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" class="submit-btn" :loading="loading" @click="submit">
          重置密码
        </el-button>
        <div class="auth-actions">
          <router-link to="/login" class="link">返回登录</router-link>
        </div>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Lock } from '@element-plus/icons-vue'
import { authApi } from '../api'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const form = reactive({ newPassword: '', confirm: '' })

async function submit() {
  if (form.newPassword.length < 6) return ElMessage.warning('新密码至少需要 6 个字符')
  if (form.newPassword !== form.confirm) return ElMessage.warning('两次输入的新密码不一致')
  if (!route.query.token) return ElMessage.warning('缺少重置令牌')
  loading.value = true
  try {
    await authApi.resetByToken({ token: route.query.token, newPassword: form.newPassword })
    ElMessage.success('密码重置成功，请使用新密码登录')
    router.push('/login')
  } catch {
    /* 拦截器已提示 */
  } finally {
    loading.value = false
  }
}
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
.submit-btn {
  width: 100%;
}
.auth-actions {
  margin-top: 14px;
  text-align: center;
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
