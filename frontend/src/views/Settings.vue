<template>
  <div class="settings-wrap">
    <div class="page-header">
      <div class="page-title">个人设置</div>
      <div class="page-sub">管理你的个人资料与账号安全</div>
    </div>

    <div class="settings-grid">
      <!-- 左侧 -->
      <div class="col space-y-4">
        <!-- 个人资料 -->
        <el-card shadow="never">
          <template #header>
            <span class="font-semibold">个人资料</span>
          </template>

          <div class="avatar-row">
            <UserAvatar :src="avatarUrl" :name="auth.user?.full_name || auth.user?.username" :size="64" />
            <div class="avatar-actions">
              <el-button size="small" :loading="avatarUploading" @click="avatarInput?.click()">
                <el-icon class="mr-1"><Camera /></el-icon>上传 / 更换头像
              </el-button>
              <input
                ref="avatarInput"
                type="file"
                accept="image/jpeg,image/png,image/gif,image/webp"
                class="hidden"
                @change="onAvatarChange"
              />
              <p class="hint">支持 jpg / png / gif / webp，不超过 5MB</p>
            </div>
          </div>

          <el-form label-position="top">
            <el-form-item label="姓名 / 昵称" required>
              <el-input v-model="form.full_name" placeholder="全站显示的名称" />
            </el-form-item>
            <el-form-item label="邮箱">
              <el-input :model-value="auth.user?.email" disabled />
            </el-form-item>
            <el-form-item label="个性签名">
              <el-input
                v-model="form.signature"
                type="textarea"
                :rows="2"
                maxlength="80"
                show-word-limit
                placeholder="一句话介绍自己，最多 80 字"
              />
            </el-form-item>
            <el-button type="primary" :loading="savingProfile" @click="saveProfile">保存修改</el-button>
          </el-form>
        </el-card>

        <!-- 账号信息 -->
        <el-card shadow="never">
          <template #header>
            <span class="font-semibold">账号信息</span>
          </template>
          <div class="account-info">
            <div>用户名：{{ auth.user?.username }}</div>
            <div>账号 ID：{{ auth.user?.id }}</div>
            <div>角色：{{ auth.isAdmin ? '管理员' : '普通用户' }}</div>
            <div>注册时间：{{ registeredAt }}</div>
          </div>
        </el-card>
      </div>

      <!-- 右侧 -->
      <div class="col space-y-4">
        <!-- 修改密码 -->
        <el-card shadow="never">
          <template #header>
            <span class="font-semibold">修改密码</span>
          </template>
          <el-form label-position="top">
            <el-form-item label="原密码" required>
              <el-input v-model="pwdForm.oldPassword" type="password" show-password placeholder="请输入原密码" />
            </el-form-item>
            <el-form-item label="新密码" required>
              <el-input v-model="pwdForm.newPassword" type="password" show-password placeholder="至少 6 个字符" />
            </el-form-item>
            <el-form-item label="确认新密码" required>
              <el-input v-model="pwdForm.confirm" type="password" show-password placeholder="再次输入新密码" @keyup.enter="changePassword" />
            </el-form-item>
            <el-button type="primary" plain :loading="changingPwd" @click="changePassword">确认修改</el-button>
          </el-form>
        </el-card>

        <!-- 安全设置 -->
        <el-card shadow="never">
          <template #header>
            <span class="font-semibold">安全设置</span>
          </template>
          <p class="sec-desc">
            设置「密码提示词」与「安全问题」后，忘记密码时可前往登录页「忘记密码」通过问答重置密码。
          </p>

          <el-form label-position="top">
            <div class="hint-block">
              <el-form-item label="密码提示词（可选）" class="mb-0">
                <div class="hint-line">
                  <el-input
                    v-model="secForm.passwordHint"
                    placeholder="给自己一句能想起密码的提示，如：常用昵称+数字组合"
                  />
                  <el-button type="primary" plain :loading="secSavingHint" @click="saveHint">保存</el-button>
                </div>
              </el-form-item>
            </div>

            <div class="qa-block">
              <div class="qa-title">找回安全问题</div>
              <el-alert
                v-if="answerLegacy"
                type="warning"
                :closable="false"
                show-icon
                class="qa-alert"
                title="该账号的安全答案存于旧版本，已不再用于校验，请重新设置一次才能通过问答找回密码。"
              />
              <p v-if="secForm.existingQuestion" class="qa-current">
                当前问题：<span>{{ secForm.existingQuestion }}</span>（修改问题时需重新填写答案）
              </p>
              <el-form-item label="选择问题" class="mb-2">
                <el-select
                  v-model="secForm.securityQuestion"
                  placeholder="选择或输入自定义问题"
                  filterable
                  allow-create
                  default-first-option
                  clearable
                  style="width: 100%"
                >
                  <el-option v-for="q in securityQuestions" :key="q" :label="q" :value="q" />
                </el-select>
              </el-form-item>
              <el-form-item label="答案（仅用于找回验证）" class="mb-2">
                <el-input v-model="secForm.securityAnswer" placeholder="回答你的安全问题" />
              </el-form-item>
              <div class="qa-btns">
                <el-button type="primary" plain :loading="secSavingQA" @click="saveSecurityQA">
                  {{ secForm.existingQuestion ? '更新问答' : '保存问答' }}
                </el-button>
                <el-button
                  v-if="secForm.existingQuestion"
                  type="danger"
                  plain
                  :loading="secClearing"
                  @click="clearSecurityQA"
                >清除问答</el-button>
              </div>
            </div>
          </el-form>
        </el-card>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAuthStore } from '../stores/auth'
import { profileApi, authApi } from '../api'
import UserAvatar from '../components/UserAvatar.vue'
import { formatDateTime } from '../utils/constants'

const auth = useAuthStore()
const router = useRouter()

const savingProfile = ref(false)
const changingPwd = ref(false)
const avatarUploading = ref(false)
const avatarInput = ref(null)

const avatarUrl = computed(() => auth.user?.avatar_url || '')
const registeredAt = computed(() => formatDateTime(auth.user?.created_at))

const form = reactive({
  full_name: auth.user?.full_name || '',
  signature: auth.user?.signature || '',
})

const pwdForm = reactive({ oldPassword: '', newPassword: '', confirm: '' })

// ===== 头像 =====
function onAvatarChange(e) {
  const file = e.target.files?.[0]
  if (avatarInput.value) avatarInput.value.value = ''
  if (!file) return
  if (file.size > 5 * 1024 * 1024) {
    ElMessage.warning('图片不能超过 5MB')
    return
  }
  uploadAvatar(file)
}

async function uploadAvatar(file) {
  avatarUploading.value = true
  try {
    const user = await profileApi.uploadAvatar(file)
    auth.applyUser(user)
    ElMessage.success('头像已更新')
  } catch {
    /* 拦截器已提示 */
  } finally {
    avatarUploading.value = false
  }
}

async function saveProfile() {
  if (!form.full_name.trim()) {
    ElMessage.warning('姓名 / 昵称不能为空')
    return
  }
  savingProfile.value = true
  try {
    const user = await profileApi.update({
      full_name: form.full_name,
      signature: form.signature,
    })
    auth.applyUser(user)
    ElMessage.success('已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    savingProfile.value = false
  }
}

async function changePassword() {
  if (!pwdForm.oldPassword) return ElMessage.warning('请输入原密码')
  if (pwdForm.newPassword.length < 6) return ElMessage.warning('新密码至少需要 6 个字符')
  if (pwdForm.newPassword !== pwdForm.confirm) return ElMessage.warning('两次输入的新密码不一致')
  changingPwd.value = true
  try {
    await authApi.changePassword({
      oldPassword: pwdForm.oldPassword,
      newPassword: pwdForm.newPassword,
    })
    // 后端会推送口令版本号并吊销当前令牌，旧令牌立刻失效，
    // 不把本地会话一并清掉的话，用户会停在原地发现哪个按钮都在报错
    ElMessage.success('密码修改成功，请使用新密码重新登录')
    auth.clear()
    router.push('/login')
  } catch {
    /* 拦截器已提示 */
  } finally {
    changingPwd.value = false
  }
}

// ===== 安全设置 =====
const securityQuestions = [
  '你母亲的姓名是？',
  '你父亲的姓名是？',
  '你的出生城市是？',
  '你最喜欢的电影是？',
  '你的小学名称是？',
  '你初中班主任的名字是？',
  '你的宠物名字是？',
]

const secForm = reactive({
  passwordHint: '',
  securityQuestion: '',
  securityAnswer: '',
  existingQuestion: '',
})
const secSavingHint = ref(false)
const secSavingQA = ref(false)
const secClearing = ref(false)
// 旧版存的是明文答案，后端已不再用它做校验，需要提示用户重设一次
const answerLegacy = ref(false)

async function loadSecurity() {
  try {
    const data = await authApi.getSecurity()
    secForm.passwordHint = data.password_hint || ''
    secForm.existingQuestion = data.security_question || ''
    secForm.securityQuestion = data.security_question || ''
    secForm.securityAnswer = ''
    answerLegacy.value = !!data.answer_legacy
  } catch {
    /* 拦截器已提示 */
  }
}

async function saveHint() {
  secSavingHint.value = true
  try {
    await authApi.setSecurity({ passwordHint: secForm.passwordHint })
    ElMessage.success('密码提示词已保存')
    loadSecurity()
  } catch {
    /* 拦截器已提示 */
  } finally {
    secSavingHint.value = false
  }
}

async function saveSecurityQA() {
  if (!secForm.securityQuestion) return ElMessage.warning('请选择或输入安全问题')
  if (!secForm.securityAnswer.trim()) return ElMessage.warning('请填写安全问题答案')
  secSavingQA.value = true
  try {
    await authApi.setSecurity({
      securityQuestion: secForm.securityQuestion,
      securityAnswer: secForm.securityAnswer,
    })
    ElMessage.success('安全问题已保存')
    loadSecurity()
  } catch {
    /* 拦截器已提示 */
  } finally {
    secSavingQA.value = false
  }
}

async function clearSecurityQA() {
  try {
    await ElMessageBox.confirm('确定清除安全问题？清除后将无法通过问答找回密码。', '提示', { type: 'warning' })
  } catch {
    return
  }
  secClearing.value = true
  try {
    await authApi.setSecurity({ securityQuestion: '', securityAnswer: '' })
    ElMessage.success('已清除安全问题')
    loadSecurity()
  } catch {
    /* 拦截器已提示 */
  } finally {
    secClearing.value = false
  }
}

loadSecurity()
</script>

<style scoped>
.settings-wrap {
  max-width: 1080px;
  margin: 0 auto;
}
.page-header {
  margin-bottom: 16px;
}
.page-title {
  font-size: 18px;
  font-weight: 600;
}
.page-sub {
  color: #6b7280;
  font-size: 13px;
  margin-top: 2px;
}
.settings-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  align-items: start;
}
@media (max-width: 900px) {
  .settings-grid {
    grid-template-columns: 1fr;
  }
}
.space-y-4 > * + * {
  margin-top: 16px;
}
.font-semibold {
  font-weight: 600;
}
.avatar-row {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
}
.avatar-actions {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}
.hint {
  margin: 0;
  color: #9ca3af;
  font-size: 12px;
}
.hidden {
  display: none;
}
.account-info {
  color: #4b5563;
  font-size: 14px;
  line-height: 1.9;
}
.sec-desc {
  margin: 0 0 14px;
  color: #6b7280;
  font-size: 13px;
  line-height: 1.6;
}
.hint-line {
  display: flex;
  gap: 10px;
  width: 100%;
}
.qa-block {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid #f3f4f6;
}
.qa-title {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 10px;
}
.qa-current {
  margin: 0 0 10px;
  color: #9ca3af;
  font-size: 12px;
}
.qa-alert {
  margin-bottom: 10px;
}
.qa-current span {
  color: #4b5563;
}
.qa-btns {
  display: flex;
  gap: 10px;
}
</style>
