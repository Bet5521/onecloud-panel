<template>
  <div>
    <div class="page-head">
      <h2>角色权限</h2>
    </div>

    <el-row :gutter="12">
      <!-- 角色列表 -->
      <el-col :xs="24" :sm="9" :md="7" :lg="6">
        <el-card shadow="never" body-class="role-list-card">
          <div v-for="r in roles" :key="r.id" class="role-item"
            :class="{ active: current?.id === r.id }" @click="selectRole(r)">
            <div class="role-item-main">
              <span class="role-name">{{ r.name }}</span>
              <el-tag v-if="r.protected" size="small" type="danger" effect="plain">受保护</el-tag>
              <el-tag v-else-if="r.builtin" size="small" type="info" effect="plain">内置</el-tag>
            </div>
            <span class="role-code">{{ r.code }} · {{ r.permissions.length }} 项权限</span>
          </div>
        </el-card>
      </el-col>

      <!-- 权限矩阵 -->
      <el-col :xs="24" :sm="15" :md="17" :lg="18">
        <el-card shadow="never" v-loading="loading">
          <template v-if="current">
            <div class="matrix-head">
              <div class="matrix-title">
                <h3>{{ current.name }}</h3>
                <el-tag v-if="current.protected" type="danger" effect="plain">内置受保护角色，权限只读</el-tag>
              </div>
              <el-button v-if="can('role:write') && !current.protected" type="primary"
                :loading="saving" @click="save">保存修改</el-button>
            </div>
            <el-form label-position="top" class="matrix">
              <el-form-item v-for="g in groups" :key="g.key" :label="g.label">
                <el-checkbox-group v-model="editPerms">
                  <el-checkbox v-for="p in g.perms" :key="p.value" :value="p.value"
                    :disabled="current.protected || !can('role:write')">
                    {{ p.label }}
                    <span class="perm-value">{{ p.value }}</span>
                  </el-checkbox>
                </el-checkbox-group>
              </el-form-item>
            </el-form>
          </template>
          <el-empty v-else description="请选择左侧角色" />
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { get, put, showErr } from '../api/http'
import { session } from '../session'

const can = (p) => session.can(p)

const roles = ref([])
const allPerms = ref([])
const current = ref(null)
const editPerms = ref([])
const loading = ref(false)
const saving = ref(false)

const GROUP_LABELS = {
  dashboard: '仪表盘',
  node: '节点管理',
  app: '应用管理',
  user: '用户管理',
  role: '角色权限',
  auditlog: '审计日志',
  settings: '面板设置'
}
const ACTION_LABELS = { read: '查看', write: '管理' }

const groups = computed(() => {
  const map = new Map()
  for (const p of allPerms.value) {
    const [mod, action] = p.split(':')
    if (!map.has(mod)) map.set(mod, { key: mod, label: GROUP_LABELS[mod] || mod, perms: [] })
    map.get(mod).perms.push({ value: p, label: ACTION_LABELS[action] || action })
  }
  return [...map.values()]
})

async function load() {
  loading.value = true
  try {
    const r = await get('/api/roles')
    roles.value = Array.isArray(r.items) ? r.items : []
    allPerms.value = Array.isArray(r.all_permissions) ? r.all_permissions : []
    if (current.value) {
      const found = roles.value.find((x) => x.id === current.value.id)
      if (found) selectRole(found)
    }
  } catch (e) {
    showErr(e, '角色加载失败')
  } finally {
    loading.value = false
  }
}

function selectRole(r) {
  current.value = r
  editPerms.value = Array.isArray(r.permissions) ? [...r.permissions] : []
}

async function save() {
  saving.value = true
  try {
    const updated = await put(`/api/roles/${current.value.id}`, {
      name: current.value.name,
      permissions: editPerms.value
    })
    ElMessage.success('权限已保存，用户重新登录或新请求时生效')
    const idx = roles.value.findIndex((x) => x.id === updated.id)
    if (idx >= 0) roles.value[idx] = updated
    selectRole(updated)
  } catch (e) {
    showErr(e, '权限保存失败')
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.page-head {
  margin-bottom: 12px;
}
.page-head h2 {
  margin: 0;
  font-size: 18px;
}
:deep(.role-list-card) {
  padding: 6px;
}
.role-item {
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.role-item:hover {
  background: #f2f6fc;
}
.role-item.active {
  background: #e6f0ff;
}
.role-item-main {
  display: flex;
  align-items: center;
  gap: 8px;
}
.role-name {
  font-weight: 600;
  color: #303133;
}
.role-code {
  font-size: 12px;
  color: #909399;
}
.matrix-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  flex-wrap: wrap;
  gap: 8px;
}
.matrix-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.matrix-title h3 {
  margin: 0;
  font-size: 16px;
}
.perm-value {
  color: #a8abb2;
  font-size: 12px;
  margin-left: 4px;
}
.matrix :deep(.el-form-item) {
  margin-bottom: 14px;
}
</style>
