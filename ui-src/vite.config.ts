import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Сборка выдаёт в ../internal/web/ui/app ровно app.js + styles.css +
// index.html (плюс статику из public/); имена фиксированные, кэш-бастинг
// делает scripts/stamp-assets.mjs через ?v=<sha256-8>.
export default defineConfig({
  plugins: [vue()],
  base: '/app/',
  server: {
    proxy: {
      '/api': { target: process.env.MAWG_API ?? 'http://192.168.0.1:8090' },
    },
  },
  build: {
    outDir: '../internal/web/ui/app',
    emptyOutDir: true,
    target: 'es2018',
    cssCodeSplit: false,
    assetsInlineLimit: 0,
    sourcemap: false,
    rollupOptions: {
      output: {
        format: 'iife',
        inlineDynamicImports: true,
        entryFileNames: 'app.js',
        assetFileNames: 'styles.css',
      },
    },
  },
})
