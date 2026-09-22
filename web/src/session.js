import { reactive } from 'vue'
import { get } from './api/http'

// 全局会话状态（基于 Cookie，前端只存展示信息）。
export const session = reactive({
  ready: false,
  // 面板是否已完成初始化。三态：
  //   true  已初始化   false 未初始化（需走向导）   null 未知（探测失败）
  // 必须保留 null：把「探测失败」当成 false，会让已初始化的面板被路由守卫
  // 重定向进安装向导。
  initialized: null,
  // 状态探测失败（面板不可达等）。供守卫/界面按「未知」降级处理。
  loadFailed: false,
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

// 初始化/刷新会话：读取面板状态与当前登录用户。
// 本函数**不抛错**，两种失败都在内部消化：
//  1) 状态探测失败 → initialized 保持「未知」并置 loadFailed。
//     原先这里让异常冒泡：main.js 的 .finally() 只保证挂载，异常变成
//     unhandled rejection，而 initialized 停在初始 false →
//     路由守卫把已初始化的面板整站重定向到安装向导。
//  2) 读取当前用户失败 → user 置空（未登录/会话失效由 http.js 统一处理）。
export async function initSession() {
  try {
    const st = await get('/api/system/status')
    session.initialized = !!st.initialized
    session.loadFailed = false
  } catch {
    session.loadFailed = true
  }

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

  session.ready = true
}

// 设置面板名称（供设置页保存后调用，实时刷新侧边栏与标题）。
export function setPanelName(name) {
  if (name) session.panelName = name
}
