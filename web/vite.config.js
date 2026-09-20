import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接输出到 Go embed 目录 internal/web/assets
export default defineConfig({
  plugins: [vue()],
  base: '/',
  build: {
    outDir: '../internal/web/assets',
    emptyOutDir: true,
    assetsDir: 'static',
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // go:embed 会排除以 _ 或 . 开头的文件，chunk 命名不能有前导下划线
        chunkFileNames: 'static/chunk-[hash].js',
        entryFileNames: 'static/[name]-[hash].js',
        assetFileNames: 'static/[name]-[hash][extname]'
      }
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      }
    }
  }
})
