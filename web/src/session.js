import { reactive } from 'vue'
import { get } from './api/http'

// 全局会话状态（基于 Cookie，前端只存展示信息）。
export const session = reactive({
  ready: false,
  initialized: false,
  user: null, // {id, username, role_code, permissions}
  panelName: 'OneCloud Panel',
  expired: false,

  get permissions() {
    return this.user?.permissions ?? []
  },
  can(perm) {
    return this.permissions.includes(perm)
  },
  clear() {
    this.user = null
  }
})

export async function initSession() {
  try {
    const st = await get('/api/system/status')
    session.initialized = !!st.initialized
    if (session.initialized) {
      try {
        const me = await get('/api/auth/me')
        session.user = {
          id: me.id,
          username: me.username,
          real_name: me.real_name || '',
          phone: me.phone || '',
          role_code: me.role,
          role_name: me.role_name,
          permissions: me.permissions
        }
        if (me.panel_name) session.panelName = me.panel_name
      } catch {
        session.user = null
      }
    }
  } finally {
    session.ready = true
  }
}

// 设置面板名称（供设置页保存后调用，实时刷新侧边栏与标题）。
export function setPanelName(name) {
  if (name) session.panelName = name
}
