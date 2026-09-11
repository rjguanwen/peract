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

const app = createApp(App)

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

const pinia = createPinia()
app.use(pinia)
app.use(router)
app.use(ElementPlus, { locale: zhCn })

// 令牌失效（过期、被吊销、改密作废、账号被停用）时统一收敛到未登录态。
// 只清 localStorage 是不够的：Pinia 里仍然认为已登录，界面会停在原页面反复报错。
onUnauthorized(() => {
  useAuthStore().clear()
  // 等首屏导航落地再决定跳去哪，否则会拿初始占位路由覆盖掉用户真正想访问的地址；
  // 而在路由守卫完成之前，未访问受保护页面时也无需强行跳转
  router.isReady().then(() => {
    const current = router.currentRoute.value
    if (current.name !== 'login') {
      router.replace({ name: 'login', query: { redirect: current.fullPath } })
    }
  })
})

// 先用本地缓存渲染，再静默拉一次 /auth/me 校准角色与停用状态。
// 失败后的收尾交给上面的 401 回调，这里不阻塞首屏挂载。
const auth = useAuthStore()
if (auth.isLoggedIn) auth.restore()

app.mount('#app')
