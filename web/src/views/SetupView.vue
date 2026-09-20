<template>
  <div class="auth-page">
    <el-card class="auth-card" shadow="always">
      <h1 class="auth-title">初始化 OneCloud 管理面板</h1>
      <p class="auth-subtitle">首次使用，请创建管理员账号</p>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
        <el-form-item label="管理员用户名" prop="username">
          <el-input v-model="form.username" size="large" placeholder="3-32 位" :prefix-icon="User" autocomplete="username" />
        </el-form-item>
        <el-form-item label="登录密码" prop="password">
          <el-input v-model="form.password" type="password" size="large" show-password placeholder="至少 8 位"
            :prefix-icon="Lock" autocomplete="new-password" />
        </el-form-item>
        <el-form-item label="确认密码" prop="confirm">
          <el-input v-model="form.confirm" type="password" size="large" show-password placeholder="再次输入密码"
            :prefix-icon="Lock" autocomplete="new-password" @keyup.enter="submit" />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
          创建并进入面板
        </el-button>
      </el-form>
    </el-card>

    <!-- 初始化成功后展示管理员超级验证码 -->
    <SuperCodeDialog v-model="codeDlg" :code="superCode" @update:model-value="(v) => { if (!v && session.user) router.replace({ name: 'dashboard' }) }" />
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { post } from '../api/http'
import { initSession, session } from '../session'
import SuperCodeDialog from '../components/SuperCodeDialog.vue'

const router = useRouter()
const formRef = ref()
const loading = ref(false)
const form = reactive({ username: '', password: '', confirm: '' })

// 初始化成功后展示管理员超级验证码（仅一次）
const codeDlg = ref(false)
const superCode = ref('')

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 32, message: '长度需为 3-32', trigger: 'blur' }
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 8, max: 128, message: '长度需为 8-128', trigger: 'blur' }
  ],
  confirm: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    {
      validator: (_r, v, cb) =>
        v === form.password ? cb() : cb(new Error('两次输入的密码不一致')),
      trigger: 'blur'
    }
  ]
}

async function submit() {
  await formRef.value.validate(async (valid) => {
    if (!valid || loading.value) return
    loading.value = true
    try {
      const resp = await post('/api/setup', { username: form.username.trim(), password: form.password })
      await initSession()
      if (session.user) {
        ElMessage.success('初始化完成，欢迎使用')
        if (resp?.super_code) {
          superCode.value = resp.super_code
          codeDlg.value = true
        } else {
          router.replace({ name: 'dashboard' })
        }
      }
    } catch (e) {
      ElMessage.error(e.message || '初始化失败')
    } finally {
      loading.value = false
    }
  })
}
</script>
