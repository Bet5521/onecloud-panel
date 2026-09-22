<template>
  <div>
    <div class="page-head">
      <h2>通知管理</h2>
      <el-button v-if="can('settings:write')" type="primary" @click="openCreate">新建通道</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="items" v-loading="loading" empty-text="尚未配置通知通道">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="类型" width="150">
          <template #default="{ row }">
            <el-tag size="small">{{ row.type_name || row.type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-switch :model-value="row.enabled" :disabled="!can('settings:write') || !row.configured"
              @change="(v) => toggleEnabled(row, v)" />
            <div v-if="!row.configured" class="muted" style="font-size: 12px; margin-top: 2px">
              未完成配置
            </div>
            <el-tooltip v-else-if="!row.enabled" content="启用后用户方可在个人资料中选择该通知方式" placement="top">
              <span class="muted" style="font-size: 12px; margin-top: 2px">已停用</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="最近测试" width="160">
          <template #default="{ row }">
            <span v-if="row.tested_at">{{ fmtTime(Number(row.tested_at)) }}</span>
            <span v-else class="muted">未测试</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :loading="testingId === row.id" @click="test(row)">测试</el-button>
            <el-button size="small" :disabled="!can('settings:write')" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" type="danger" plain :disabled="!can('settings:write')"
              @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <p class="hint" style="margin-top: 12px">
        已启用的通道用于：用户按个人资料中绑定的通知方式接收密码重置码等重要通知。密钥字段回显为
        <code>******</code>，保存时保持不变即可。短信通道需选择云短信平台并完成配置后，才可用于下发验证码。
      </p>
    </el-card>

    <!-- 新建/编辑对话框 -->
    <el-dialog v-model="dlg" :title="editingId ? '编辑通道' : '新建通道'" width="520px" @closed="resetForm">
      <el-form ref="formRef" :model="form" label-width="110px">
        <el-form-item label="类型" required>
          <el-select v-model="form.type" :disabled="!!editingId" style="width: 100%"
            @change="onTypeChange">
            <el-option v-for="(label, key) in typeOptions" :key="key" :value="key" :label="label" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="64" placeholder="如 运维群机器人" />
        </el-form-item>

        <el-form-item v-if="form.type === 'sms'" label="短信平台" required>
          <el-select v-model="form.config.provider" style="width: 100%">
            <el-option v-for="(label, key) in smsProviders" :key="key" :value="key" :label="label" />
          </el-select>
          <span v-if="form.config.provider && !smsImplemented[form.config.provider]" class="hint">
            {{ smsProviders[form.config.provider] }}短信暂未实现，可先保存配置；以该通道下发验证码会被拒绝。
          </span>
        </el-form-item>

        <template v-for="f in currentFields" :key="f.key">
          <el-form-item :label="f.label">
            <el-input v-model="form.config[f.key]" :placeholder="f.placeholder"
              :show-password="f.secret" clearable />
            <span v-if="f.hint" class="hint">{{ f.hint }}</span>
          </el-form-item>
        </template>

        <el-form-item label="启用">
          <el-switch v-model="form.enabled" :disabled="!formConfigured" />
          <span v-if="!formConfigured" class="hint">
            请先填写完整配置后才能启用（未配置的通道不会出现在用户的通知方式选项中）。
          </span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, showErr } from '../api/http'
import { session } from '../session'
import { fmtTime } from '../utils'

const can = (p) => session.can(p)

const loading = ref(false)
const saving = ref(false)
const items = ref([])
const testingId = ref(null)

const dlg = ref(false)
const editingId = ref(null)
const formRef = ref()
const form = reactive({
  type: 'wxpusher',
  name: '',
  enabled: true,
  config: {}
})

// 各类型配置字段定义（secret 字段回显 ******，提交保持原值）
const typeOptions = {
  wxpusher: 'WxPusher',
  serverchan: 'Server酱',
  wecom: '企业微信机器人',
  dingtalk: '钉钉机器人',
  webhook: '通用 Webhook',
  sms: '短信'
}

// 短信平台：阿里云 / 腾讯云已实现；华为云 / 百度云预留（可保存，发送时提示暂未实现）
const smsProviders = { aliyun: '阿里云', tencent: '腾讯云', huawei: '华为云', baidu: '百度云' }
const smsImplemented = { aliyun: true, tencent: true, huawei: false, baidu: false }

const smsCommonFields = [
  { key: 'sign_name', label: '短信签名', placeholder: '云平台审核通过的签名' },
  { key: 'template_code', label: '模板 Code', placeholder: '云平台审核通过的模板 ID' },
  { key: 'region', label: '地域', placeholder: '阿里云默认 cn-hangzhou；腾讯云默认 ap-guangzhou' },
  { key: 'phone', label: '默认接收号码', placeholder: '可选；用户未绑定手机号时使用' }
]

const smsProviderFields = {
  aliyun: [
    { key: 'access_key_id', label: 'AccessKeyId', placeholder: 'RAM 用户 AccessKeyId' },
    { key: 'access_key_secret', label: 'AccessKeySecret', secret: true, placeholder: 'RAM 用户 AccessKeySecret' }
  ],
  tencent: [
    { key: 'secret_id', label: 'SecretId', placeholder: '腾讯云 SecretId' },
    { key: 'secret_key', label: 'SecretKey', secret: true, placeholder: '腾讯云 SecretKey' },
    { key: 'app_id', label: 'SmsSdkAppId', placeholder: '短信应用 SdkAppId，如 1400xxxxxx' }
  ],
  huawei: [],
  baidu: []
}

const fieldDefs = {
  wxpusher: [
    { key: 'app_token', label: 'APP_TOKEN', secret: true, placeholder: 'WxPusher 应用 appToken' },
    { key: 'uids', label: 'UID 列表', placeholder: '接收者 UID，逗号分隔；与主题至少配一项' },
    { key: 'topic', label: '主题 ID', placeholder: '主题 topicId；与 UID 至少配一项' }
  ],
  serverchan: [
    { key: 'sendkey', label: 'SendKey', secret: true, placeholder: 'Server酱 SendKey，如 SCT****' }
  ],
  wecom: [
    { key: 'webhook', label: 'Webhook', placeholder: '群机器人 https://qyapi.weixin.qq.com/...' }
  ],
  dingtalk: [
    { key: 'webhook', label: 'Webhook', placeholder: '群机器人 https://oapi.dingtalk.com/...' },
    { key: 'secret', label: '加签密钥', secret: true, placeholder: '安全设置选「加签」时填写' }
  ],
  webhook: [
    { key: 'url', label: 'URL', placeholder: '接收 POST {title, body} JSON 的 http(s) 地址' }
  ]
}

const currentFields = computed(() => {
  if (form.type !== 'sms') return fieldDefs[form.type] || []
  const provider = form.config.provider
  if (!provider) return []
  return [...(smsProviderFields[provider] || []), ...smsCommonFields]
})

// 与后端 notify.Build 等价的前端闸口：配置不完整时不允许启用。
// 注意：编辑既有通道时密钥字段回显为 ******（非空），按"已填写"对待。
const formConfigured = computed(() => {
  const c = form.config || {}
  const has = (k) => (c[k] || '').trim() !== ''
  const https = (k) => (c[k] || '').startsWith('https://')
  switch (form.type) {
    case 'wxpusher':
      return has('app_token')
    case 'serverchan':
      return has('sendkey')
    case 'wecom':
      return https('webhook')
    case 'dingtalk':
      return https('webhook')
    case 'webhook':
      return (c.url || '').startsWith('http://') || (c.url || '').startsWith('https://')
    case 'sms': {
      if (!has('provider') || !has('sign_name') || !has('template_code')) return false
      if (c.provider === 'aliyun') return has('access_key_id') && has('access_key_secret')
      if (c.provider === 'tencent') return has('secret_id') && has('secret_key') && has('app_id')
      return true // 预留平台（华为云/百度云）：校验到签名/模板即可
    }
    default:
      return false
  }
})

function onTypeChange() {
  form.config = form.type === 'sms' ? { provider: 'aliyun' } : {}
}

async function load() {
  loading.value = true
  try {
    const resp = await get('/api/notifications/channels')
    items.value = resp.items || []
  } catch (e) {
    showErr(e, '加载失败')
  } finally {
    loading.value = false
  }
}

function resetForm() {
  editingId.value = null
  form.type = 'wxpusher'
  form.name = ''
  form.enabled = true
  form.config = {}
}

function openCreate() {
  resetForm()
  dlg.value = true
}

function openEdit(row) {
  editingId.value = row.id
  form.type = row.type
  form.name = row.name
  form.enabled = row.enabled
  form.config = { ...(row.config || {}) }
  dlg.value = true
}

async function save() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写通道名称')
    return
  }
  if (form.type === 'sms' && !form.config.provider) {
    ElMessage.warning('请选择短信平台')
    return
  }
  saving.value = true
  try {
    const payload = {
      type: form.type,
      name: form.name.trim(),
      enabled: form.enabled,
      config: { ...form.config }
    }
    if (editingId.value) {
      await put(`/api/notifications/channels/${editingId.value}`, payload)
      ElMessage.success('已保存')
    } else {
      await post('/api/notifications/channels', payload)
      ElMessage.success('已创建')
    }
    dlg.value = false
    load()
  } catch (e) {
    showErr(e, '保存失败')
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(row, v) {
  try {
    await put(`/api/notifications/channels/${row.id}`, {
      name: row.name,
      enabled: !!v,
      config: { ...(row.config || {}) }
    })
    row.enabled = !!v
    ElMessage.success(v ? '已启用' : '已停用')
  } catch (e) {
    showErr(e, '操作失败')
  }
}

async function test(row) {
  testingId.value = row.id
  try {
    const updated = await post(`/api/notifications/channels/${row.id}/test`, {})
    row.tested_at = updated.tested_at
    ElMessage.success('测试消息已发送，请查收')
  } catch (e) {
    showErr(e, '测试发送失败')
  } finally {
    testingId.value = null
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除通道「${row.name}」吗？`, '提示', { type: 'warning' })
  } catch {
    return
  }
  try {
    await del(`/api/notifications/channels/${row.id}`)
    ElMessage.success('已删除')
    load()
  } catch (e) {
    showErr(e, '删除失败')
  }
}

onMounted(load)
</script>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.page-head h2 {
  margin: 0;
  font-size: 20px;
}
.muted {
  color: #909399;
}
.hint {
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
}
</style>
