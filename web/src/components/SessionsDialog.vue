<template>
  <el-dialog :model-value="modelValue" title="登录设备" width="640px"
    @update:model-value="$emit('update:modelValue', $event)" @open="onOpen">
    <el-alert v-if="revokedCount > 0" type="success" :closable="false" style="margin-bottom: 10px"
      :title="`已注销 ${revokedCount} 个其他设备的登录`" />
    <el-table :data="items" v-loading="loading" stripe size="small">
      <el-table-column label="设备 / 客户端" min-width="200">
        <template #default="{ row }">
          <div class="ua">{{ row.user_agent || '未知客户端' }}</div>
          <el-tag v-if="row.current" type="success" size="small" effect="plain">当前设备</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="ip" label="IP" min-width="120" />
      <el-table-column label="最近活跃" min-width="150">
        <template #default="{ row }">{{ fmtTime(row.last_seen) }}</template>
      </el-table-column>
      <el-table-column label="过期时间" min-width="150">
        <template #default="{ row }">{{ fmtTime(row.expires_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90" align="center">
        <template #default="{ row }">
          <el-button link type="danger" size="small" :loading="busyId === row.id" @click="revoke(row)">
            注销
          </el-button>
        </template>
      </el-table-column>
    </el-table>
    <template #footer>
      <el-button type="danger" plain :loading="revokingOthers" :disabled="!items.length"
        @click="revokeOthers">注销其他所有设备</el-button>
      <el-button @click="$emit('update:modelValue', false)">关闭</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, del, showErr } from '../api/http'
import { fmtTime } from '../utils'

defineProps({ modelValue: { type: Boolean, default: false } })
defineEmits(['update:modelValue'])

const items = ref([])
const loading = ref(false)
const busyId = ref('')
const revokingOthers = ref(false)
const revokedCount = ref(0)

async function onOpen() {
  revokedCount.value = 0
  await load()
}

async function load() {
  loading.value = true
  try {
    const d = await get('/api/auth/sessions')
    items.value = Array.isArray(d.items) ? d.items : []
  } catch (e) {
    showErr(e, '登录设备加载失败')
  } finally {
    loading.value = false
  }
}

async function revoke(row) {
  try {
    await ElMessageBox.confirm(
      row.current ? '这是当前设备的登录，注销后需要重新登录。确定继续吗？' : '确定注销该设备的登录吗？',
      '提示', { type: 'warning' })
  } catch {
    return
  }
  busyId.value = row.id
  try {
    await del(`/api/auth/sessions/${encodeURIComponent(row.id)}`)
    ElMessage.success('已注销')
    await load()
  } catch (e) {
    showErr(e, '注销失败')
  } finally {
    busyId.value = ''
  }
}

async function revokeOthers() {
  try {
    await ElMessageBox.confirm('将注销除当前设备外的全部登录，确定继续吗？', '提示', { type: 'warning' })
  } catch {
    return
  }
  revokingOthers.value = true
  try {
    const d = await post('/api/auth/sessions/revoke-others', {})
    await load()
    revokedCount.value = d.revoked || 0
    ElMessage.success(revokedCount.value > 0 ? `已注销 ${revokedCount.value} 个设备` : '没有其他设备在线')
  } catch (e) {
    showErr(e, '注销失败')
  } finally {
    revokingOthers.value = false
  }
}
</script>

<style scoped>
.ua {
  font-size: 12px;
  color: #606266;
  word-break: break-all;
  line-height: 1.4;
}
</style>
