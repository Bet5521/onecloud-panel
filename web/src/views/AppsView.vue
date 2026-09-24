<template>
  <el-card shadow="never">
    <el-tabs v-model="tab" @tab-change="onTabChange">
      <el-tab-pane label="应用目录" name="catalog">
        <div class="filter-bar">
          <el-input v-model="kw" placeholder="搜索应用名称/描述" clearable style="width: 240px" />
          <el-select v-model="cat" placeholder="全部分类" clearable style="width: 160px">
            <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
          </el-select>
        </div>

        <el-row :gutter="12" v-loading="loading">
          <el-col :xs="24" :sm="12" :md="8" :lg="6" v-for="r in filtered" :key="r.id">
            <el-card shadow="hover" class="app-card" @click="openInstall(r)">
              <div class="app-head">
                <span class="app-name">{{ r.Name }}</span>
                <el-tag size="small" type="info" effect="plain">{{ r.Category }}</el-tag>
              </div>
              <p class="app-desc">{{ r.Description || '暂无描述' }}</p>
              <div class="app-meta">
                <el-tag v-for="m in r.Methods" :key="m" size="small"
                  :type="m === 'docker' ? 'success' : 'primary'" effect="plain" class="method-tag">
                  {{ m === 'docker' ? '容器' : '直装' }}
                </el-tag>
                <span v-if="r.Ports?.length" class="port-hint">
                  端口 {{ r.Ports.map((p) => p.Port).join(', ') }}
                </span>
              </div>
            </el-card>
          </el-col>
        </el-row>
      </el-tab-pane>

      <el-tab-pane label="自定义应用" name="custom">
        <div class="filter-bar" v-if="can('app:read')">
          <el-button v-if="isAdmin" type="primary" @click="openCreateCustom">新建自定义应用</el-button>
          <span class="muted">支持连接 GitHub 源码、Docker 容器、或直接上传二进制程序部署到节点。</span>
        </div>

        <el-row :gutter="12" v-loading="customLoading" v-if="can('app:read')">
          <el-col :xs="24" :sm="12" :md="8" :lg="6" v-for="c in customApps" :key="c.id">
            <el-card shadow="hover" class="app-card">
              <div class="app-head">
                <span class="app-name">{{ c.name }}</span>
                <el-tag size="small" :type="typeTag(c.type)" effect="plain">{{ typeLabel(c.type) }}</el-tag>
              </div>
              <p class="app-desc">{{ c.description || '暂无描述' }}</p>
              <div class="app-meta">
                <el-tag v-if="c.type === 'binary' && !c.has_binary" size="small" type="warning" effect="plain">
                  未上传二进制
                </el-tag>
                <el-tag v-if="c.owner_user_id == null" size="small" type="info" effect="plain">系统级</el-tag>
              </div>
              <div class="app-actions">
                <el-button size="small" type="primary" :disabled="!can('app:write') || (c.type === 'binary' && !c.has_binary)"
                  @click="installCustom(c)">安装</el-button>
                <el-button size="small" :disabled="!isAdmin" @click="openEditCustom(c)">编辑</el-button>
                <el-button v-if="c.type === 'binary' && isAdmin" size="small" @click="pickUpload(c)">上传</el-button>
                <el-button size="small" type="danger" plain :disabled="!isAdmin" @click="removeCustom(c)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col v-if="!customApps.length && !customLoading" :span="24">
            <el-empty description="暂无自定义应用" />
          </el-col>
        </el-row>
        <el-empty v-if="!can('app:read')" description="无查看权限" />
      </el-tab-pane>

      <el-tab-pane label="已安装" name="installed">
        <el-table :data="installedRows" v-loading="instLoading" size="default">
          <el-table-column label="节点" min-width="140">
            <template #default="{ row }">{{ nodeName(row.NodeID) }}</template>
          </el-table-column>
          <el-table-column prop="AppID" label="应用" min-width="140" />
          <el-table-column label="方式" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.Method === 'docker' ? 'success' : 'primary'" effect="plain">
                {{ row.Method === 'docker' ? '容器' : '直装' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="安装时间" width="180">
            <template #default="{ row }">{{ fmtTime(row.InstalledAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button link type="primary"
                @click="$router.push(`/nodes/${row.NodeID}/apps/${row.AppID}`)">管理</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>
  </el-card>

  <!-- 安装对话框 -->
  <el-dialog v-model="installVisible" :title="`安装 ${recipe?.name || ''}`" width="620px" destroy-on-close>
    <el-form label-width="110px">
      <el-form-item label="目标节点" required>
        <el-select v-model="form.node_id" style="width: 100%" placeholder="选择节点">
          <el-option v-for="n in nodeOptions" :key="n.id"
            :label="n.name + (n.mode === 'local' ? '（本机）' : '')" :value="n.id" />
        </el-select>
      </el-form-item>

      <el-form-item label="安装方式" required>
        <el-radio-group v-model="form.method">
          <el-radio v-for="m in availableMethods" :key="m.method" :value="m.method">
            {{ m.method === 'docker' ? '容器方式' : '直接安装' }}
          </el-radio>
        </el-radio-group>
        <div v-if="!availableMethods.length" class="muted">
          该节点架构不支持此应用的任何安装方式
        </div>
      </el-form-item>

      <template v-if="recipe?.variables?.length">
        <el-divider content-position="left">应用参数</el-divider>
        <el-form-item v-for="v in recipe.variables" :key="v.key" :label="v.name || v.key"
          :required="v.required">
          <el-select v-if="v.type === 'select'" v-model="form.vars[v.key]" style="width: 100%">
            <el-option v-for="o in v.options" :key="o" :label="o" :value="o" />
          </el-select>
          <el-switch v-else-if="v.type === 'bool'" v-model="boolVars[v.key]" />
          <el-input-number v-else-if="v.type === 'int'" v-model="numVars[v.key]" :min="0" />
          <el-input v-else v-model="form.vars[v.key]" :placeholder="v.description || ''" />
        </el-form-item>
      </template>
    </el-form>
    <template #footer>
      <el-button @click="installVisible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">开始安装</el-button>
    </template>
  </el-dialog>

  <!-- 自定义应用新建/编辑向导 -->
  <el-dialog v-model="customDlg" :title="editingId ? '编辑自定义应用' : '新建自定义应用'" width="660px"
    destroy-on-close @closed="resetCustomForm">
    <el-form ref="customRef" :model="customForm" label-width="120px">
      <el-form-item label="类型" required>
        <el-radio-group v-model="customForm.type" :disabled="!!editingId">
          <el-radio value="docker">Docker 容器</el-radio>
          <el-radio value="github">GitHub 源码</el-radio>
          <el-radio value="binary">二进制程序</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="名称" required>
        <el-input v-model="customForm.name" maxlength="64" placeholder="应用显示名称" />
      </el-form-item>
      <el-form-item label="分类">
        <el-input v-model="customForm.category" placeholder="如 工具 / 媒体 / 网络" />
      </el-form-item>
      <el-form-item label="图标">
        <el-input v-model="customForm.icon" placeholder="emoji 或图标字符，如 🐳" />
      </el-form-item>
      <el-form-item label="描述">
        <el-input v-model="customForm.description" type="textarea" :rows="2" />
      </el-form-item>
      <el-form-item label="主页">
        <el-input v-model="customForm.homepage" placeholder="https://..." />
      </el-form-item>

      <!-- Docker -->
      <template v-if="customForm.type === 'docker'">
        <el-divider content-position="left">容器配置</el-divider>
        <el-form-item label="镜像" required>
          <el-input v-model="customForm.image" placeholder="如 nginx:latest" />
        </el-form-item>
        <el-form-item label="端口映射">
          <el-input v-model="customForm.ports" type="textarea" :rows="2"
            placeholder="每行或逗号分隔：8080:80/tcp" />
        </el-form-item>
        <el-form-item label="环境变量">
          <el-input v-model="customForm.env" type="textarea" :rows="2" placeholder="TZ=Asia/Shanghai" />
        </el-form-item>
        <el-form-item label="数据卷">
          <el-input v-model="customForm.volumes" type="textarea" :rows="2" placeholder="/data/app:/data" />
        </el-form-item>
        <el-form-item label="重启策略">
          <el-select v-model="customForm.restart" style="width: 100%">
            <el-option label="unless-stopped" value="unless-stopped" />
            <el-option label="always" value="always" />
            <el-option label="on-failure" value="on-failure" />
            <el-option label="no" value="no" />
          </el-select>
        </el-form-item>
        <el-form-item label="网络模式">
          <el-input v-model="customForm.network" placeholder="bridge / host" />
        </el-form-item>
        <el-form-item label="特权模式">
          <el-switch v-model="customForm.privileged" />
        </el-form-item>
      </template>

      <!-- GitHub -->
      <template v-if="customForm.type === 'github'">
        <el-divider content-position="left">源码配置</el-divider>
        <el-form-item label="仓库地址" required>
          <el-input v-model="customForm.repo" placeholder="https://github.com/user/repo" />
        </el-form-item>
        <el-form-item label="分支">
          <el-input v-model="customForm.branch" placeholder="默认 main" />
        </el-form-item>
        <el-form-item label="运行命令">
          <el-input v-model="customForm.run" placeholder="默认 docker compose up -d" />
          <span class="hint">克隆后在该目录执行的启动命令（支持 gh-proxy 加速克隆）。</span>
        </el-form-item>
      </template>

      <!-- Binary -->
      <template v-if="customForm.type === 'binary'">
        <el-divider content-position="left">二进制配置</el-divider>
        <el-form-item label="可执行文件名" required>
          <el-input v-model="customForm.exec_name" placeholder="如 myapp（上传文件将以此名落盘）" />
        </el-form-item>
        <el-form-item label="启动参数">
          <el-input v-model="customForm.args" placeholder="如 -p 8080 --debug" />
        </el-form-item>
        <el-form-item label="工作目录">
          <el-input v-model="customForm.workdir" placeholder="默认 /opt/onecloud-apps/<id>" />
        </el-form-item>
        <el-form-item label="运行用户">
          <el-input v-model="customForm.user" placeholder="默认 root" />
        </el-form-item>
        <el-form-item label="二进制文件">
          <input type="file" @change="onWizardFile" />
          <span v-if="editingId && editingHasBinary" class="hint">已上传，重新选择可覆盖。</span>
          <span v-else-if="binaryFile" class="hint">已选择：{{ binaryFile.name }}</span>
        </el-form-item>
      </template>

      <!-- 健康检查（通用） -->
      <el-divider content-position="left">健康检查（可选）</el-divider>
      <el-form-item label="类型">
        <el-select v-model="customForm.healthType" style="width: 100%" clearable>
          <el-option label="无" value="" />
          <el-option label="TCP" value="tcp" />
          <el-option label="HTTP" value="http" />
          <el-option label="命令" value="command" />
        </el-select>
      </el-form-item>
      <el-form-item v-if="customForm.healthType === 'tcp' || customForm.healthType === 'http'" label="端口">
        <el-input-number v-model="customForm.healthPort" :min="1" :max="65535" />
      </el-form-item>
      <el-form-item v-if="customForm.healthType === 'http'" label="路径">
        <el-input v-model="customForm.healthPath" placeholder="如 /healthz" />
      </el-form-item>
      <el-form-item v-if="customForm.healthType === 'command'" label="命令">
        <el-input v-model="customForm.healthCmd" placeholder="如 curl -f http://localhost:8080" />
      </el-form-item>

      <el-form-item v-if="isAdmin" label="系统级">
        <el-switch v-model="customForm.system" />
        <span class="hint">管理员勾选后该应用对所有用户可见（owner 为空）。</span>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="customDlg = false">取消</el-button>
      <el-button type="primary" :loading="customSaving" @click="submitCustom">保存</el-button>
    </template>
  </el-dialog>

  <input ref="uploadInput" type="file" hidden @change="onUploadChange" />

  <TaskProgressDialog v-if="taskId" :task-id="taskId"
    @close="taskId = null" @finished="onFinished" />
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, showErr } from '../api/http'
import { session } from '../session'
const can = (p) => session.can(p)
import { fmtTime } from '../utils'
import TaskProgressDialog from '../components/TaskProgressDialog.vue'

const tab = ref('catalog')
const recipes = ref([])
const nodes = ref([])
const loading = ref(false)
const kw = ref('')
const cat = ref('')

const categories = computed(() => [...new Set(recipes.value.map((r) => r.Category).filter(Boolean))])
const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  return recipes.value.filter((r) => {
    if (cat.value && r.Category !== cat.value) return false
    if (!k) return true
    return (r.Name + ' ' + (r.Description || '') + ' ' + r.ID).toLowerCase().includes(k)
  })
})

async function load() {
  loading.value = true
  try {
    const [rs, ns] = [await get('/api/recipes'), await get('/api/nodes')]
    recipes.value = rs.items || rs
    nodes.value = ns.items
  } catch (e) {
    showErr(e, '加载失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function nodeName(id) {
  return nodes.value.find((n) => n.id === id)?.name || '#' + id
}

// ---- 已安装聚合 ----
const installedRows = ref([])
const instLoading = ref(false)
function onTabChange(name) {
  if (name === 'installed') loadInstalled()
  if (name === 'custom') loadCustomApps()
}

async function loadInstalled() {
  instLoading.value = true
  try {
    const rows = []
    for (const n of nodes.value) {
      const d = await get('/api/nodes/' + n.id + '/apps')
      for (const it of d.items || []) rows.push(it)
    }
    installedRows.value = rows
  } catch (e) {
    showErr(e, '已安装应用加载失败')
  } finally {
    instLoading.value = false
  }
}

// ---- 安装对话框 ----
const installVisible = ref(false)
const submitting = ref(false)
const recipe = ref(null)
const form = reactive({ node_id: null, method: 'native', vars: {} })
const boolVars = reactive({})
const numVars = reactive({})

const nodeOptions = computed(() => nodes.value.filter((n) => n.status !== 'disabled' && n.online))

const selectedNode = computed(() => nodes.value.find((n) => n.id === form.node_id))
const availableMethods = computed(() => {
  if (!recipe.value || !selectedNode.value) return []
  const arch = selectedNode.value.arch
  // 与后端 Compatibility/Supports 完全一致的判定
  const specOK = (spec) => spec && (!spec.Arches || !spec.Arches.length || spec.Arches.includes(arch))
  return (recipe.value.Methods || [])
    .map((method) => ({
      method,
      ok: method === 'docker' ? specOK(recipe.value.Docker) : specOK(recipe.value.Native)
    }))
    .filter((m) => m.ok)
})

// 切换节点时，安装方式自动落到首个兼容方式
watch(() => form.node_id, () => {
  if (!availableMethods.value.some((m) => m.method === form.method)) {
    form.method = availableMethods.value[0]?.method || 'native'
  }
})

function openInstall(r) {
  if (!can('app:write')) {
    ElMessage.warning('当前角色无应用安装权限')
    return
  }
  recipe.value = r
  form.node_id = nodeOptions.value[0]?.id ?? null
  form.vars = {}
  for (const k of Object.keys(boolVars)) delete boolVars[k]
  for (const k of Object.keys(numVars)) delete numVars[k]
  for (const v of r.Variables || []) {
    if (v.Type === 'bool') {
      boolVars[v.Key] = v.Default === 'true'
    } else if (v.Type === 'int') {
      numVars[v.Key] = v.Default !== '' ? Number(v.Default) : undefined
    } else {
      form.vars[v.Key] = v.Default != null ? String(v.Default) : ''
    }
  }
  // 默认方式随选中节点的兼容性决定（节点变化时 watch 同步）
  form.method = availableMethods.value[0]?.method || 'native'
  installVisible.value = true
}

async function submit() {
  if (!form.node_id) {
    ElMessage.warning('请选择目标节点')
    return
  }
  if (!availableMethods.value.some((m) => m.method === form.method)) {
    ElMessage.warning('该方式不兼容目标节点')
    return
  }
  const vars = { ...form.vars }
  for (const v of recipe.value.Variables || []) {
    if (v.Type === 'bool') vars[v.Key] = boolVars[v.Key] ? 'true' : 'false'
    if (v.Type === 'int') vars[v.Key] = numVars[v.Key] != null ? String(numVars[v.Key]) : ''
    if (v.Required && !vars[v.Key]) {
      ElMessage.warning(`参数「${v.Name || v.Key}」为必填项`)
      return
    }
  }
  submitting.value = true
  try {
    const d = await post(`/api/nodes/${form.node_id}/apps/${recipe.value.ID}/install`, {
      method: form.method, vars
    })
    installVisible.value = false
    taskId.value = d.task_id
  } catch (e) {
    showErr(e, '安装任务创建失败')
  } finally {
    submitting.value = false
  }
}

const taskId = ref(null)
function onFinished() {
  tab.value = 'installed'
  loadInstalled()
}

// ---- 自定义应用 ----
const customApps = ref([])
const customLoading = ref(false)
const customDlg = ref(false)
const editingId = ref(null)
const editingHasBinary = ref(false)
const customSaving = ref(false)
const customRef = ref()
const binaryFile = ref(null)
const uploadInput = ref(null)
const uploadTarget = ref(null)

const isAdmin = computed(() => session.user?.role_code === 'admin')
const customTypeOptions = { docker: 'Docker 容器', github: 'GitHub 源码', binary: '二进制程序' }
const typeLabel = (t) => customTypeOptions[t] || t
const typeTag = (t) => (t === 'docker' ? 'success' : t === 'github' ? 'warning' : 'primary')

const customForm = reactive({
  type: 'docker',
  name: '', category: '', icon: '', description: '', homepage: '', system: false,
  image: '', ports: '', env: '', volumes: '', restart: 'unless-stopped', privileged: false, network: 'bridge',
  repo: '', branch: '', run: '',
  exec_name: '', args: '', workdir: '', user: '',
  healthType: '', healthPort: null, healthPath: '', healthCmd: ''
})

function resetCustomForm() {
  editingId.value = null
  editingHasBinary.value = false
  binaryFile.value = null
  Object.assign(customForm, {
    type: 'docker', name: '', category: '', icon: '', description: '', homepage: '', system: false,
    image: '', ports: '', env: '', volumes: '', restart: 'unless-stopped', privileged: false, network: 'bridge',
    repo: '', branch: '', run: '', exec_name: '', args: '', workdir: '', user: '',
    healthType: '', healthPort: null, healthPath: '', healthCmd: ''
  })
}

function toArr(s) {
  return String(s || '').split(/[\n,]/).map((x) => x.trim()).filter(Boolean)
}

async function loadCustomApps() {
  if (!can('app:read')) return
  customLoading.value = true
  try {
    const d = await get('/api/custom-apps')
    customApps.value = d.items || []
  } catch (e) {
    showErr(e, '加载自定义应用失败')
  } finally {
    customLoading.value = false
  }
}

function openCreateCustom() {
  resetCustomForm()
  customDlg.value = true
}

function openEditCustom(c) {
  resetCustomForm()
  editingId.value = c.id
  editingHasBinary.value = !!c.has_binary
  customForm.type = c.type
  customForm.name = c.name
  customForm.category = c.category || ''
  customForm.icon = c.icon || ''
  customForm.description = c.description || ''
  customForm.homepage = c.homepage || ''
  const cfg = c.config || {}
  if (c.type === 'docker') {
    customForm.image = cfg.image || ''
    customForm.ports = (cfg.ports || []).join('\n')
    customForm.env = (cfg.env || []).join('\n')
    customForm.volumes = (cfg.volumes || []).join('\n')
    customForm.restart = cfg.restart || 'unless-stopped'
    customForm.privileged = !!cfg.privileged
    customForm.network = cfg.network || 'bridge'
  } else if (c.type === 'github') {
    customForm.repo = cfg.repo || ''
    customForm.branch = cfg.branch || ''
    customForm.run = cfg.run || ''
  } else if (c.type === 'binary') {
    customForm.exec_name = cfg.exec_name || ''
    customForm.args = cfg.args || ''
    customForm.workdir = cfg.workdir || ''
    customForm.user = cfg.user || ''
  }
  const h = cfg.health || {}
  customForm.healthType = h.type || ''
  customForm.healthPort = h.port || null
  customForm.healthPath = h.path || ''
  customForm.healthCmd = (h.cmd || []).join(' ')
  customDlg.value = true
}

function buildHealth() {
  const t = customForm.healthType
  if (!t) return {}
  if (t === 'command') {
    const cmd = toArr(customForm.healthCmd)
    if (!cmd.length) return {}
    return { type: 'command', cmd }
  }
  if ((t === 'http' || t === 'tcp') && customForm.healthPort) {
    const h = { type: t, port: Number(customForm.healthPort) }
    if (t === 'http') h.path = customForm.healthPath || ''
    return h
  }
  return {}
}

function buildConfig() {
  const t = customForm.type
  if (t === 'docker') {
    return {
      image: customForm.image.trim(),
      ports: toArr(customForm.ports),
      env: toArr(customForm.env),
      volumes: toArr(customForm.volumes),
      restart: customForm.restart || 'unless-stopped',
      privileged: !!customForm.privileged,
      network: customForm.network || 'bridge',
      health: buildHealth()
    }
  }
  if (t === 'github') {
    return {
      repo: customForm.repo.trim(),
      branch: customForm.branch.trim(),
      run: customForm.run.trim(),
      health: buildHealth()
    }
  }
  return {
    exec_name: customForm.exec_name.trim(),
    args: customForm.args,
    workdir: customForm.workdir.trim(),
    user: customForm.user.trim(),
    health: buildHealth()
  }
}

function onWizardFile(e) {
  binaryFile.value = e.target.files?.[0] || null
  e.target.value = ''
}

async function submitCustom() {
  if (!customForm.name.trim()) {
    ElMessage.warning('请填写应用名称')
    return
  }
  if (customForm.type === 'docker' && !customForm.image.trim()) {
    ElMessage.warning('请填写镜像')
    return
  }
  if (customForm.type === 'github' && !customForm.repo.trim()) {
    ElMessage.warning('请填写仓库地址')
    return
  }
  if (customForm.type === 'binary' && !customForm.exec_name.trim()) {
    ElMessage.warning('请填写可执行文件名')
    return
  }
  customSaving.value = true
  try {
    const payload = {
      type: customForm.type,
      name: customForm.name.trim(),
      category: customForm.category,
      icon: customForm.icon,
      description: customForm.description,
      homepage: customForm.homepage,
      config: buildConfig()
    }
    if (isAdmin.value && customForm.system) payload.system = true
    let id
    if (editingId.value) {
      await put('/api/custom-apps/' + editingId.value, payload)
      id = editingId.value
      ElMessage.success('已保存')
    } else {
      const d = await post('/api/custom-apps', payload)
      id = d.id
      ElMessage.success('已创建')
    }
    if (customForm.type === 'binary' && binaryFile.value) {
      await uploadBinary(id, binaryFile.value)
      ElMessage.success('二进制已上传')
    }
    customDlg.value = false
    loadCustomApps()
  } catch (e) {
    showErr(e, '保存失败')
  } finally {
    customSaving.value = false
  }
}

async function uploadBinary(id, file) {
  const fd = new FormData()
  fd.append('file', file)
  const resp = await fetch(`/api/custom-apps/${id}/binary`, {
    method: 'POST', body: fd, credentials: 'same-origin'
  })
  if (!resp.ok) {
    let msg = '上传失败'
    try {
      const t = await resp.text()
      const j = JSON.parse(t)
      if (j && j.error) msg = j.error
    } catch { /* ignore */ }
    throw new Error(msg)
  }
  return resp.json()
}

function pickUpload(c) {
  uploadTarget.value = c
  uploadInput.value?.click()
}

async function onUploadChange(e) {
  const file = e.target.files?.[0]
  if (!file || !uploadTarget.value) return
  try {
    await uploadBinary(uploadTarget.value.id, file)
    ElMessage.success('二进制已上传')
    loadCustomApps()
  } catch (err) {
    ElMessage.error(err.message || '上传失败')
  } finally {
    e.target.value = ''
  }
}

function pseudoRecipe(c) {
  return {
    ID: 'custom-' + c.id,
    Name: c.name,
    Methods: c.type === 'docker' ? ['docker'] : ['native'],
    Variables: [],
    // 占位以通过前端兼容性判定（合成配方放行全部架构）
    Docker: c.type === 'docker' ? {} : undefined,
    Native: c.type !== 'docker' ? {} : undefined
  }
}

function installCustom(c) {
  if (c.type === 'binary' && !c.has_binary) {
    ElMessage.warning('请先上传二进制文件')
    return
  }
  openInstall(pseudoRecipe(c))
}

async function removeCustom(c) {
  try {
    await ElMessageBox.confirm(`确定删除自定义应用「${c.name}」吗？已安装实例不会被自动卸载。`, '提示', { type: 'warning' })
  } catch {
    return
  }
  try {
    await del(`/api/custom-apps/${c.id}`)
    ElMessage.success('已删除')
    loadCustomApps()
  } catch (e) {
    showErr(e, '删除失败')
  }
}
</script>

<style scoped>
.filter-bar {
  display: flex;
  gap: 10px;
  margin-bottom: 14px;
  flex-wrap: wrap;
}
.app-card {
  margin-bottom: 12px;
  cursor: pointer;
}
.app-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.app-name {
  font-weight: 700;
  font-size: 15px;
}
.app-desc {
  color: #909399;
  font-size: 13px;
  min-height: 38px;
  margin: 8px 0;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.app-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.app-actions {
  display: flex;
  gap: 6px;
  margin-top: 10px;
  flex-wrap: wrap;
}
.method-tag {
  margin: 0;
}
.port-hint {
  font-size: 12px;
  color: #c0c4cc;
}
.muted {
  color: #909399;
  font-size: 13px;
}
.hint {
  color: #909399;
  font-size: 12px;
  line-height: 1.6;
  display: block;
  margin-top: 4px;
}
</style>
