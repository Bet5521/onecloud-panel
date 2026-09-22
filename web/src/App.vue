<template>
  <router-view v-if="session.ready" />
  <div v-else class="boot-loading">
    <el-icon class="is-loading" :size="32"><Loading /></el-icon>
    <p>正在加载 OneCloud 集群管理面板…</p>
  </div>
</template>

<script setup>
import { watch } from 'vue'
import { useRouter } from 'vue-router'
import { session } from './session'

const router = useRouter()

// 会话失效（401）时跳转登录页。
// 这里**只负责导航**：expired 标记由 http.js 在真正收到 401 时置位。
// 原先在此无条件 `session.expired = true`，导致主动「退出登录」也被报成
// 「登录状态已过期，请重新登录」，与同时弹出的「已退出登录」自相矛盾。
watch(
  () => session.user,
  (u, old) => {
    if (!u && old) {
      const route = router.currentRoute.value
      if (!route.meta.public) {
        router.push({ name: 'login' })
      }
    }
  }
)
</script>
