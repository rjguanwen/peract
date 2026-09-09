import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  // 后端端口：默认指向 Go 后端(8001)；切回 Python 后端时设为 8000
  // 也可在 frontend/.env 中配置 VITE_BACKEND_PORT=8000
  const env = loadEnv(mode, process.cwd(), '')
  const backendPort = env.VITE_BACKEND_PORT || '8001'

  return {
    plugins: [vue()],
    server: {
      allowedHosts: ['inkpot.cn'],
      host: '0.0.0.0',
      port: 5173,
      proxy: {
        '/api': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
        '/uploads': {
          target: `http://localhost:${backendPort}`,
          changeOrigin: true,
        },
      },
    },
  }
})
