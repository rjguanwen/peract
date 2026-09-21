import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

import App from './App.vue'
import router from './router'
import { onUnauthorized } from './api'
import { useAuthStore } from './stores/auth'
import { goToPortal } from './utils/portal'

const app = createApp(App)

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

const pinia = createPinia()
app.use(pinia)
app.use(router)
app.use(ElementPlus, { locale: zhCn })

// 会话失效(平台登出、会话被清、单点登出轮询到)时统一收敛到"未登录"。
//
// 只清 store 是不够的: 页面会停在原处, 而上面每一个组件都会各自再报一次 401。
// 也不能只清"本地 cookie"—— 那张 cookie 是 HttpOnly 的, 前端根本碰不到它,
// 唯一的处置就是回门户重新进来。
//
// 判据用 401 而不是 403: 403 是"这个人没有这个权限", 把他送回门户只会让他重新登录一遍
// 再撞同一个 403 —— 那会把一个"找管理员授权"的问题变成一次登录循环。
onUnauthorized(() => {
  useAuthStore().clear()
  goToPortal()
})

// 首屏不在这里拉 /auth/me: 路由守卫会拉(而且它必须先拿到结果才能决定放不放行)。
// 两处都拉会让每次刷新多一次往返, 而更麻烦的是它们可能一个成功一个失败。
app.mount('#app')
