<template>
  <el-container class="layout">
    <el-aside :width="collapsed ? '64px' : '210px'" class="sidebar">
      <div class="logo">
        <el-icon :size="24"><Cpu /></el-icon>
        <span v-show="!collapsed" class="logo-text">{{ session.panelName }}</span>
      </div>
      <el-menu :default-active="activeMenu" :collapse="collapsed" :collapse-transition="false"
        router class="sidebar-menu" background-color="#1f2d3d" text-color="#bfcbd9"
        active-text-color="#409eff">
        <el-menu-item v-for="item in visibleMenus" :key="item.to" :index="item.to">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title>{{ item.title }}</template>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <el-icon class="collapse-btn" :size="20" @click="collapsed = !collapsed">
          <Fold v-if="!collapsed" />
          <Expand v-else />
        </el-icon>
        <div class="header-right">
          <el-dropdown @command="onCommand">
            <span class="user-trigger">
              <el-icon><UserFilled /></el-icon>
              <span class="username">{{ session.user?.username }}</span>
              <el-tag size="small" type="info" effect="plain">{{ session.user?.role_name }}</el-tag>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">个人资料</el-dropdown-item>
                <el-dropdown-item command="changePassword">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>

    <ChangePasswordDialog v-model="pwdDlg" />

    <!-- 个人资料 -->
    <el-dialog v-model="profileDlg" title="个人资料" width="460px">
      <el-form label-width="100px">
        <el-form-item label="用户名">
          <el-input :model-value="session.user?.username" disabled />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="profileForm.real_name" placeholder="不超过 32 字" maxlength="32" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="profileForm.phone" maxlength="24" />
        </el-form-item>
        <el-divider content-position="left">通知方式</el-divider>
        <el-form-item label="通知方式">
          <el-select v-model="profileForm.notify_method" style="width: 100%">
            <el-option label="仅记录日志" value="log" />
            <el-option label="邮件" value="email" />
            <el-option label="短信" value="sms" />
            <el-option label="通知通道" value="channel" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="profileForm.notify_method === 'email'" label="接收邮箱">
          <el-input v-model="profileForm.notify_email" placeholder="留空则用系统配置的默认收件人" />
        </el-form-item>
        <el-form-item v-if="profileForm.notify_method === 'sms'" label="接收手机号">
          <el-input v-model="profileForm.notify_sms_phone" placeholder="接收短信验证码的手机号" maxlength="24" />
        </el-form-item>
        <el-form-item v-if="profileForm.notify_method === 'channel'" label="通知通道">
          <el-select v-model="profileForm.notify_channel_id" placeholder="请选择" style="width: 100%">
            <el-option v-for="c in notifyChannels" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <div v-if="!notifyChannels.length" class="tip">暂无启用中的通知通道，请先在「通知管理」中配置</div>
        </el-form-item>
        <el-form-item
          v-if="profileForm.notify_method === 'channel' && selectedChannel?.target_label"
          :label="targetLabel"
          :required="selectedChannel?.needs_target"
        >
          <el-input
            v-model="profileForm.notify_target"
            :type="selectedChannel?.target_secret ? 'password' : 'text'"
            :show-password="selectedChannel?.target_secret"
            :placeholder="'请输入' + targetLabel"
          />
          <div class="tip">{{ targetHint }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="profileDlg = false">取消</el-button>
        <el-button type="primary" :loading="savingProfile" @click="saveProfile">保存</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup>
import { ref, reactive, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, showErr } from '../api/http'
import { session } from '../session'
import ChangePasswordDialog from '../components/ChangePasswordDialog.vue'

const route = useRoute()
const router = useRouter()
const collapsed = ref(false)
const pwdDlg = ref(false)

// 个人资料
const profileDlg = ref(false)
const savingProfile = ref(false)
const notifyChannels = ref([])
const profileForm = reactive({
  real_name: '', phone: '',
  notify_method: 'log', notify_email: '', notify_sms_phone: '', notify_channel_id: null,
  notify_target: ''
})

// 当前所选通道的「每用户接收标识」需求描述：决定是否显示 / 是否必填。
const selectedChannel = computed(
  () => notifyChannels.value.find((c) => c.id === profileForm.notify_channel_id) || null
)
const targetLabel = computed(() => selectedChannel.value?.target_label || '接收标识')
const targetHint = computed(() => selectedChannel.value?.target_hint || '留空则使用通道默认接收方')

async function saveProfile() {
  if (profileForm.notify_method === 'channel') {
    if (!profileForm.notify_channel_id) {
      ElMessage.warning('请选择通知通道')
      return
    }
    if (selectedChannel.value?.needs_target && !String(profileForm.notify_target || '').trim()) {
      ElMessage.warning(`请填写${targetLabel.value}`)
      return
    }
  }
  savingProfile.value = true
  try {
    await put('/api/auth/profile', {
      real_name: profileForm.real_name,
      phone: profileForm.phone,
      notify_method: profileForm.notify_method,
      notify_email: profileForm.notify_email,
      notify_sms_phone: profileForm.notify_sms_phone,
      notify_channel_id: profileForm.notify_channel_id || 0,
      notify_target: profileForm.notify_target || ''
    })
    // 同步本地会话展示
    if (session.user) {
      session.user.real_name = profileForm.real_name
      session.user.phone = profileForm.phone
    }
    ElMessage.success('已保存')
    profileDlg.value = false
  } catch (e) {
    showErr(e, '保存失败')
  } finally {
    savingProfile.value = false
  }
}

async function loadNotifyChannels() {
  try {
    const d = await get('/api/auth/notify-channels')
    notifyChannels.value = d.items || []
  } catch {
    notifyChannels.value = []
  }
}

// 打开个人资料：以服务端最新资料为准（会话里的字段可能已过期）。
async function openProfile() {
  let me = session.user || {}
  try {
    me = await get('/api/auth/me')
  } catch {
    /* 读取失败时退回会话缓存 */
  }
  profileForm.real_name = me.real_name || ''
  profileForm.phone = me.phone || ''
  profileForm.notify_method = me.notify_method || 'log'
  profileForm.notify_email = me.notify_email || ''
  profileForm.notify_sms_phone = me.notify_sms_phone || ''
  profileForm.notify_channel_id = me.notify_channel_id || null
  profileForm.notify_target = me.notify_target || ''
  profileDlg.value = true
  await loadNotifyChannels()
}

watch(() => session.panelName, (n) => {
  if (n) document.title = n
}, { immediate: true })

const menus = [
  { to: '/dashboard', title: '仪表盘', icon: 'Odometer', perm: 'dashboard:read' },
  { to: '/nodes', title: '节点管理', icon: 'Monitor', perm: 'node:read' },
  { to: '/apps', title: '应用管理', icon: 'Grid', perm: 'app:read' },
  { to: '/users', title: '用户管理', icon: 'User', perm: 'user:read' },
  { to: '/roles', title: '角色权限', icon: 'Lock', perm: 'role:read' },
  { to: '/audit', title: '审计日志', icon: 'Document', perm: 'auditlog:read' },
  { to: '/notifications', title: '通知管理', icon: 'Bell', perm: 'settings:read' },
  { to: '/settings', title: '面板设置', icon: 'Setting', perm: 'settings:read' }
]

const visibleMenus = computed(() => menus.filter((m) => session.can(m.perm)))
const activeMenu = computed(() => '/' + (route.path.split('/')[1] || 'dashboard'))

async function onCommand(command) {
  if (command === 'profile') {
    await openProfile()
    return
  }
  if (command === 'changePassword') {
    pwdDlg.value = true
    return
  }
  if (command === 'logout') {
    try {
      await ElMessageBox.confirm('确定退出登录吗？', '提示', { type: 'warning' })
    } catch {
      return
    }
    try {
      await post('/api/auth/logout', {})
    } catch {
      // 即使接口失败也清除本地会话
    }
    session.clear()
    ElMessage.success('已退出登录')
    router.replace({ name: 'login' })
  }
}
</script>

<style scoped>
.layout {
  height: 100%;
}
.sidebar {
  background: #1f2d3d;
  transition: width 0.2s;
  overflow-x: hidden;
}
.logo {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 18px;
  color: #fff;
  white-space: nowrap;
}
.logo-text {
  font-size: 16px;
  font-weight: 600;
}
.sidebar-menu {
  border-right: none;
}
.sidebar-menu:not(.el-menu--collapse) {
  width: 210px;
}
.header {
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
}
.collapse-btn {
  cursor: pointer;
  color: #606266;
}
.user-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  outline: none;
}
.username {
  font-size: 14px;
  color: #303133;
}
.main {
  padding: 16px;
  background: #f2f4f8;
}
.tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}
</style>
