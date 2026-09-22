<template>
  <div v-loading="loading">
    <!-- 统计卡片 -->
    <el-row :gutter="14">
      <el-col :xs="12" :sm="8" v-for="c in cards" :key="c.label" style="margin-bottom: 14px">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-value" :style="{ color: c.color }">{{ c.value }}</div>
          <div class="stat-label">{{ c.label }}</div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 节点资源与应用概览 -->
    <el-card shadow="never" style="margin-top: 4px">
      <template #header>
        <div class="card-head">
          <span>节点概览</span>
          <el-button text :icon="Refresh" @click="load">刷新</el-button>
        </div>
      </template>
      <el-row :gutter="14">
        <el-col :xs="24" :sm="12" :md="8" v-for="n in nodes" :key="n.id" style="margin-bottom: 14px">
          <div class="node-card" :class="{ offline: !n.online }">
            <div class="node-head">
              <span class="node-name">{{ n.name }}</span>
              <el-tag size="small" :type="n.online ? 'success' : 'info'" effect="plain">
                {{ n.online ? '在线' : '离线' }}
              </el-tag>
            </div>
            <div class="node-meta">
              <el-tag size="small" type="info" effect="plain">{{ networkLabel(n.network_type) }}</el-tag>
              <span class="muted" v-if="n.arch">{{ n.arch }}</span>
              <span class="muted" v-if="n.resources?.cpu_cores">{{ n.resources.cpu_cores }} 核</span>
            </div>

            <div v-if="n.resources?.live" class="water-row">
              <span class="water-label">内存</span>
              <el-progress :percentage="pct(n.resources.mem_used, n.resources.mem_total)"
                :status="waterStatus(n.resources.mem_used, n.resources.mem_total)" :stroke-width="10" />
              <span class="water-text">{{ fmtBytes(n.resources.mem_used) }}/{{ fmtBytes(n.resources.mem_total) }}</span>
            </div>
            <div v-if="n.resources?.live" class="water-row">
              <span class="water-label">磁盘</span>
              <el-progress :percentage="pct(n.resources.disk_used, n.resources.disk_total)"
                :status="waterStatus(n.resources.disk_used, n.resources.disk_total)" :stroke-width="10" />
              <span class="water-text">{{ fmtBytes(n.resources.disk_used) }}/{{ fmtBytes(n.resources.disk_total) }}</span>
            </div>
            <div v-if="n.resources?.live" class="node-load muted">
              负载 {{ n.resources.load_avg?.map((x) => x.toFixed(2)).join(' / ') }}
              <span v-if="n.resources.uptime_seconds"> · 运行 {{ fmtUptime(n.resources.uptime_seconds) }}</span>
            </div>
            <div v-else class="muted" style="font-size:12px">实时资源不可用（节点离线或采集超时）</div>

            <div class="node-apps">
              <span class="apps-total">应用 {{ n.apps.total }}</span>
              <el-tag size="small" type="success" effect="plain">运行 {{ n.apps.running }}</el-tag>
              <el-tag size="small" :type="n.apps.error > 0 ? 'danger' : 'info'" effect="plain">
                异常 {{ n.apps.error }}
              </el-tag>
              <el-tag size="small" type="warning" effect="plain" v-if="n.apps.stopped">停止 {{ n.apps.stopped }}</el-tag>
            </div>
          </div>
        </el-col>
      </el-row>
      <div v-if="nodes.length === 0" class="muted">暂无节点</div>
    </el-card>

    <el-row :gutter="14" style="margin-top: 14px">
      <!-- 最近任务 -->
      <el-col :xs="24" :sm="10">
        <el-card shadow="never">
          <template #header><span>最近后台任务</span></template>
          <el-timeline>
            <el-timeline-item v-for="t in recentTasks" :key="t.id"
              :type="taskDot[t.status] || 'info'" :timestamp="fmtTime(t.created_at)">
              {{ taskTypeText[t.type] || t.type }}
              <span v-if="t.app_id" class="muted"> · {{ t.app_id }}</span>
            </el-timeline-item>
          </el-timeline>
          <div v-if="recentTasks.length === 0" class="muted">暂无后台任务</div>
        </el-card>
      </el-col>

      <!-- 最近审计 -->
      <el-col :xs="24" :sm="14">
        <el-card shadow="never">
          <template #header><span>最近审计事件</span></template>
          <el-table :data="recentAudits" size="small" style="width: 100%">
            <el-table-column prop="Username" label="用户" width="100" />
            <el-table-column label="模块/动作" width="150">
              <template #default="{ row }">{{ auditModuleLabel(row.Module) }} / {{ row.Action }}</template>
            </el-table-column>
            <el-table-column label="目标" min-width="120" show-overflow-tooltip>
              <template #default="{ row }">
                <span v-if="row.TargetType || row.TargetID">{{ row.TargetType }}<span v-if="row.TargetID">#{{ row.TargetID }}</span></span>
                <span v-else class="muted">-</span>
              </template>
            </el-table-column>
            <el-table-column label="结果" width="80">
              <template #default="{ row }">
                <el-tag size="small" :type="row.Result === 'success' ? 'success' : 'danger'">
                  {{ row.Result === 'success' ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="时间" width="150">
              <template #default="{ row }">{{ fmtTime(row.Ts) }}</template>
            </el-table-column>
          </el-table>
          <div v-if="recentAudits.length === 0" class="muted">暂无审计事件</div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { get, showErr } from '../api/http'
import { fmtTime, fmtBytes } from '../utils'

const loading = ref(true)
const stats = ref({})
const appStats = ref({ total: 0, running: 0, error: 0, stopped: 0 })
const recentAudits = ref([])
const recentTasks = ref([])
const nodes = ref([])

const cards = computed(() => [
  { label: '节点总数', value: stats.value.nodes_total ?? 0, color: '#409eff' },
  { label: '在线节点', value: stats.value.nodes_online ?? 0, color: '#67c23a' },
  { label: '应用总数', value: appStats.value.total ?? 0, color: '#e6a23c' },
  { label: '应用运行中', value: appStats.value.running ?? 0, color: '#67c23a' },
  { label: '应用异常', value: appStats.value.error ?? 0, color: (appStats.value.error ?? 0) > 0 ? '#f56c6c' : '#67c23a' },
  { label: 'Docker 节点', value: stats.value.docker ?? 0, color: '#909399' }
])

const networkLabels = {
  lan: '局域网', wireguard: 'WireGuard', public: '公网', local: '本机'
}
function networkLabel(t) { return networkLabels[t] || t || '局域网' }

const taskDot = { success: 'success', failure: 'danger', running: 'warning', queued: 'info' }
const taskTypeText = {
  app_install: '安装应用', app_uninstall: '卸载应用',
  app_start: '启动应用', app_stop: '停止应用', app_restart: '重启应用',
  docker_install: '安装 Docker', panel_restart: '重启面板'
}
const auditModuleLabels = {
  auth: '认证', node: '节点', app: '应用', user: '用户',
  role: '角色', settings: '设置', system: '系统'
}
function auditModuleLabel(m) { return auditModuleLabels[m] || m || '-' }

function pct(used, total) {
  if (!total) return 0
  return Math.min(100, Math.round((used / total) * 100))
}
function waterStatus(used, total) {
  return pct(used, total) >= 90 ? 'exception' : ''
}
function fmtUptime(s) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  return d > 0 ? `${d}天${h}时` : `${h}时${Math.floor((s % 3600) / 60)}分`
}

async function load() {
  loading.value = true
  try {
    const d = await get('/api/dashboard/summary')
    stats.value = d.stats ?? {}
    appStats.value = d.app_stats ?? { total: 0, running: 0, error: 0, stopped: 0 }
    recentAudits.value = d.recent_audits ?? []
    recentTasks.value = d.recent_tasks ?? []
    nodes.value = d.nodes ?? []
  } catch (e) {
    showErr(e, '仪表盘加载失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.stat-card { text-align: center; }
.stat-value { font-size: 30px; font-weight: 700; line-height: 1.2; }
.stat-label { color: #909399; font-size: 13px; margin-top: 4px; }
.card-head { display: flex; justify-content: space-between; align-items: center; }
.node-card {
  border: 1px solid #ebeef5;
  border-radius: 6px;
  padding: 12px;
  height: 100%;
  background: #fff;
}
.node-card.offline { opacity: 0.75; }
.node-head { display: flex; justify-content: space-between; align-items: center; }
.node-name { font-weight: 600; font-size: 15px; }
.node-meta { display: flex; gap: 8px; align-items: center; margin: 6px 0 10px; font-size: 12px; }
.water-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.water-label { width: 32px; color: #606266; font-size: 13px; }
.water-text { font-size: 12px; color: #909399; white-space: nowrap; }
.node-load { font-size: 12px; margin-bottom: 8px; }
.node-apps { display: flex; gap: 6px; align-items: center; margin-top: 6px; flex-wrap: wrap; }
.apps-total { font-size: 13px; font-weight: 600; color: #303133; margin-right: 4px; }
.muted { color: #909399; font-size: 13px; }
</style>
