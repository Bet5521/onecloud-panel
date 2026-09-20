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

// 会话失效（401）时跳转登录页
watch(
  () => session.user,
  (u, old) => {
    if (!u && old) {
      const route = router.currentRoute.value
      if (!route.meta.public) {
        session.expired = true
        router.push({ name: 'login' })
      }
    }
  }
)
</script>
