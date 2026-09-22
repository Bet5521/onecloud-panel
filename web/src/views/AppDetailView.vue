<template>
  <div v-loading="loading">
    <!-- 顶部信息 -->
    <el-page-header @back="$router.push('/apps')" class="back">
      <template #content>
        <span class="title">{{ recipe?.Name || appId }}</span>
        <el-tag size="small" type="info" effect="plain" style="margin-left: 10px">
          {{ node?.name }}
        </el-tag>
      </template>
      <template #extra>
        <el-space v-if="can('app:write')">
          <el-button size="small" @click="action('start')">启动</el-button>
          <el-button size="small" @click="action('stop')">停止</el-button>
          <el-button size="small" type="primary" plain @click="action('restart')">重启</el-button>
          <el-button size="small" type="danger" plain @click="uninstallVisible = true">卸载</el-button>
        </el-space>
      </template>
    </el-page-header>

    <!-- 状态卡 -->
    <el-card shadow="never" style="margin-top: 12px">
      <el-descriptions :column="3" border size="small">
        <el-descriptions-item label="运行状态">
          <el-tag size="small" :type="isRunning ? 'success' : 'info'">
            {{ isRunning ? '运行中' : (status?.state || '已停止') }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="健康检查">
          <template v-if="status && status.healthy !== undefined">
            <el-tag size="small" :type="status.healthy ? 'success' : 'danger'">
              {{ status.healthy ? '健康' : '异常' }}
            </el-tag>
          </template>
          <span v-else class="muted">未配置</span>
        </el-descriptions-item>
        <el-descriptions-item label="安装方式">
          <el-tag size="small" :type="status?.method === 'docker' ? 'success' : 'primary'" effect="plain">
            {{ status?.method === 'docker' ? '容器' : '直装' }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item v-if="status?.container_name" label="容器名">
          {{ status.container_name }}
        </el-descriptions-item>
        <el-descriptions-item v-if="status?.service_name" label="系统服务">
          {{ status.service_name }}
        </el-descriptions-item>
        <el-descriptions-item label="访问入口">
          <el-space wrap>
            <el-link v-for="p in accessPorts" :key="p.Port" type="primary"
              :href="p.url" target="_blank">
              :{{ p.Port }}<span v-if="p.Description">（{{ p.Description }}）</span>
            </el-link>
            <span v-if="!accessPorts.length" class="muted">-</span>
          </el-space>
        </el-descriptions-item>
      </el-descriptions>

      <!-- WireGuard Peer 表 -->
      <template v-if="wg">
        <el-divider content-position="left">
          WireGuard Peers（接口 {{ wg.interface }} · 监听 {{ wg.listen_port }}）
        </el-divider>
        <el-alert v-if="wg.error" type="error" :title="wg.error" :closable="false" />
        <el-table v-else :data="wg.peers" size="small">
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag size="small" :type="row.online ? 'success' : 'info'">
                {{ row.online ? '在线' : '离线' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="公钥" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">{{ row.public_key }}</template>
          </el-table-column>
          <el-table-column label="Endpoint" width="180">
            <template #default="{ row }">{{ row.endpoint || '-' }}</template>
          </el-table-column>
          <el-table-column label="AllowedIPs" min-width="150">
            <template #default="{ row }">{{ (row.allowed_ips || []).join(', ') || '-' }}</template>
          </el-table-column>
          <el-table-column label="最近握手" width="150">
            <template #default="{ row }">
              {{ row.latest_handshake ? fmtAgo(row.latest_handshake) : '从未' }}
            </template>
          </el-table-column>
          <el-table-column label="接收" width="90">
            <template #default="{ row }">{{ fmtBytes(row.rx_bytes) }}</template>
          </el-table-column>
          <el-table-column label="发送" width="90">
            <template #default="{ row }">{{ fmtBytes(row.tx_bytes) }}</template>
          </el-table-column>
        </el-table>
      </template>
    </el-card>

    <!-- 日志 / 配置 -->
    <el-card shadow="never" style="margin-top: 12px">
      <el-tabs v-model="tab" @tab-change="onDetailTabChange">
        <el-tab-pane label="运行日志" name="journal">
          <div class="journal-tools">
            <el-select v-model="lines" size="small" style="width: 120px" @change="loadJournal">
              <el-option :value="100" label="最近 100 行" />
              <el-option :value="300" label="最近 300 行" />
              <el-option :value="1000" label="最近 1000 行" />
            </el-select>
            <el-switch v-model="autoFollow" active-text="自动刷新" @change="toggleAuto" />
            <el-button size="small" @click="loadJournal">刷新</el-button>
          </div>
          <pre class="journal">{{ journal || '暂无日志内容' }}</pre>
        </el-tab-pane>

        <el-tab-pane label="配置文件" name="config">
          <div v-if="!configFiles.length" class="muted">该应用未开放配置编辑</div>
          <template v-else>
            <div class="journal-tools">
              <el-select v-model="configPath" style="width: 340px" @change="loadConfig">
                <el-option v-for="f in configFiles" :key="f.Path"
                  :label="f.Path + (f.Optional ? '（可选）' : '')" :value="f.Path" />
              </el-select>
              <el-button @click="loadConfig" :disabled="!configPath">读取</el-button>
              <el-button type="primary" :loading="savingConfig"
                :disabled="!configPath" @click="saveConfig">
                保存
              </el-button>
            </div>
            <el-input v-model="configContent" type="textarea" :rows="18"
              class="config-editor" />
          </template>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 卸载对话框 -->
    <el-dialog v-model="uninstallVisible" title="卸载应用" width="460px">
      <el-alert type="warning" :closable="false" show-icon
        title="卸载将停止并移除该应用的运行单元" style="margin-bottom: 12px" />
      <el-checkbox v-model="purgeData">同时清除数据目录（不可恢复）</el-checkbox>
      <template #footer>
        <el-button @click="uninstallVisible = false">取消</el-button>
        <el-button type="danger" @click="submitUninstall">确认卸载</el-button>
      </template>
    </el-dialog>

    <TaskProgressDialog v-if="taskId" :task-id="taskId"
      @close="taskId = null" @finished="onTaskFinished" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { get, post, put, showErr } from '../api/http'
import { session } from '../session'
const can = (p) => session.can(p)
import { fmtAgo, fmtBytes } from '../utils'
import TaskProgressDialog from '../components/TaskProgressDialog.vue'

const route = useRoute()
const nodeId = route.params.nodeId
const appId = route.params.appId

const loading = ref(true)
const node = ref(null)
const recipe = ref(null)
const status = ref(null)

const isRunning = computed(() =>
  status.value ? status.value.active === true || status.value.running === true : false)

const wg = computed(() => status.value?.wireguard || null)

// 访问入口：仅对 TCP/HTTP 端口生成链接，主机用节点地址；本机节点用当前访问主机
const accessPorts = computed(() => {
  if (!recipe.value) return []
  let host = node.value?.address?.split(':')[0]
  if (node.value?.mode === 'local' || !host) host = location.hostname
  return (recipe.value.Ports || [])
    .filter((p) => p.Proto !== 'udp')
    .map((p) => ({ ...p, url: `http://${host}:${p.Port}` }))
})

async function loadAll() {
  loading.value = true
  try {
    ;[node.value, recipe.value, status.value] = await Promise.all([
      get('/api/nodes/' + nodeId),
      get('/api/recipes/' + appId),
      get(`/api/nodes/${nodeId}/apps/${appId}/status`)
    ])
  } catch (e) {
    showErr(e, '加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(loadAll)

// ---- 服务动作 ----
async function action(act) {
  try {
    await post(`/api/nodes/${nodeId}/apps/${appId}/${act}`)
    ElMessage.success(act === 'start' ? '已启动' : act === 'stop' ? '已停止' : '已重启')
  } catch (e) {
    showErr(e, '操作失败')
  }
  refreshStatus()
}
async function refreshStatus() {
  try {
    status.value = await get(`/api/nodes/${nodeId}/apps/${appId}/status`)
  } catch { /* ignore */ }
}

// ---- 日志 ----
const tab = ref('journal')
const journal = ref('')
const lines = ref(300)
const autoFollow = ref(false)
let timer = null

async function loadJournal() {
  try {
    journal.value = await get(`/api/nodes/${nodeId}/apps/${appId}/journal?lines=${lines.value}`)
  } catch (e) {
    journal.value = '日志读取失败：' + e.message
  }
}
loadJournal()

function toggleAuto(on) {
  clearInterval(timer)
  if (on) timer = setInterval(loadJournal, 3000)
}
onUnmounted(() => clearInterval(timer))

// ---- 配置 ----
const configFiles = computed(() => recipe.value?.ConfigFiles || [])
const configPath = ref('')
const configContent = ref('')
const savingConfig = ref(false)

function onDetailTabChange(name) {
  if (name === 'config') initConfig()
  if (name === 'journal') loadJournal()
}

function initConfig() {
  if (!configPath.value && configFiles.value.length) {
    configPath.value = configFiles.value[0].Path
    loadConfig()
  }
}

async function loadConfig() {
  if (!configPath.value) return
  try {
    const d = await get(`/api/nodes/${nodeId}/apps/${appId}/config?path=` +
      encodeURIComponent(configPath.value))
    configContent.value = d.content || ''
  } catch (e) {
    configContent.value = ''
    ElMessage.error(e.message)
  }
}

async function saveConfig() {
  savingConfig.value = true
  try {
    const d = await put(`/api/nodes/${nodeId}/apps/${appId}/config`, {
      path: configPath.value, content: configContent.value
    })
    ElMessage.success(d.hint || '配置已保存')
  } catch (e) {
    showErr(e, '配置保存失败')
  } finally {
    savingConfig.value = false
  }
}

// ---- 卸载 ----
const uninstallVisible = ref(false)
const purgeData = ref(false)
const taskId = ref(null)

async function submitUninstall() {
  try {
    const d = await post(`/api/nodes/${nodeId}/apps/${appId}/uninstall`, {
      purge_data: purgeData.value
    })
    // 失败时保留对话框，便于直接重试
    uninstallVisible.value = false
    taskId.value = d.task_id
  } catch (e) {
    showErr(e, '卸载任务创建失败')
  }
}
function onTaskFinished() {
  // 卸载完成后回到应用页
  window.location.assign('/apps')
}
</script>

<style scoped>
.back {
  margin-top: 4px;
}
.title {
  font-weight: 700;
  font-size: 16px;
}
.muted {
  color: #909399;
  font-size: 13px;
}
.journal-tools {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}
.journal {
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
  max-height: 480px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
.config-editor :deep(textarea) {
  font-family: Consolas, 'Courier New', monospace;
  font-size: 13px;
  line-height: 1.6;
}
</style>
