<template>
  <el-container class="layout">
    <el-aside width="220px" class="aside">
      <div class="logo">
        <LogoMark :size="26" />
        <div class="logo-text">
          <span class="logo-name">躬行</span>
          <span class="logo-en">Peract</span>
        </div>
      </div>
      <el-menu
        :default-active="activeMenu"
        router
        background-color="#1d2939"
        text-color="#cbd5e1"
        active-text-color="#ffffff"
        class="menu"
      >
        <!--
          菜单由 utils/menu.js 配置 + 权限快照过滤而来, 不再逐条写死。
          写死的那一版要改一处菜单就得改这个模板, 而"某一项该不该出现"的判断散在
          每个 el-menu-item 上 —— 加了第四、第五个入口之后, 漏一个 v-if 的表现是
          "没权限的人也看得见入口", 点进去才报 403。
        -->
        <el-menu-item v-for="m in menuItems" :key="m.path" :index="m.path">
          <el-icon><component :is="m.icon" /></el-icon>
          <span>{{ m.title }}</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-title">{{ pageTitle }}</div>
        <div class="header-right">
          <ReminderBell />
          <el-dropdown @command="onCommand">
            <span class="user-info">
              <UserAvatar :src="auth.user?.avatar_url" :name="auth.user?.full_name || auth.user?.username" :size="30" />
              {{ auth.user?.full_name || auth.user?.username }}
              <el-tag v-if="auth.canSeeAllTasks" size="small" type="info" effect="dark">可看全部任务</el-tag>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="settings">
                  <el-icon><Setting /></el-icon>个人设置
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <el-icon><SwitchButton /></el-icon>退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { useAuthStore } from '../stores/auth'
import { visibleMenus } from '../utils/menu'
import { goToPortal, portalConfigured } from '../utils/portal'
import ReminderBell from '../components/ReminderBell.vue'
import UserAvatar from '../components/UserAvatar.vue'
import LogoMark from '../components/LogoMark.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

// 菜单的可见性跟着权限快照走。快照变了(重新登录)菜单就跟着变 —— 所以它必须是
// computed 而不是在 setup 里算一次: 这个布局被 keep-alive 留着, 重登之后不重挂。
const menuItems = computed(() => visibleMenus(auth.permissions, auth.isSuper))

const activeMenu = computed(() => {
  if (route.path.startsWith('/tasks/deleted')) return '/tasks/deleted'
  if (route.path.startsWith('/tasks')) return '/tasks'
  return route.path
})
const pageTitle = computed(() => route.meta.title || '')

async function onCommand(cmd) {
  if (cmd === 'settings') {
    router.push('/settings')
    return
  }
  if (cmd === 'logout') {
    const tip = portalConfigured()
      ? '确定退出登录吗？退出后需要回到 OneLink 门户重新进入躬行。'
      : '确定退出登录吗？'
    try {
      await ElMessageBox.confirm(tip, '提示', { type: 'warning' })
    } catch {
      // 取消时 ElMessageBox 是 reject，不接住就是一条未处理的 Promise 异常
      return
    }
    await auth.logout()
    // 回门户, 而不是回一个本地登录页(那个页面已经删了)。
    // 这里**不** router.push: 应用内已经没有任何"能让人重新进来"的页面了,
    // 唯一的路是从门户点卡片 —— 而那一步会带一张新票据回来。
    goToPortal()
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}
.aside {
  background-color: #1d2939;
  display: flex;
  flex-direction: column;
}
.logo {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #fff;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.logo-text {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  line-height: 1.15;
}
.logo-name {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 2px;
}
.logo-en {
  font-size: 10px;
  letter-spacing: 1px;
  color: #9bb7b0;
}
.menu {
  border-right: none;
  flex: 1;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e5e7eb;
  height: 56px;
}
.header-title {
  font-size: 16px;
  font-weight: 600;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 20px;
}
.user-info {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  outline: none;
}
.main {
  background: #f3f4f6;
  padding: 16px;
}
</style>
