import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  // 后端端口：Go 后端默认 8001，可在 frontend/.env 中用 VITE_BACKEND_PORT 覆盖
  const env = loadEnv(mode, process.cwd(), '')
  const backendPort = env.VITE_BACKEND_PORT || '8001'

  return {
    plugins: [vue()],
    server: {
      allowedHosts: ['inkpot.cn'],
      host: '0.0.0.0',
      port: 5177,
      proxy: {
        '/api': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
        '/uploads': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
        // OneLink 接入层的两个入口也要代理, 而且它们**不在** /api 前缀下:
        //   /sso/landing  门户带票据把人送回来的落地页
        //   /logout       登出(由 SDK 的守卫处理, 要同时删会话行与清 cookie)
        // 漏了这两条的表现是"从门户点卡片进来落在前端 404 页", 而那看起来像门户配错了。
        '/sso': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
        '/logout': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
      },
    },
  }
})
