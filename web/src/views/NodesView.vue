<template>
  <div>
    <!-- 工具栏 -->
    <el-card shadow="never" class="toolbar">
      <div class="toolbar-inner">
        <el-radio-group v-model="fNet" size="default" @change="load">
          <el-radio-button value="">全部接入</el-radio-button>
          <el-radio-button value="lan">局域网</el-radio-button>
          <el-radio-button value="wireguard">WireGuard</el-radio-button>
          <el-radio-button value="public">公网</el-radio-button>
          <el-radio-button value="local">本机</el-radio-button>
        </el-radio-group>
        <el-select v-model="fOnline" style="width: 110px" @change="load">
          <el-option label="全部状态" value="" />
          <el-option label="在线" value="1" />
          <el-option label="离线" value="0" />
        </el-select>
        <div class="toolbar-right">
          <el-button @click="load">刷新</el-button>
          <el-button v-if="can('node:write')" type="primary" @click="openAdd">添加节点</el-button>
        </div>
      </div>
    </el-card>

    <!-- 节点表 -->
    <el-card shadow="never" style="margin-top: 12px" v-loading="loading">
      <el-table :data="nodes" style="width: 100%" @row-click="openDetail">
        <el-table-column label="节点" min-width="170">
          <template #default="{ row }">
            <span class="node-dot" :class="row.online ? 'on' : 'off'" />
            <span class="node-name">{{ row.name }}</span>
            <el-tag v-if="row.mode === 'local'" size="small" type="warning" effect="plain">本机</el-tag>
            <el-tag v-if="row.status === 'disabled'" size="small" type="info" effect="plain">已停用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="接入类型" width="120">
          <template #default="{ row }">
            <el-tag size="small" :type="netMeta[row.network_type]?.tag || 'info'">
              {{ netMeta[row.network_type]?.label || '未分类' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="arch" label="架构" width="90" />
        <el-table-column label="地址" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">{{ row.address || '-' }}</template>
        </el-table-column>
        <el-table-column label="Docker" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.docker_version ? 'success' : 'info'" effect="plain">
              {{ row.docker_version ? '已安装' : '无' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最后在线" width="130">
          <template #default="{ row }">{{ fmtAgo(row.last_seen) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click.stop="openDetail(row)">详情</el-button>
            <template v-if="can('node:write') && row.mode !== 'local'">
              <el-button link :type="row.status === 'disabled' ? 'success' : 'warning'"
                @click.stop="toggleStatus(row)">
                {{ row.status === 'disabled' ? '启用' : '停用' }}
              </el-button>
              <el-button link type="danger" @click.stop="remove(row)">删除</el-button>
            </template>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 节点详情抽屉 -->
    <el-drawer v-model="drawer" :title="cur?.name" size="56%" destroy-on-close>
      <div v-if="cur" class="drawer-body">
        <el-descriptions :column="3" border size="small">
          <el-descriptions-item label="主机名">{{ cur.hostname || '-' }}</el-descriptions-item>
          <el-descriptions-item label="系统">{{ (cur.os_name || '-') + ' ' + (cur.os_version || '') }}</el-descriptions-item>
          <el-descriptions-item label="内核">{{ cur.kernel || '-' }}</el-descriptions-item>
          <el-descriptions-item label="架构">{{ cur.arch || '-' }}</el-descriptions-item>
          <el-descriptions-item label="CPU 核数">{{ cur.cpu_cores || '-' }}</el-descriptions-item>
          <el-descriptions-item label="内存总量">{{ fmtBytes(cur.mem_total) }}</el-descriptions-item>
          <el-descriptions-item label="Agent 地址">{{ cur.address || '-' }}</el-descriptions-item>
          <el-descriptions-item label="备用地址">{{ cur.alt_address || '-' }}</el-descriptions-item>
          <el-descriptions-item label="接入类型">
            <el-select v-model="cur.network_type" size="small" style="width: 130px" @change="saveNetworkType">
              <el-option label="局域网" value="lan" />
              <el-option label="WireGuard" value="wireguard" />
              <el-option label="公网" value="public" />
              <el-option label="本机" value="local" />
            </el-select>
          </el-descriptions-item>
          <el-descriptions-item label="心跳">
            <el-tag size="small" :type="cur.online ? 'success' : 'danger'">
              {{ cur.online ? '在线' : '离线' }}
            </el-tag>
            <span class="muted" style="margin-left: 8px">{{ fmtTime(cur.last_seen) }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 实时信息 -->
        <el-card shadow="never" class="section">
          <template #header>
            <div class="card-head">
              <span>实时资源</span>
              <el-button text :loading="liveLoading" @click="loadLive">刷新</el-button>
            </div>
          </template>
          <div v-if="liveErr" class="muted">{{ liveErr }}</div>
          <template v-else-if="live">
            <div class="water-row">
              <span class="water-label">内存</span>
              <el-progress :percentage="pct(live.mem_used, live.mem_total)" />
              <span class="water-text">{{ fmtBytes(live.mem_used) }} / {{ fmtBytes(live.mem_total) }}</span>
            </div>
            <div class="water-row">
              <span class="water-label">磁盘</span>
              <el-progress :percentage="pct(live.disk_used, live.disk_total)" />
              <span class="water-text">{{ fmtBytes(live.disk_used) }} / {{ fmtBytes(live.disk_total) }}</span>
            </div>
            <el-descriptions :column="2" size="small" style="margin-top: 8px">
              <el-descriptions-item label="负载">{{ live.load_avg?.map((x) => x.toFixed(2)).join(' / ') }}</el-descriptions-item>
              <el-descriptions-item label="运行时长">{{ fmtUptime(live.uptime_seconds) }}</el-descriptions-item>
            </el-descriptions>

            <el-table :data="live.interfaces" size="small" style="margin-top: 10px">
              <el-table-column label="接口" width="130">
                <template #default="{ row }">
                  {{ row.name }}
                  <el-tag v-if="row.wireguard" size="small" type="success">WG</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="mac" label="MAC" width="140" />
              <el-table-column label="地址">
                <template #default="{ row }">{{ (row.addresses || []).join(', ') }}</template>
              </el-table-column>
            </el-table>
          </template>
        </el-card>

        <!-- 已装应用 -->
        <el-card shadow="never" class="section">
          <template #header><span>已安装应用</span></template>
          <el-table :data="installations" size="small">
            <el-table-column prop="AppID" label="应用" />
            <el-table-column label="方式" width="90">
              <template #default="{ row }">
                <el-tag size="small" :type="row.Method === 'docker' ? 'success' : 'primary'" effect="plain">
                  {{ row.Method === 'docker' ? '容器' : '直装' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="安装时间" width="170">
              <template #default="{ row }">{{ fmtTime(row.InstalledAt) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="90">
              <template #default="{ row }">
                <el-button link type="primary"
                  @click="goApp(cur.id, row.AppID)">管理</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <!-- 节点操作 -->
        <el-card v-if="can('node:write')" shadow="never" class="section">
          <template #header><span>节点操作</span></template>
          <el-space wrap>
            <el-button @click="rotateToken(cur)">轮换 Agent Token</el-button>
            <el-button v-if="!cur.docker_version && cur.online" type="primary" plain
              :loading="dockerInstalling" @click="installDocker(cur)">
              按需安装 Docker
            </el-button>
          </el-space>
        </el-card>

        <!-- Docker 配置 -->
        <el-card shadow="never" class="section">
          <template #header><span>Docker 镜像加速与仓库</span></template>
          <el-form label-width="100px">
            <el-form-item label="镜像加速">
              <el-input v-model="nodeDocker.mirrors" type="textarea" :rows="2"
                :disabled="!can('node:write')"
                placeholder="留空继承面板默认；每行一个 URL" />
            </el-form-item>
            <el-form-item label="第三方仓库">
              <el-input v-model="nodeDocker.insecure_registries" type="textarea" :rows="2"
                :disabled="!can('node:write')"
                placeholder="每行一个 host[:port]" />
            </el-form-item>
            <el-form-item v-if="can('node:write')">
              <el-button @click="saveNodeDocker">保存配置</el-button>
              <el-button v-if="cur.docker_version" type="primary" plain
                :loading="dockerApplying" @click="applyNodeDocker">立即应用并重载</el-button>
              <span class="hint" style="margin-left:8px">应用会重写 daemon.json 并 reload Docker</span>
            </el-form-item>
          </el-form>
        </el-card>
      </div>
    </el-drawer>

    <!-- 添加节点 -->
    <el-dialog v-model="addVisible" title="添加节点" width="640px" destroy-on-close>
      <el-tabs v-model="addTab">
        <el-tab-pane label="注册命令（推荐）" name="token">
          <el-form label-width="80px">
            <el-form-item label="备注">
              <el-input v-model="tokDesc" placeholder="例如：客厅玩客云" />
            </el-form-item>
            <el-form-item label="有效期">
              <el-select v-model="tokTTL" style="width: 180px">
                <el-option label="24 小时" :value="24" />
                <el-option label="7 天" :value="168" />
                <el-option label="永久" :value="0" />
              </el-select>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" :loading="tokCreating" @click="createToken">生成注册令牌</el-button>
            </el-form-item>
          </el-form>
          <template v-if="installCmd">
            <el-alert type="success" :closable="false" show-icon style="margin-bottom: 10px"
              title="在目标机器执行以下命令即可自动安装 Agent 并加入集群" />
            <el-input v-model="installCmd" type="textarea" :rows="3" readonly />
            <el-button style="margin-top: 8px" @click="copyCmd">复制命令</el-button>
          </template>
        </el-tab-pane>

        <el-tab-pane label="手动录入" name="manual">
          <el-form label-width="92px">
            <el-form-item label="名称">
              <el-input v-model="manual.name" placeholder="节点名称" />
            </el-form-item>
            <el-form-item label="Agent 地址">
              <el-input v-model="manual.address" placeholder="host:port，如 192.168.1.20:8080" />
            </el-form-item>
            <el-form-item label="Agent Token">
              <el-input v-model="manual.token" placeholder="目标节点 Agent 的访问 Token" />
            </el-form-item>
            <el-form-item label="接入类型">
              <el-select v-model="manual.network_type" placeholder="自动识别" clearable style="width: 160px">
                <el-option label="局域网" value="lan" />
                <el-option label="WireGuard" value="wireguard" />
                <el-option label="公网" value="public" />
                <el-option label="本机" value="local" />
              </el-select>
              <el-button text @click="suggest">自动判断</el-button>
              <span class="muted" style="margin-left:8px;font-size:12px">留空则按地址自动识别</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <el-tab-pane label="SSH 添加" name="ssh">
          <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px"
            title="面板将通过 SSH 登录目标机执行 install.sh，Agent 安装后自动注册回面板" />
          <el-form label-width="92px">
            <el-form-item label="主机地址">
              <el-input v-model="ssh.host" placeholder="如 192.168.1.20 或 node.local" />
            </el-form-item>
            <el-form-item label="SSH 端口">
              <el-input-number v-model="ssh.port" :min="1" :max="65535" controls-position="right"
                style="width: 160px" />
            </el-form-item>
            <el-form-item label="用户名">
              <el-input v-model="ssh.user" placeholder="通常为 root" style="width: 220px" />
            </el-form-item>
            <el-form-item label="认证方式">
              <el-radio-group v-model="ssh.auth_mode">
                <el-radio-button value="password">密码</el-radio-button>
                <el-radio-button value="key">私钥</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item v-if="ssh.auth_mode === 'password'" label="密码">
              <el-input v-model="ssh.password" type="password" show-password
                placeholder="SSH 登录密码" />
            </el-form-item>
            <template v-else>
              <el-form-item label="私钥">
                <el-input v-model="ssh.private_key" type="textarea" :rows="5"
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
              </el-form-item>
              <el-form-item label="私钥口令">
                <el-input v-model="ssh.passphrase" type="password" show-password
                  placeholder="私钥未加密时留空" />
              </el-form-item>
            </template>
            <el-form-item label="主机指纹">
              <el-radio-group v-model="ssh.host_key_policy">
                <el-radio-button value="pin">首次确认（pin）</el-radio-button>
                <el-radio-button value="strict">known_hosts（strict）</el-radio-button>
              </el-radio-group>
              <div class="hint">
                <template v-if="ssh.host_key_policy === 'pin'">
                  首次提交会在任务输出中回报目标主机指纹，核对无误后填入下方再次提交：
                </template>
                <template v-else>
                  依据面板数据目录下的 <code>known_hosts</code> 文件严格校验
                </template>
              </div>
              <el-input v-if="ssh.host_key_policy === 'pin'" v-model="ssh.host_key_fingerprint"
                placeholder="SHA256:xxxx（首次提交可留空）" style="margin-top:6px" />
            </el-form-item>
            <el-form-item label="节点名称">
              <el-input v-model="ssh.name" placeholder="留空则使用目标机主机名" style="width: 240px" />
            </el-form-item>
            <el-form-item label="接入类型">
              <el-select v-model="ssh.network_type" placeholder="自动识别" clearable style="width: 160px">
                <el-option label="局域网" value="lan" />
                <el-option label="WireGuard" value="wireguard" />
                <el-option label="公网" value="public" />
                <el-option label="本机" value="local" />
              </el-select>
              <span class="muted" style="margin-left:8px;font-size:12px">留空则按主机地址自动识别</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>
      <template #footer>
        <el-button @click="addVisible = false">关闭</el-button>
        <el-button v-if="addTab === 'manual'" type="primary" @click="submitManual">保存</el-button>
        <el-button v-if="addTab === 'ssh'" type="primary" :loading="sshSubmitting"
          @click="submitSSH">开始安装</el-button>
      </template>
    </el-dialog>

    <TaskProgressDialog v-if="taskId" :task-id="taskId" :endpoint="taskEndpoint"
      :title="taskTitle" @close="taskId = null" @finished="onTaskFinished" />
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del } from '../api/http'
import { session } from '../session'
const can = (p) => session.can(p)
import { fmtTime, fmtAgo, fmtBytes } from '../utils'
import TaskProgressDialog from '../components/TaskProgressDialog.vue'

const router = useRouter()

const nodes = ref([])
const loading = ref(false)
const fNet = ref('')
const fOnline = ref('')

const netMeta = {
  lan: { label: '局域网', tag: 'primary' },
  wireguard: { label: 'WireGuard', tag: 'success' },
  public: { label: '公网', tag: 'warning' },
  local: { label: '本机', tag: 'success' }
}

async function load() {
  loading.value = true
  try {
    const q = new URLSearchParams()
    if (fNet.value) q.set('network_type', fNet.value)
    if (fOnline.value) q.set('online', fOnline.value)
    const d = await get('/api/nodes' + (q.toString() ? '?' + q : ''))
    nodes.value = d.items
  } catch (e) {
    ElMessage.error(e.message || '节点列表加载失败')
  } finally {
    loading.value = false
  }
}
load()

// ---- 详情抽屉 ----
const drawer = ref(false)
const cur = ref(null)
const live = ref(null)
const liveErr = ref('')
const liveLoading = ref(false)
const installations = ref([])

async function openDetail(row) {
  cur.value = await get('/api/nodes/' + row.id)
  nodeDocker.value = {
    mirrors: cur.value.docker_mirrors || '',
    insecure_registries: cur.value.docker_insecure_registries || ''
  }
  drawer.value = true
  live.value = null
  liveErr.value = ''
  loadLive()
  loadInstallations()
}

async function loadLive() {
  if (!cur.value) return
  liveLoading.value = true
  try {
    live.value = await get('/api/nodes/' + cur.value.id + '/info')
  } catch (e) {
    live.value = null
    liveErr.value = e.message
  } finally {
    liveLoading.value = false
  }
}

async function loadInstallations() {
  try {
    const d = await get('/api/nodes/' + cur.value.id + '/apps')
    installations.value = Array.isArray(d.items) ? d.items : []
  } catch {
    installations.value = []
  }
}

function goApp(nodeID, appID) {
  drawer.value = false
  router.push(`/nodes/${nodeID}/apps/${appID}`)
}

function pct(used, total) {
  if (!total) return 0
  return Math.min(100, Math.round((used / total) * 100))
}
function fmtUptime(s) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  return d > 0 ? `${d} 天 ${h} 小时` : `${h} 小时`
}

// ---- 节点操作 ----
async function toggleStatus(row) {
  const next = row.status === 'disabled' ? 'active' : 'disabled'
  await put('/api/nodes/' + row.id, { status: next })
  ElMessage.success('已更新')
  load()
}

async function remove(row) {
  await ElMessageBox.confirm(`确认删除节点「${row.name}」？该操作不影响节点主机上的服务。`, '删除节点', {
    type: 'warning'
  })
  await del('/api/nodes/' + row.id)
  ElMessage.success('已删除')
  load()
}

async function rotateToken(n) {
  await ElMessageBox.confirm('轮换后旧 Token 立即失效，需同步更新 Agent 配置，确认继续？', '轮换 Token', {
    type: 'warning'
  })
  const d = await post('/api/nodes/' + n.id + '/rotate-token', {})
  ElMessage.success('Token 已轮换')
  if (d.token) {
    ElMessageBox.alert(d.token, '新 Agent Token（请妥善保管）')
  }
}

const taskId = ref(null)
const taskEndpoint = ref('/api/tasks/')
const taskTitle = ref('任务执行中')
const dockerInstalling = ref(false)
const dockerApplying = ref(false)
const nodeDocker = ref({ mirrors: '', insecure_registries: '' })
async function installDocker(n) {
  dockerInstalling.value = true
  try {
    const d = await post('/api/nodes/' + n.id + '/docker/install', {})
    taskEndpoint.value = '/api/tasks/'
    taskTitle.value = '任务执行中'
    taskId.value = d.task_id
  } catch (e) {
    ElMessage.error(e.message || 'Docker 安装任务创建失败')
  } finally {
    dockerInstalling.value = false
  }
}
async function saveNodeDocker() {
  if (!cur.value) return
  await put('/api/nodes/' + cur.value.id + '/docker-config', {
    mirrors: nodeDocker.value.mirrors,
    insecure_registries: nodeDocker.value.insecure_registries
  })
  ElMessage.success('节点 Docker 配置已保存')
}
async function applyNodeDocker() {
  if (!cur.value) return
  dockerApplying.value = true
  try {
    const d = await post('/api/nodes/' + cur.value.id + '/docker/apply-config', {})
    ElMessage.success('已入队应用配置，任务 ID: ' + d.task_id)
  } catch (e) {
    ElMessage.error(e.message || '应用 Docker 配置失败')
  } finally {
    dockerApplying.value = false
  }
}
function onTaskFinished() {
  load()
  if (cur.value) openDetail(cur.value)
}

// ---- 添加节点 ----
const addVisible = ref(false)
const addTab = ref('token')
const tokDesc = ref('')
const tokTTL = ref(24)
const tokCreating = ref(false)
const installCmd = ref('')

function openAdd() {
  addVisible.value = true
  addTab.value = 'token'
  installCmd.value = ''
  ssh.value = sshEmpty()
}

async function createToken() {
  tokCreating.value = true
  try {
    const d = await post('/api/registration-tokens', {
      description: tokDesc.value, ttl_hours: tokTTL.value, max_uses: 1
    })
    installCmd.value = d.install_command
  } catch (e) {
    ElMessage.error(e.message || '注册令牌生成失败')
  } finally {
    tokCreating.value = false
  }
}

async function copyCmd() {
  try {
    await navigator.clipboard.writeText(installCmd.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择复制')
  }
}

const manual = ref({ name: '', address: '', token: '', network_type: 'lan' })
async function suggest() {
  if (!manual.value.address) return
  const d = await get('/api/network-suggest?address=' + encodeURIComponent(manual.value.address))
  manual.value.network_type = d.network_type
  ElMessage.success('建议接入类型：' + netMeta[d.network_type]?.label)
}

async function submitManual() {
  if (!manual.value.name || !manual.value.address || !manual.value.token) {
    ElMessage.warning('请填写完整信息')
    return
  }
  await post('/api/nodes/manual', { ...manual.value })
  ElMessage.success('节点已录入')
  addVisible.value = false
  manual.value = { name: '', address: '', token: '', network_type: '' }
  load()
}

// ---- SSH 添加 ----
const sshEmpty = () => ({
  host: '', port: 22, user: 'root', auth_mode: 'password',
  password: '', private_key: '', passphrase: '',
  host_key_policy: 'pin', host_key_fingerprint: '',
  name: '', network_type: ''
})
const ssh = ref(sshEmpty())
const sshSubmitting = ref(false)

async function submitSSH() {
  const v = ssh.value
  if (!v.host || !v.user) {
    ElMessage.warning('请填写 SSH 主机地址与用户名')
    return
  }
  if (v.auth_mode === 'password' && !v.password) {
    ElMessage.warning('请填写 SSH 密码')
    return
  }
  if (v.auth_mode === 'key' && !v.private_key) {
    ElMessage.warning('请粘贴 SSH 私钥')
    return
  }
  sshSubmitting.value = true
  try {
    const d = await post('/api/nodes/ssh-install', { ...v })
    taskEndpoint.value = '/api/nodes/ssh-install?id='
    taskTitle.value = '节点 SSH 安装进度'
    addVisible.value = false
    taskId.value = d.task_id
  } catch (e) {
    // 业务拒绝（如 409 目标主机已纳管）时保留对话框与已填内容，交由用户处置。
    // 该错误信息较长，用可关闭的提示避免被截断。
    ElMessage.error({ message: e.message || 'SSH 纳管任务创建失败', duration: 8000, showClose: true })
  } finally {
    sshSubmitting.value = false
  }
}

async function saveNetworkType() {
  if (!cur.value) return
  try {
    await put('/api/nodes/' + cur.value.id, { network_type: cur.value.network_type })
    ElMessage.success('接入类型已更新')
  } catch (e) {
    ElMessage.error(e.message || '更新失败')
    loadDetail(cur.value.id)
  }
}
</script>

<style scoped>
.toolbar-inner {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.toolbar-right {
  margin-left: auto;
}
.node-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 8px;
}
.node-dot.on {
  background: #67c23a;
  box-shadow: 0 0 6px #95d475;
}
.node-dot.off {
  background: #c0c4cc;
}
.node-name {
  font-weight: 600;
  margin-right: 8px;
}
.drawer-body {
  padding: 0 16px 20px;
}
.section {
  margin-top: 14px;
}
.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.muted {
  color: #909399;
  font-size: 13px;
}
.water-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.water-label {
  width: 36px;
  color: #606266;
  font-size: 13px;
}
.water-text {
  font-size: 12px;
  color: #909399;
  white-space: nowrap;
}
</style>
