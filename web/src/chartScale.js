// 趋势图的纯计算部分。抽出来是为了能脱离 DOM/浏览器直接跑断言——
// 坐标算错在页面上只会表现为「线画歪了/画没了」，肉眼看不出来。

/** 上限取整到 1/2/5×10^n，避免顶部刻度出现 87.3 这种读数 */
export function niceMax(v) {
  if (!(v > 0)) return 1
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  const n = v / p
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * p
}

/**
 * 抽稀后的采样下标。返回**原始数据下标**，调用方据此取数，
 * 才能保证悬停读数对得上原始样本而不是抽稀后的近似值。
 */
export function downsample(n, maxPoints) {
  if (!(n > 0)) return []
  if (n <= maxPoints) return Array.from({ length: n }, (_, i) => i)
  const stride = Math.ceil(n / maxPoints)
  const idx = []
  for (let i = 0; i < n; i += stride) idx.push(i)
  // 必须保留最后一个点，否则最新一段数据会被裁掉
  if (idx[idx.length - 1] !== n - 1) idx.push(n - 1)
  return idx
}

/** 轴刻度文本：整数轴不要尾巴上的 .0，小数轴保留一位 */
export function axisLabel(v, max) {
  if (max >= 100) return String(Math.round(v))
  if (max >= 10) return v.toFixed(0)
  return v.toFixed(1).replace(/\.0$/, '')
}

/**
 * 把一条序列切成若干连续有效段。
 * 缺采样（null/非有限值）处必须断开，否则会在两个真实点之间连出一条不存在的直线。
 */
export function buildSegments(data, idx, max, xOf, yOf) {
  const segments = []
  let cur = []
  idx.forEach((di, k) => {
    const v = data[di]
    if (!Number.isFinite(v)) {
      if (cur.length > 1) segments.push(cur.join(' '))
      cur = []
      return
    }
    // 夹紧放在这里而不是交给 yOf：越界值（如异常上报出 120% 的 CPU）
    // 必须在**生成坐标这一步**就被压回画布内，否则会画出画布外、甚至负坐标。
    const clamped = Math.max(0, Math.min(v, max))
    cur.push(`${xOf(k).toFixed(1)},${yOf(clamped, max).toFixed(1)}`)
  })
  if (cur.length > 1) segments.push(cur.join(' '))
  return segments
}

/** 把鼠标横坐标换算成最近的抽稀下标（越界夹紧） */
export function hoverIndex(mx, padLeft, innerW, count) {
  if (count <= 0) return -1
  if (count === 1) return 0
  const k = Math.round(((mx - padLeft) / innerW) * (count - 1))
  return Math.max(0, Math.min(count - 1, k))
}
