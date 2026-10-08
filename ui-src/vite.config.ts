import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// base /app/ - SPA живёт рядом со старой панелью (старая на /)
export default defineConfig({
  plugins: [vue()],
  base: '/app/',
  build: { outDir: '../internal/web/ui/app', emptyOutDir: true },
  server: {
    proxy: {
      '/api': { target: process.env.MAWG_API ?? 'http://192.168.0.1:8090' },
    },
  },
})
