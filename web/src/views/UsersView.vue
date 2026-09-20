<template>
  <div>
    <div class="page-head">
      <h2>用户管理</h2>
      <el-button v-if="can('user:write')" type="primary" :icon="Plus" @click="openCreate">新建用户</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="users" v-loading="loading" stripe>
        <el-table-column prop="username" label="用户名" min-width="120" />
        <el-table-column prop="real_name" label="姓名" min-width="90">
          <template #default="{ row }">{{ row.real_name || '—' }}</template>
        </el-table-column>
        <el-table-column prop="phone" label="手机号" min-width="120">
          <template #default="{ row }">{{ row.phone || '—' }}</template>
        </el-table-column>
        <el-table-column label="角色" min-width="110">
          <template #default="{ row }">
            <el-tag :type="row.role_code === 'admin' ? 'danger' : row.role_code === 'operator' ? 'warning' : 'info'" effect="plain">
              {{ row.role_name }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'">
              {{ row.status === 'active' ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="150">
          <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column v-if="can('user:write')" label="操作" width="270" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="warning" @click="openReset(row)">重置密码</el-button>
            <el-button link type="success" @click="onRotateCode(row)">重置验证码</el-button>
            <el-button link type="danger" :disabled="row.id === session.user?.id" @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建用户 -->
    <el-dialog v-model="createDlg" title="新建用户" width="440px">
      <el-form ref="createFormRef" :model="createForm" :rules="rules" label-width="90px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="createForm.username" placeholder="3-32 位" />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="createForm.real_name" placeholder="可选，不超过 32 字" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="createForm.phone" placeholder="可选" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="createForm.password" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
        <el-form-item label="角色" prop="role_id">
          <el-select v-model="createForm.role_id" placeholder="选择角色" style="width: 100%">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDlg = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 编辑用户 -->
    <el-dialog v-model="editDlg" title="编辑用户" width="440px">
      <el-form label-width="90px">
        <el-form-item label="用户名">
          <el-input :model-value="editRow?.username" disabled />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="editForm.real_name" placeholder="不超过 32 字" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="editForm.phone" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="editForm.role_id" style="width: 100%">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态">
          <el-radio-group v-model="editForm.status">
            <el-radio value="active">启用</el-radio>
            <el-radio value="disabled" :disabled="editRow?.id === session.user?.id">停用</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-divider content-position="left">通知方式</el-divider>
        <el-form-item label="通知方式">
          <el-select v-model="editForm.notify_method" style="width: 100%">
            <el-option label="仅记录日志" value="log" />
            <el-option label="邮件" value="email" />
            <el-option label="短信" value="sms" />
            <el-option label="通知通道" value="channel" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="editForm.notify_method === 'email'" label="接收邮箱">
          <el-input v-model="editForm.notify_email" placeholder="留空则用系统默认收件人" />
        </el-form-item>
        <el-form-item v-if="editForm.notify_method === 'sms'" label="接收手机号">
          <el-input v-model="editForm.notify_sms_phone" maxlength="24" />
        </el-form-item>
        <el-form-item v-if="editForm.notify_method === 'channel'" label="通知通道">
          <el-select v-model="editForm.notify_channel_id" placeholder="请选择" style="width: 100%">
            <el-option v-for="c in notifyChannels" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <div v-if="!notifyChannels.length" class="tip">暂无启用中的通知通道</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDlg = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="resetDlg" title="重置密码" width="440px">
      <el-form label-width="90px">
        <el-form-item label="用户">
          <el-input :model-value="resetRow?.username" disabled />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input v-model="resetPwd" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false" show-icon>
        重置成功后该用户的超级验证码将自动刷新，请将新验证码告知用户。
      </el-alert>
      <template #footer>
        <el-button @click="resetDlg = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitReset">重置</el-button>
      </template>
    </el-dialog>

    <!-- 超级验证码展示（仅创建/重置时显示一次） -->
    <SuperCodeDialog v-model="codeDlg" :code="superCode" />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { get, post, put, del } from '../api/http'
import { session } from '../session'
import { fmtTime } from '../utils'
import SuperCodeDialog from '../components/SuperCodeDialog.vue'

const can = (p) => session.can(p)

const users = ref([])
const roles = ref([])
const loading = ref(false)
const saving = ref(false)

// 超级验证码展示
const codeDlg = ref(false)
const superCode = ref('')
function showSuperCode(code) {
  superCode.value = code
  codeDlg.value = true
}

async function load() {
  loading.value = true
  try {
    const [u, r] = await Promise.all([get('/api/users'), get('/api/roles')])
    users.value = Array.isArray(u.items) ? u.items : []
    roles.value = Array.isArray(r.items) ? r.items : []
  } finally {
    loading.value = false
  }
}

// 新建
const createDlg = ref(false)
const createFormRef = ref(null)
const createForm = ref({ username: '', real_name: '', phone: '', password: '', role_id: null })
const rules = {
  username: [{ required: true, min: 3, max: 32, message: '用户名 3-32 位', trigger: 'blur' }],
  password: [{ required: true, min: 8, max: 128, message: '密码 8-128 位', trigger: 'blur' }],
  role_id: [{ required: true, message: '请选择角色', trigger: 'change' }]
}

function openCreate() {
  createForm.value = { username: '', real_name: '', phone: '', password: '', role_id: roles.value[0]?.id ?? null }
  createDlg.value = true
}
async function submitCreate() {
  const ok = await createFormRef.value.validate().catch(() => false)
  if (!ok) return
  saving.value = true
  try {
    const created = await post('/api/users', createForm.value)
    ElMessage.success('用户已创建')
    createDlg.value = false
    if (created?.super_code) {
      showSuperCode(created.super_code)
    }
    load()
  } finally {
    saving.value = false
  }
}

// 编辑
const editDlg = ref(false)
const editRow = ref(null)
const notifyChannels = ref([])
const editForm = ref({
  role_id: null, status: 'active', real_name: '', phone: '',
  notify_method: 'log', notify_email: '', notify_sms_phone: '', notify_channel_id: null
})

async function loadNotifyChannels() {
  try {
    const d = await get('/api/auth/notify-channels')
    notifyChannels.value = d.items || []
  } catch {
    notifyChannels.value = []
  }
}

function openEdit(row) {
  editRow.value = row
  editForm.value = {
    role_id: row.role_id, status: row.status,
    real_name: row.real_name || '', phone: row.phone || '',
    notify_method: row.notify_method || 'log',
    notify_email: row.notify_email || '',
    notify_sms_phone: row.notify_sms_phone || '',
    notify_channel_id: row.notify_channel_id || null
  }
  editDlg.value = true
  loadNotifyChannels()
}
async function submitEdit() {
  if (editForm.value.notify_method === 'channel' && !editForm.value.notify_channel_id) {
    ElMessage.warning('请选择通知通道')
    return
  }
  saving.value = true
  try {
    await put(`/api/users/${editRow.value.id}`, {
      role_id: editForm.value.role_id,
      status: editForm.value.status,
      real_name: editForm.value.real_name,
      phone: editForm.value.phone,
      notify_method: editForm.value.notify_method,
      notify_email: editForm.value.notify_email,
      notify_sms_phone: editForm.value.notify_sms_phone,
      notify_channel_id: editForm.value.notify_channel_id || 0
    })
    ElMessage.success('已保存')
    editDlg.value = false
    load()
  } finally {
    saving.value = false
  }
}

// 重置密码（成功后返回新超级验证码）
const resetDlg = ref(false)
const resetRow = ref(null)
const resetPwd = ref('')

function openReset(row) {
  resetRow.value = row
  resetPwd.value = ''
  resetDlg.value = true
}
async function submitReset() {
  if (resetPwd.value.length < 8 || resetPwd.value.length > 128) {
    ElMessage.warning('新密码长度需为 8-128')
    return
  }
  saving.value = true
  try {
    const resp = await post(`/api/users/${resetRow.value.id}/password`, { password: resetPwd.value })
    ElMessage.success('密码已重置')
    resetDlg.value = false
    if (resp?.super_code) {
      showSuperCode(resp.super_code)
    }
  } finally {
    saving.value = false
  }
}

// 重置超级验证码
async function onRotateCode(row) {
  try {
    await ElMessageBox.confirm(
      `将为用户「${row.username}」生成新的超级验证码，旧码立即失效。继续吗？`,
      '重置验证码', { type: 'warning', confirmButtonText: '生成' }
    )
  } catch {
    return
  }
  const resp = await post(`/api/users/${row.id}/super-code`, {})
  if (resp?.super_code) {
    showSuperCode(resp.super_code)
  }
}

// 删除
async function onDelete(row) {
  try {
    await ElMessageBox.confirm(`确定删除用户「${row.username}」吗？此操作不可恢复。`, '删除确认', {
      type: 'warning', confirmButtonText: '删除'
    })
  } catch {
    return
  }
  await del(`/api/users/${row.id}`)
  ElMessage.success('已删除')
  load()
}

onMounted(load)
</script>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.page-head h2 {
  margin: 0;
  font-size: 18px;
}
.tip {
  font-size: 12px;
  color: #909399;
}
</style>
