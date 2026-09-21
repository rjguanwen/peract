<template>
  <div class="settings-wrap">
    <div class="page-header">
      <div class="page-title">个人设置</div>
      <div class="page-sub">管理你的个人资料</div>
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
            <!--
              姓名与邮箱是**只读**的。
              它们由 OneLink 维护: 后端每次请求都会拿平台那份同步过来, 所以在这里改完,
              下一次请求就会被覆盖回去 —— 表现是"改了自己的名字, 过一会儿又变回来了"。
              与其接住它再报错, 不如把话说在前面。
            -->
            <el-form-item label="姓名 / 昵称">
              <el-input :model-value="auth.user?.full_name" disabled />
            </el-form-item>
            <el-form-item label="邮箱">
              <el-input :model-value="auth.user?.email" disabled />
            </el-form-item>
            <p class="hint managed-hint">
              姓名、邮箱与头像由 OneLink 维护。要修改请回到门户的「个人资料」。
            </p>
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
            <div>数据范围：{{ auth.canSeeAllTasks ? '全部任务' : '仅与自己相关的任务' }}</div>
            <div>创建时间：{{ registeredAt }}</div>
          </div>
          <p class="hint managed-hint">
            账号与权限由 OneLink 统一管理。口令修改、找回密码、以及"能做什么"的角色授权都在门户上，
            这里只展示结果。
          </p>
        </el-card>
      </div>

      <!-- 右侧 -->
      <div class="col space-y-4">
        <el-card shadow="never">
          <template #header>
            <span class="font-semibold">本次会话的权限</span>
          </template>
          <p class="hint managed-hint">
            下面是登录时从 OneLink 取到的权限快照，用于决定界面上的入口。它是**加速值**：
            真正的判定在服务端每一条接口上，而权限被改后这份快照最迟在下次登录时更新。
          </p>
          <div class="perm-list">
            <el-tag
              v-for="p in auth.permissions"
              :key="p"
              size="small"
              type="info"
              effect="plain"
              class="perm-tag"
            >{{ p }}</el-tag>
            <el-tag v-if="auth.isSuper" size="small" type="warning" effect="dark">超级管理员</el-tag>
            <span v-if="!auth.permissions.length && !auth.isSuper" class="dim">
              没有取到任何权限点。如果你确信自己应该能做事，请联系管理员在 OneLink 上为你授权。
            </span>
          </div>
        </el-card>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '../stores/auth'
import { profileApi } from '../api'
import UserAvatar from '../components/UserAvatar.vue'
import { formatDateTime } from '../utils/constants'

const auth = useAuthStore()

const savingProfile = ref(false)
const avatarUploading = ref(false)
const avatarInput = ref(null)

const avatarUrl = computed(() => auth.user?.avatar_url || '')
const registeredAt = computed(() => formatDateTime(auth.user?.created_at))

// 只有个性签名是可改的 —— 理由见模板里那段注释。
const form = reactive({
  signature: auth.user?.signature || '',
})

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
  savingProfile.value = true
  try {
    const user = await profileApi.update({ signature: form.signature })
    auth.applyUser(user)
    ElMessage.success('已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    savingProfile.value = false
  }
}
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
.managed-hint {
  line-height: 1.6;
  margin-bottom: 12px;
}
.hidden {
  display: none;
}
.account-info {
  color: #4b5563;
  font-size: 14px;
  line-height: 1.9;
}
.perm-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.perm-tag {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.dim {
  color: #9ca3af;
  font-size: 12px;
  line-height: 1.6;
}
</style>
