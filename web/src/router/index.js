import { createRouter, createWebHistory } from 'vue-router'
import { session } from '../session'

const routes = [
  { path: '/setup', name: 'setup', component: () => import('../views/SetupView.vue'), meta: { public: true } },
  { path: '/login', name: 'login', component: () => import('../views/LoginView.vue'), meta: { public: true } },
  {
    path: '/',
    component: () => import('../views/MainLayout.vue'),
    children: [
      { path: '', redirect: '/dashboard' },
      { path: 'dashboard', name: 'dashboard', component: () => import('../views/DashboardView.vue') },
      { path: 'nodes', name: 'nodes', component: () => import('../views/NodesView.vue'), meta: { title: '节点管理', perm: 'node:read' } },
      { path: 'nodes/:nodeId/apps/:appId', name: 'app-detail', component: () => import('../views/AppDetailView.vue'), meta: { title: '应用详情', perm: 'app:read' } },
      { path: 'apps', name: 'apps', component: () => import('../views/AppsView.vue'), meta: { title: '应用管理', perm: 'app:read' } },
      { path: 'users', name: 'users', component: () => import('../views/UsersView.vue'), meta: { title: '用户管理', perm: 'user:read' } },
      { path: 'roles', name: 'roles', component: () => import('../views/RolesView.vue'), meta: { title: '角色权限', perm: 'role:read' } },
      { path: 'audit', name: 'audit', component: () => import('../views/AuditView.vue'), meta: { title: '审计日志', perm: 'auditlog:read' } },
      { path: 'notifications', name: 'notifications', component: () => import('../views/NotificationsView.vue'), meta: { title: '通知管理', perm: 'settings:read' } },
      { path: 'settings', name: 'settings', component: () => import('../views/SettingsView.vue'), meta: { title: '面板设置', perm: 'settings:read' } }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach(async (to) => {
  // 等待会话初始化完成
  if (!session.ready) {
    await new Promise((resolve) => {
      const t = setInterval(() => {
        if (session.ready) {
          clearInterval(t)
          resolve()
        }
      }, 20)
    })
  }

  // 未初始化：只允许向导页。
  // 严格判 === false：initialized 为 null 表示「状态未知」（/api/system/status
  // 探测失败）。此时把用户按「未初始化」处理，会把已初始化的面板整站重定向到
  // 安装向导 —— 一次网络抖动就够触发。未知时放行，由后续的登录守卫兜底
  // （无会话 → 登录页），用户重新登录即可恢复正常。
  if (session.initialized === false) {
    return to.name === 'setup' ? true : { name: 'setup' }
  }
  // 已初始化：向导页不再可访问
  if (session.initialized === true && to.name === 'setup') return { name: 'dashboard' }

  // 公开页（登录）
  if (to.meta.public) {
    if (to.name === 'login' && session.user) return { name: 'dashboard' }
    return true
  }

  // 受保护页：未登录 → 登录页
  if (!session.user) {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : undefined }
  }
  // 权限不足
  if (to.meta.perm && !session.can(to.meta.perm)) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
