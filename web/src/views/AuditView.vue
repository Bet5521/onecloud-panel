<template>
  <div>
    <div class="page-head">
      <h2>审计日志</h2>
    </div>

    <el-card shadow="never" class="filter-card">
      <el-form :inline="true" :model="filter" @submit.prevent>
        <el-form-item label="用户名">
          <el-input v-model="filter.username" placeholder="用户名" clearable style="width: 140px"
            @keyup.enter="onSearch" @clear="onSearch" />
        </el-form-item>
        <el-form-item label="模块">
          <el-select v-model="filter.module" placeholder="全部" clearable style="width: 130px" @change="onSearch">
            <el-option v-for="m in moduleOptions" :key="m.value" :label="m.label" :value="m.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="结果">
          <el-select v-model="filter.result" placeholder="全部" clearable style="width: 110px" @change="onSearch">
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failure" />
          </el-select>
        </el-form-item>
        <el-form-item label="动作">
          <el-input v-model="filter.action" placeholder="如 login / install" clearable style="width: 150px"
            @keyup.enter="onSearch" @clear="onSearch" />
        </el-form-item>
        <el-form-item label="时间">
          <el-date-picker v-model="dateRange" type="datetimerange" range-separator="至"
            start-placeholder="开始" end-placeholder="结束" value-format="X" style="width: 340px" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :icon="Search" @click="onSearch">查询</el-button>
          <el-button :icon="RefreshLeft" @click="onReset">重置</el-button>
          <el-button :icon="Download" @click="onExport">导出 CSV</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" style="margin-top: 12px">
      <el-table :data="logs" v-loading="loading" stripe>
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="detail-box">
              <div v-if="row.RequestID"><b>请求 ID：</b>{{ row.RequestID }}</div>
              <div><b>详情：</b>{{ row.Detail || '（无）' }}</div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="时间" min-width="170">
          <template #default="{ row }">{{ fmtTime(row.Ts) }}</template>
        </el-table-column>
        <el-table-column prop="Username" label="用户" min-width="110" />
        <el-table-column prop="IP" label="IP" min-width="120" />
        <el-table-column label="模块" min-width="90">
          <template #default="{ row }">{{ moduleLabel(row.Module) }}</template>
        </el-table-column>
        <el-table-column prop="Action" label="动作" min-width="110" />
        <el-table-column label="目标" min-width="120">
          <template #default="{ row }">
            <span v-if="row.TargetType || row.TargetID">{{ row.TargetType }}<span v-if="row.TargetID">#{{ row.TargetID }}</span></span>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <el-tag :type="row.Result === 'success' ? 'success' : 'danger'" size="small">
              {{ row.Result === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>

      <div class="pager">
        <el-pagination background layout="total, sizes, prev, pager, next"
          :total="total" :current-page="page" :page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="(p) => { page = p; load() }"
          @size-change="(s) => { pageSize = s; page = 1; load() }" />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Search, RefreshLeft, Download } from '@element-plus/icons-vue'
import { get, showErr } from '../api/http'
import { fmtTime } from '../utils'

const moduleOptions = [
  { value: 'auth', label: '认证' },
  { value: 'node', label: '节点' },
  { value: 'app', label: '应用' },
  { value: 'user', label: '用户' },
  { value: 'role', label: '角色' },
  { value: 'settings', label: '设置' },
  { value: 'audit', label: '审计' },
  { value: 'system', label: '系统' }
]
const moduleLabel = (m) => moduleOptions.find((x) => x.value === m)?.label || m

const filter = ref({ username: '', module: '', result: '', action: '' })
const dateRange = ref([])
const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)

// buildQuery 汇总筛选条件（查询与导出共用，导出不分页）。
function buildQuery(withPage) {
  const params = new URLSearchParams()
  if (filter.value.username) params.set('username', filter.value.username)
  if (filter.value.module) params.set('module', filter.value.module)
  if (filter.value.result) params.set('result', filter.value.result)
  if (filter.value.action) params.set('action', filter.value.action)
  if (Array.isArray(dateRange.value) && dateRange.value.length === 2) {
    params.set('start', dateRange.value[0])
    params.set('end', dateRange.value[1])
  }
  if (withPage) {
    params.set('page', String(page.value))
    params.set('page_size', String(pageSize.value))
  }
  return params.toString()
}

async function load() {
  loading.value = true
  try {
    const d = await get(`/api/audit-logs?${buildQuery(true)}`)
    logs.value = Array.isArray(d.items) ? d.items : []
    total.value = d.total || 0
  } catch (e) {
    showErr(e, '审计日志加载失败')
  } finally {
    loading.value = false
  }
}

// onExport 按当前筛选条件导出 CSV（鉴权走会话 Cookie，直接触发浏览器下载）。
function onExport() {
  const a = document.createElement('a')
  a.href = `/api/audit-logs/export?${buildQuery(false)}`
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

function onSearch() {
  page.value = 1
  load()
}
function onReset() {
  filter.value = { username: '', module: '', result: '', action: '' }
  dateRange.value = []
  page.value = 1
  load()
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
:deep(.filter-card) .el-form-item {
  margin-bottom: 0;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 14px;
}
.detail-box {
  padding: 8px 20px;
  color: #606266;
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.muted {
  color: #c0c4cc;
}
</style>
