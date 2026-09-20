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

  <TaskProgressDialog v-if="taskId" :task-id="taskId"
    @close="taskId = null" @finished="onFinished" />
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { get, post } from '../api/http'
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
  } finally {
    submitting.value = false
  }
}

const taskId = ref(null)
function onFinished() {
  tab.value = 'installed'
  loadInstalled()
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
</style>
