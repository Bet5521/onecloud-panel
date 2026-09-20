<template>
  <div class="auth-page">
    <el-card class="auth-card" shadow="always">
      <h1 class="auth-title">OneCloud 集群管理面板</h1>

      <!-- ============ 登录 ============ -->
      <template v-if="mode === 'login'">
        <p class="auth-subtitle">请登录后管理您的玩客云集群</p>

        <el-alert v-if="session.expired" title="登录状态已过期，请重新登录" type="warning"
          :closable="true" @close="session.expired = false" style="margin-bottom: 16px" />

        <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
          <el-form-item label="用户名" prop="username">
            <el-input v-model="form.username" size="large" placeholder="请输入用户名"
              :prefix-icon="User" autocomplete="username" />
          </el-form-item>
          <el-form-item label="密码" prop="password">
            <el-input v-model="form.password" type="password" size="large" show-password placeholder="请输入密码"
              :prefix-icon="Lock" autocomplete="current-password" @keyup.enter="submit" />
          </el-form-item>
          <div class="form-foot">
            <el-button text type="primary" @click="goReset">忘记密码？</el-button>
          </div>
          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
            登 录
          </el-button>
        </el-form>
      </template>

      <!-- ============ 重置密码 步骤1：选择方式并申请 ============ -->
      <template v-if="mode === 'reset1'">
        <p class="auth-subtitle">自助重置密码</p>
        <el-form ref="r1Ref" :model="r1" :rules="r1Rules" label-position="top" @submit.prevent="sendRequest">
          <el-form-item label="用户名" prop="username">
            <el-input v-model="r1.username" size="large" placeholder="请输入要重置的用户名"
              :prefix-icon="User" autocomplete="username" />
          </el-form-item>
          <el-form-item label="验证方式">
            <el-radio-group v-model="r1.method">
              <el-radio-button value="super_code">超级验证码</el-radio-button>
              <el-radio-button value="email">邮箱</el-radio-button>
              <el-radio-button value="notification">通知</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-alert :type="r1.method === 'super_code' ? 'warning' : 'info'" :closable="false" show-icon
            style="margin-bottom: 16px">
            <template #default>
              <template v-if="r1.method === 'super_code'">
                使用创建账号/管理员发放的 10 位超级验证码，直接设置新密码。
              </template>
              <template v-else-if="r1.method === 'email'">
                重置码将发送至该用户绑定的邮箱（需配置 SMTP），同时输出到面板服务日志：
                <code>journalctl -u onecloud-panel</code>
              </template>
              <template v-else>
                重置码将按该用户绑定的通知方式（邮箱/短信/通知通道）定向下发，
                同时输出到面板服务日志。
              </template>
            </template>
          </el-alert>
          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="sendRequest">
            {{ r1.method === 'super_code' ? '下一步' : '申请重置码' }}
          </el-button>
        </el-form>
        <div class="back-link">
          <el-button text :icon="ArrowLeft" @click="mode = 'login'">返回登录</el-button>
        </div>
      </template>

      <!-- ============ 重置密码 步骤2：输入验证码与新密码 ============ -->
      <template v-if="mode === 'reset2'">
        <p class="auth-subtitle">输入验证码与新密码</p>
        <el-alert :type="r1.method === 'super_code' ? 'warning' : 'success'" :closable="false" show-icon
          style="margin-bottom: 16px">
          <template #default>
            <template v-if="r1.method === 'super_code'">
              请输入 10 位超级验证码（不区分大小写）。
            </template>
            <template v-else>
              重置申请已提交，请查收邮件或通知消息，也可查看面板服务日志获取 8 位重置码（15 分钟内有效）。
            </template>
          </template>
        </el-alert>
        <el-form ref="r2Ref" :model="r2" :rules="r2Rules" label-position="top" @submit.prevent="confirmReset">
          <el-form-item label="用户名" prop="username">
            <el-input v-model="r2.username" size="large" :prefix-icon="User" disabled />
          </el-form-item>
          <el-form-item :label="r1.method === 'super_code' ? '超级验证码' : '重置码'" prop="code">
            <el-input v-model="r2.code" size="large" :placeholder="codePlaceholder"
              :maxlength="r1.method === 'super_code' ? 10 : 8"
              :prefix-icon="Key" style="text-transform: uppercase" />
          </el-form-item>
          <el-form-item label="新密码" prop="newPassword">
            <el-input v-model="r2.newPassword" type="password" size="large" show-password
              placeholder="至少 8 位" :prefix-icon="Lock" autocomplete="new-password" />
          </el-form-item>
          <el-form-item label="确认新密码" prop="confirmPassword">
            <el-input v-model="r2.confirmPassword" type="password" size="large" show-password
              placeholder="再次输入新密码" :prefix-icon="Lock" autocomplete="new-password"
              @keyup.enter="confirmReset" />
          </el-form-item>
          <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="confirmReset">
            重置密码
          </el-button>
        </el-form>
        <div class="back-link">
          <el-button text :icon="ArrowLeft" @click="mode = 'login'">返回登录</el-button>
        </div>
      </template>
    </el-card>

    <!-- 重置成功后展示刷新的超级验证码 -->
    <SuperCodeDialog v-model="codeDlg" :code="newSuperCode" />
  </div>
</template>

<script setup>
import { ref, reactive, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock, Key, ArrowLeft } from '@element-plus/icons-vue'
import { post } from '../api/http'
import { initSession, session } from '../session'
import SuperCodeDialog from '../components/SuperCodeDialog.vue'

const router = useRouter()
const route = useRoute()
const mode = ref('login')
const loading = ref(false)

// ---- 登录 ----
const formRef = ref()
const form = reactive({ username: '', password: '' })
const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }]
}

async function submit() {
  await formRef.value.validate(async (valid) => {
    if (!valid || loading.value) return
    loading.value = true
    try {
      await post('/api/auth/login', { username: form.username.trim(), password: form.password })
      await initSession()
      session.expired = false
      ElMessage.success('登录成功')
      const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
      router.replace(redirect)
    } catch (e) {
      if (e.status === 429) {
        ElMessage.error('登录尝试过于频繁，请稍后再试（限流）')
      } else {
        ElMessage.error(e.message || '登录失败')
      }
    } finally {
      loading.value = false
    }
  })
}

// ---- 密码重置 ----
const r1Ref = ref()
const r1 = reactive({ username: '', method: 'super_code' })
const r1Rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 32, message: '用户名长度 3-32', trigger: 'blur' }
  ]
}

const r2Ref = ref()
const r2 = reactive({ username: '', code: '', newPassword: '', confirmPassword: '' })
const codePlaceholder = computed(() =>
  r1.method === 'super_code' ? '10 位超级验证码' : '8 位重置码')
const codeRules = computed(() => r1.method === 'super_code'
  ? [
      { required: true, message: '请输入超级验证码', trigger: 'blur' },
      { len: 10, message: '超级验证码为 10 位', trigger: 'blur' }
    ]
  : [
      { required: true, message: '请输入重置码', trigger: 'blur' },
      { len: 8, message: '重置码为 8 位', trigger: 'blur' }
    ])
const r2Rules = computed(() => ({
  code: codeRules.value,
  newPassword: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, max: 128, message: '密码长度需为 8-128 位', trigger: 'blur' }
  ],
  confirmPassword: [
    { required: true, message: '请再次输入新密码', trigger: 'blur' },
    {
      validator: (_rule, value, callback) =>
        value === r2.newPassword ? callback() : callback(new Error('两次输入的密码不一致')),
      trigger: 'blur'
    }
  ]
}))

// 重置成功后展示刷新的超级验证码
const codeDlg = ref(false)
const newSuperCode = ref('')

function goReset() {
  r1.username = form.username || ''
  r1.method = 'super_code'
  mode.value = 'reset1'
}

async function sendRequest() {
  await r1Ref.value.validate(async (valid) => {
    if (!valid || loading.value) return
    loading.value = true
    try {
      await post('/api/auth/password-reset/request', {
        username: r1.username.trim(),
        method: r1.method
      })
      r2.username = r1.username.trim()
      r2.code = ''
      r2.newPassword = ''
      r2.confirmPassword = ''
      mode.value = 'reset2'
      ElMessage.success(r1.method === 'super_code' ? '请输入超级验证码' : '重置申请已提交')
    } catch (e) {
      ElMessage.error(e.message || '申请失败')
    } finally {
      loading.value = false
    }
  })
}

async function confirmReset() {
  await r2Ref.value.validate(async (valid) => {
    if (!valid || loading.value) return
    loading.value = true
    try {
      const resp = await post('/api/auth/password-reset/confirm', {
        username: r2.username,
        code: r2.code.trim().toUpperCase(),
        new_password: r2.newPassword,
        method: r1.method
      })
      form.username = r2.username
      form.password = ''
      mode.value = 'login'
      if (resp?.super_code) {
        newSuperCode.value = resp.super_code
        codeDlg.value = true
      } else {
        ElMessage.success('密码已重置，请使用新密码登录')
      }
    } catch (e) {
      ElMessage.error(e.message || '重置失败')
    } finally {
      loading.value = false
    }
  })
}
</script>

<style scoped>
.form-foot {
  text-align: right;
  margin: -8px 0 8px;
}
.back-link {
  margin-top: 12px;
  text-align: center;
}
code {
  background: #f4f4f5;
  padding: 1px 4px;
  border-radius: 3px;
  font-size: 12px;
}
</style>
