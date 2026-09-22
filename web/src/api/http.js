// 统一 fetch 封装：JSON 编解码、错误提取、401 全局会话失效。
import { ElMessage } from 'element-plus'
import { session } from '../session'

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

export async function request(method, url, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' }
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json'
    opt.body = JSON.stringify(body)
  }
  let resp
  try {
    resp = await fetch(url, opt)
  } catch (e) {
    throw new ApiError('网络异常，无法连接面板服务', 0)
  }

  if (resp.status === 401 && !url.endsWith('/auth/login')) {
    // 只有「本来已登录」才叫会话过期。启动时的 /api/auth/me 探测同样返回
    // 401（还没登录），若一律置 expired，首次访问的用户会看到
    // 「登录状态已过期」这种莫名其妙的话。
    const wasLoggedIn = !!session.user
    session.clear()
    if (wasLoggedIn) session.expired = true
    // 会话失效必须抛错。早先这里 return null，调用方随后在 d.items 上抛
    // 语义不明的 TypeError，把「登录过期」掩盖成「前端 bug」。
    // 跳转由 App.vue 监听 session.user 变为 null 完成，不依赖返回值。
    const err = new ApiError('登录状态已过期，请重新登录', 401)
    err.sessionExpired = true
    throw err
  }

  let data = null
  const text = await resp.text()
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = text
    }
  }
  if (!resp.ok) {
    const msg = data && data.error ? data.error : `请求失败（${resp.status}）`
    throw new ApiError(msg, resp.status)
  }
  return data
}

// 统一的请求错误提示。
// 会话失效（sessionExpired）不弹窗：全局已跳转登录页并在页头给出说明，
// 再弹一次属于重复打扰。注意登录接口密码错误同样是 401，但不带该标记，
// 因此仍会正常提示。
export function showErr(e, fallback) {
  if (e && e.sessionExpired) return
  ElMessage.error((e && e.message) || fallback || '操作失败')
}

export const get = (url) => request('GET', url)
export const post = (url, body) => request('POST', url, body ?? {})
export const put = (url, body) => request('PUT', url, body ?? {})
export const del = (url) => request('DELETE', url)
