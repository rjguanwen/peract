<template>
  <div class="auth-page">
    <el-card class="auth-card">
      <div class="auth-title">
        <el-icon :size="28"><Key /></el-icon>
        <h2>找回密码</h2>
        <p class="sub">通过安全问答或邮箱重置密码</p>
      </div>

      <!-- 第一步：输入邮箱 -->
      <template v-if="!account">
        <el-form ref="formRef" :model="form" :rules="rules" size="large" @keyup.enter="lookup">
          <el-form-item prop="email">
            <el-input v-model="form.email" placeholder="请输入注册邮箱" :prefix-icon="Message" />
          </el-form-item>
          <el-button type="primary" class="submit-btn" :loading="loadingLookup" @click="lookup">
            下一步
          </el-button>
        </el-form>
      </template>

      <!-- 第二步：重置方式 -->
      <template v-else>
        <!-- 无论邮箱是否注册后端都回同一形结构，这里不能直接断言「该邮箱已注册」，
             否则这个页面又变回一个账号枚举器 -->
        <el-alert type="info" :closable="false" class="mb-12">
          <template #title>
            <template v-if="account.exists">
              该邮箱已注册{{ account.password_hint ? '，密码提示词：' + account.password_hint : '' }}
            </template>
            <template v-else>若该邮箱已注册，可通过下面的方式重置密码；未注册邮箱不会收到任何邮件。</template>
          </template>
        </el-alert>

        <el-alert
          v-if="account.answer_legacy"
          type="warning"
          :closable="false"
          show-icon
          class="mb-12"
          title="该账号的安全答案是旧版明文存储，已停止使用。请登录在「个人设置」重新设置安全问答，或改用邮箱链接重置。"
        />

        <!-- 问答重置 -->
        <div v-if="account.has_security" class="panel">
          <div class="panel-title">方式一：安全问答重置</div>
          <el-form label-position="top">
            <el-form-item label="安全问题">
              <el-input :model-value="account.security_question" disabled />
            </el-form-item>
            <el-form-item label="答案" required>
              <el-input v-model="qaForm.answer" placeholder="回答安全问题" />
            </el-form-item>
            <el-form-item label="新密码" required>
              <el-input v-model="qaForm.newPassword" type="password" show-password placeholder="至少 6 位" />
            </el-form-item>
            <el-form-item label="确认新密码" required>
              <el-input v-model="qaForm.confirm" type="password" show-password placeholder="再次输入新密码" @keyup.enter="resetByQA" />
            </el-form-item>
            <el-button type="primary" plain :loading="qaLoading" @click="resetByQA">重置密码</el-button>
          </el-form>
        </div>

        <!-- 邮箱链接重置 -->
        <div class="panel">
          <div class="panel-title">方式二：通过邮箱链接重置</div>
          <p class="panel-desc">我们将向注册邮箱发送一封包含重置链接的邮件（{{ RESET_LINK_TTL }}内有效）。</p>
          <el-button type="primary" plain :loading="mailLoading" @click="sendMail">发送重置邮件</el-button>
          <el-alert v-if="devUrl" type="warning" :closable="false" class="dev-box" show-icon>
            <template #title>开发模式：SMTP 未配置，请使用以下重置链接</template>
            <div class="dev-link">{{ devUrl }}</div>
          </el-alert>
          <div v-if="mailSent" class="ok-text">若该邮箱已注册，重置邮件已发出，请在 {{ RESET_LINK_TTL }}内打开链接。</div>
        </div>
      </template>

      <div class="auth-actions">
        <router-link to="/login" class="link">返回登录</router-link>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Key, Message } from '@element-plus/icons-vue'
import { authApi } from '../api'

const router = useRouter()
// 与后端 resetLinkTTLHint 保持一致，改一边要同步改另一边
const RESET_LINK_TTL = '30 分钟'
const formRef = ref()
const loadingLookup = ref(false)
const qaLoading = ref(false)
const mailLoading = ref(false)
const account = ref(null)
const devUrl = ref('')
const mailSent = ref(false)

const form = reactive({ email: '' })
const qaForm = reactive({ answer: '', newPassword: '', confirm: '' })

const rules = {
  email: [
    { required: true, message: '请输入注册邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
}

async function lookup() {
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  loadingLookup.value = true
  try {
    account.value = await authApi.getRecovery(form.email)
    devUrl.value = ''
    mailSent.value = false
  } catch {
    /* 拦截器已提示 */
  } finally {
    loadingLookup.value = false
  }
}

async function resetByQA() {
  if (!qaForm.answer.trim()) return ElMessage.warning('请回答安全问题')
  if (qaForm.newPassword.length < 6) return ElMessage.warning('新密码至少需要 6 个字符')
  if (qaForm.newPassword !== qaForm.confirm) return ElMessage.warning('两次输入的新密码不一致')
  qaLoading.value = true
  try {
    await authApi.resetPassword({
      email: form.email,
      answer: qaForm.answer,
      newPassword: qaForm.newPassword,
    })
    ElMessage.success('密码重置成功，请使用新密码登录')
    router.push('/login')
  } catch {
    /* 拦截器已提示 */
  } finally {
    qaLoading.value = false
  }
}

async function sendMail() {
  mailLoading.value = true
  try {
    const data = await authApi.sendForgotEmail(form.email)
    if (data.dev) {
      devUrl.value = data.reset_url
    } else {
      // 邮箱未注册时后端也回 sent:true，提示文案不能变成「已发送」以外的断言
      mailSent.value = true
      ElMessage.success('若该邮箱已注册，重置邮件已发送，请查收')
    }
  } catch {
    /* 拦截器已提示 */
  } finally {
    mailLoading.value = false
  }
}
</script>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1d2939 0%, #344e41 100%);
  padding: 30px 0;
}
.auth-card {
  width: 440px;
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
.mb-12 {
  margin-bottom: 12px;
}
.panel {
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  padding: 14px;
  margin-bottom: 14px;
}
.panel-title {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 10px;
}
.panel-desc {
  margin: 0 0 10px;
  color: #6b7280;
  font-size: 13px;
}
.dev-box {
  margin-top: 12px;
}
.dev-link {
  word-break: break-all;
  color: #d97706;
  font-size: 12px;
  margin-top: 4px;
}
.ok-text {
  margin-top: 10px;
  color: #059669;
  font-size: 13px;
}
.auth-actions {
  margin-top: 8px;
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
