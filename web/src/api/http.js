// 统一 fetch 封装：JSON 编解码、错误提取、401 全局会话失效。
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
    session.clear()
    session.expired = true
    return null
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

export const get = (url) => request('GET', url)
export const post = (url, body) => request('POST', url, body ?? {})
export const put = (url, body) => request('PUT', url, body ?? {})
export const del = (url) => request('DELETE', url)
