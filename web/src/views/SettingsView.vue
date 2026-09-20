<template>
  <div>
    <div class="page-head">
      <h2>面板设置</h2>
    </div>

    <el-row :gutter="12">
      <!-- ============ 左列 ============ -->
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" v-loading="loading">
          <template #header><span>基本设置</span></template>
          <el-form label-width="110px">
            <el-form-item label="面板名称">
              <el-input v-model="form.panel_name" :disabled="!can('settings:write')" maxlength="64" show-word-limit />
            </el-form-item>
            <el-form-item label="日志保留天数">
              <el-input-number v-model="form.audit_retention_days" :min="0" :max="3650"
                :disabled="!can('settings:write')" />
              <span class="hint">0 表示永久保留</span>
            </el-form-item>
            <el-form-item label="GitHub 加速">
              <el-input v-model="form.github_proxy" :disabled="!can('settings:write')"
                placeholder="如 https://gh-proxy.com ，留空直连" clearable />
              <span class="hint">GitHub 直连受限时填写反代前缀，应用安装下载自动生效</span>
            </el-form-item>
            <el-form-item label="安装时间">
              <span class="muted">{{ info.installed_at ? fmtTime(Number(info.installed_at)) : '-' }}</span>
            </el-form-item>
            <el-form-item v-if="can('settings:write')">
              <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
            </el-form-item>
          </el-form>
        </el-card>

        <!-- ============ 传输安全 HTTPS ============ -->
        <el-card shadow="never" class="section-gap" v-loading="loading">
          <template #header>
            <div class="card-head">
              <span>传输安全（HTTPS）</span>
              <el-tag size="small" :type="tls.enabled ? 'success' : 'info'">
                {{ tls.enabled ? '已启用' : '未启用' }}
              </el-tag>
            </div>
          </template>

          <el-form label-width="110px">
            <el-form-item label="启用 HTTPS">
              <el-switch v-model="tls.enabled" :disabled="!can('settings:write')" />
              <span class="hint">开启后 HTTP 与 HTTPS 在同一端口共存</span>
            </el-form-item>

            <template v-if="tls.enabled">
              <el-form-item label="证书方式">
                <el-radio-group v-model="tls.mode" :disabled="!can('settings:write')">
                  <el-radio value="selfsigned">自动生成自签证书</el-radio>
                  <el-radio value="custom">上传自定义证书</el-radio>
                </el-radio-group>
              </el-form-item>

              <!-- 自签 -->
              <template v-if="tls.mode === 'selfsigned'">
                <el-form-item label="证书域名">
                  <el-input v-model="tls.hosts" type="textarea" :rows="2"
                    :disabled="!can('settings:write')"
                    placeholder="附加域名/IP，逗号分隔；留空自动收集 hostname 与本机全部 IP，如：panel.local,192.168.6.194" />
                  <span class="hint">证书 SAN 始终自动包含 localhost、主机名与本机 IP，此处仅附加额外名称</span>
                </el-form-item>
              </template>

              <!-- 自定义 -->
              <template v-if="tls.mode === 'custom'">
                <el-form-item label="证书 PEM">
                  <el-input v-model="tls.cert_pem" type="textarea" :rows="4"
                    :disabled="!can('settings:write')"
                    placeholder="-----BEGIN CERTIFICATE-----" />
                </el-form-item>
                <el-form-item label="私钥 PEM">
                  <el-input v-model="tls.key_pem" type="textarea" :rows="4"
                    :disabled="!can('settings:write')"
                    placeholder="-----BEGIN PRIVATE KEY-----" />
                </el-form-item>
              </template>

              <el-form-item label="强制 HTTPS">
                <el-switch v-model="tls.force_https" :disabled="!can('settings:write')" />
                <span class="hint">开启后明文 HTTP 请求自动 301 跳转到 HTTPS（/healthz 除外）</span>
              </el-form-item>
            </template>

            <el-form-item v-if="can('settings:write')">
              <el-button type="primary" :loading="tlsSaving" @click="saveTLS">
                应用 HTTPS 配置
              </el-button>
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>

      <!-- ============ 右列 ============ -->
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="mobile-gap">
          <template #header><span>版本与运行</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="版本">{{ status.version || info.version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="构建提交">{{ status.commit || '-' }}</el-descriptions-item>
            <el-descriptions-item label="systemd 状态">
              <el-tag :type="status.systemd_active ? 'success' : 'info'" size="small">
                {{ status.systemd_active ? status.systemd_state || 'active' : status.systemd_detail || '未托管' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="监听地址">{{ status.listen || '-' }}</el-descriptions-item>
            <el-descriptions-item label="已运行">{{ fmtDuration(status.uptime_seconds) }}</el-descriptions-item>
            <el-descriptions-item label="二进制路径"><span class="path">{{ status.binary || '-' }}</span></el-descriptions-item>
            <el-descriptions-item label="数据目录"><span class="path">{{ status.data_dir || '-' }}</span></el-descriptions-item>
          </el-descriptions>
        </el-card>

        <!-- ============ 邮件服务 SMTP ============ -->
        <el-card shadow="never" class="section-gap">
          <template #header>
            <div class="card-head">
              <span>邮件服务（SMTP）</span>
              <el-tag size="small" :type="smtpReady ? 'success' : 'info'">
                {{ smtpReady ? '已配置' : '未配置' }}
              </el-tag>
            </div>
          </template>
          <el-form label-width="110px">
            <el-form-item label="SMTP 主机">
              <el-input v-model="smtp.host" :disabled="!can('settings:write')"
                placeholder="如 smtp.qq.com" clearable />
            </el-form-item>
            <el-form-item label="端口">
              <el-input v-model="smtp.port" :disabled="!can('settings:write')"
                placeholder="465（SSL）/ 587（STARTTLS）/ 25" class="w160" />
            </el-form-item>
            <el-form-item label="用户名">
              <el-input v-model="smtp.username" :disabled="!can('settings:write')"
                placeholder="SMTP 登录用户名" clearable />
            </el-form-item>
            <el-form-item label="密码/授权码">
              <el-input v-model="smtp.password" type="password" show-password
                :disabled="!can('settings:write')" placeholder="SMTP 密码或邮箱授权码" />
            </el-form-item>
            <el-form-item label="发件地址">
              <el-input v-model="smtp.from" :disabled="!can('settings:write')"
                placeholder="如 user@qq.com" clearable />
            </el-form-item>
            <el-form-item label="测试收件人">
              <el-input v-model="smtp.test_recipient" :disabled="!can('settings:write')"
                placeholder="留空则发送给发件地址自身" clearable />
            </el-form-item>
            <el-form-item v-if="can('settings:write')">
              <el-button :loading="smtpSaving" @click="saveSMTP">保存 SMTP</el-button>
              <el-button :loading="smtpTesting" @click="testSMTP">发送测试邮件</el-button>
            </el-form-item>
          </el-form>
          <p class="hint">SMTP 用于接收登录页密码自助重置码；不配置时重置码会输出到面板服务日志。</p>
        </el-card>

        <!-- ============ Docker 镜像加速与仓库 ============ -->
        <el-card shadow="never" class="section-gap" v-loading="loading">
          <template #header><span>Docker 镜像加速与第三方仓库（面板级默认）</span></template>
          <el-form label-width="130px">
            <el-form-item label="镜像加速地址">
              <el-input v-model="dockerCfg.mirrors" type="textarea" :rows="3"
                :disabled="!can('settings:write')"
                placeholder="每行一个，如 https://docker.1ms.run" />
              <span class="hint">留空则使用内置默认加速地址；节点级配置优先</span>
            </el-form-item>
            <el-form-item label="第三方仓库">
              <el-input v-model="dockerCfg.insecure_registries" type="textarea" :rows="2"
                :disabled="!can('settings:write')"
                placeholder="每行一个，如 harbor.example.com 或 192.168.1.10:5000" />
              <span class="hint">配置 insecure-registries（HTTP 仓库）；HTTPS 仓库无需配置</span>
            </el-form-item>
            <el-form-item v-if="can('settings:write')">
              <el-button type="primary" :loading="dockerSaving" @click="saveDocker">保存 Docker 配置</el-button>
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, put, post } from '../api/http'
import { session, setPanelName } from '../session'
import { fmtTime, fmtDuration } from '../utils'

const can = (p) => session.can(p)

const loading = ref(false)
const saving = ref(false)
const form = ref({ panel_name: '', audit_retention_days: 30, github_proxy: '' })
const info = ref({})
const status = ref({})

// ---- HTTPS ----
const tlsSaving = ref(false)
const tls = ref({
  enabled: false, mode: 'selfsigned', hosts: '',
  cert_pem: '', key_pem: '', force_https: false
})

// ---- SMTP ----
const smtpSaving = ref(false)
const smtpTesting = ref(false)
const smtp = ref({ host: '', port: '', username: '', password: '', from: '', test_recipient: '' })
const smtpReady = computed(() => smtp.value.host && smtp.value.port && smtp.value.from)

// ---- Docker ----
const dockerSaving = ref(false)
const dockerCfg = ref({ mirrors: '', insecure_registries: '' })

async function load() {
  loading.value = true
  try {
    const [s, i] = await Promise.all([
      get('/api/settings'),
      get('/api/panel/info')
    ])
    info.value = i || {}
    form.value.panel_name = s.panel_name || ''
    form.value.audit_retention_days = s.audit_retention_days !== undefined
      ? Number(s.audit_retention_days) : 30
    form.value.github_proxy = s.github_proxy || ''

    // TLS 回填（不含任何私钥内容）
    tls.value.enabled = s.tls_enabled === '1'
    tls.value.mode = s.tls_mode || 'selfsigned'
    tls.value.hosts = s.tls_hosts || ''
    tls.value.force_https = s.tls_force_https === '1'
    tls.value.cert_pem = ''
    tls.value.key_pem = ''

    // SMTP 回填（密码仍回显，属于本地管理界面）
    smtp.value = {
      host: s.smtp_host || '',
      port: s.smtp_port || '',
      username: s.smtp_username || '',
      password: s.smtp_password || '',
      from: s.smtp_from || '',
      test_recipient: s.smtp_test_recipient || ''
    }

    dockerCfg.value = {
      mirrors: s.docker_registry_mirrors || '',
      insecure_registries: s.docker_insecure_registries || ''
    }

    try {
      status.value = await get('/api/panel/status')
    } catch {
      status.value = {}
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.panel_name.trim()) {
    ElMessage.warning('面板名称不能为空')
    return
  }
  saving.value = true
  try {
    await put('/api/settings', {
      panel_name: form.value.panel_name.trim(),
      audit_retention_days: String(form.value.audit_retention_days),
      github_proxy: form.value.github_proxy.trim()
    })
    setPanelName(form.value.panel_name.trim())
    ElMessage.success('设置已保存')
  } finally {
    saving.value = false
  }
}

// ---- HTTPS 保存：成功后询问是否立即重启 ----
async function saveTLS() {
  if (tls.value.enabled && tls.value.mode === 'custom') {
    if (!tls.value.cert_pem.trim() || !tls.value.key_pem.trim()) {
      ElMessage.warning('请填写证书与私钥 PEM 内容')
      return
    }
  }
  tlsSaving.value = true
  try {
    await post('/api/settings/tls', {
      enabled: tls.value.enabled,
      mode: tls.value.mode,
      hosts: tls.value.hosts,
      cert_pem: tls.value.cert_pem,
      key_pem: tls.value.key_pem,
      force_https: tls.value.force_https
    })
    ElMessage.success('HTTPS 配置已保存，重启面板后生效')
    try {
      await ElMessageBox.confirm('是否立即重启面板使 HTTPS 配置生效？', '重启确认', {
        confirmButtonText: '立即重启',
        cancelButtonText: '稍后手动重启',
        type: 'warning'
      })
      await post('/api/panel/restart')
      ElMessage.info('面板正在重启，约 2 秒后完成')
    } catch {
      // 用户选择稍后重启，忽略
    }
  } catch (e) {
    ElMessage.error(e.message || 'HTTPS 配置失败')
  } finally {
    tlsSaving.value = false
  }
}

// ---- SMTP ----
async function saveSMTP() {
  smtpSaving.value = true
  try {
    await put('/api/settings', {
      smtp_host: smtp.value.host.trim(),
      smtp_port: smtp.value.port.trim(),
      smtp_username: smtp.value.username.trim(),
      smtp_password: smtp.value.password,
      smtp_from: smtp.value.from.trim(),
      smtp_test_recipient: smtp.value.test_recipient.trim()
    })
    ElMessage.success('SMTP 配置已保存')
  } finally {
    smtpSaving.value = false
  }
}

async function testSMTP() {
  smtpTesting.value = true
  try {
    const r = await post('/api/settings/smtp-test', {
      to: smtp.value.test_recipient.trim()
    })
    ElMessage.success(r.hint || '测试邮件已发送')
  } catch (e) {
    ElMessage.error(e.message || '发送失败')
  } finally {
    smtpTesting.value = false
  }
}

// ---- Docker ----
async function saveDocker() {
  dockerSaving.value = true
  try {
    await put('/api/settings', {
      docker_registry_mirrors: dockerCfg.value.mirrors.trim(),
      docker_insecure_registries: dockerCfg.value.insecure_registries.trim()
    })
    ElMessage.success('Docker 配置已保存')
  } finally {
    dockerSaving.value = false
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
.section-gap {
  margin-top: 12px;
}
.card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.hint {
  margin-left: 10px;
  color: #909399;
  font-size: 12px;
}
.muted {
  color: #909399;
}
.path {
  word-break: break-all;
  font-size: 12px;
  color: #606266;
}
.w160 {
  width: 160px;
}
@media (max-width: 1199px) {
  .mobile-gap {
    margin-top: 12px;
  }
}
</style>
