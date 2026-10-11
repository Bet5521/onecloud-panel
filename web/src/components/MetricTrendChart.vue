<template>
  <div ref="wrap" class="mlc">
    <div v-if="!hasData" class="mlc-empty muted">暂无可绘制的采样数据</div>
    <template v-else>
      <svg v-if="width > 0" :width="width" :height="height" class="mlc-svg" @mousemove="onMove"
        @mouseleave="hover = -1">
        <!-- 横向网格与左右轴刻度 -->
        <g>
          <line v-for="t in yTicks" :key="'gl' + t.v" :x1="pad.l" :x2="width - pad.r" :y1="t.y" :y2="t.y"
            stroke="#ebeef5" stroke-width="1" />
          <text v-for="t in yTicks" :key="'gt' + t.v" :x="pad.l - 6" :y="t.y + 4" text-anchor="end"
            class="mlc-axis">{{ t.label }}</text>
          <text v-for="t in yTicks" :key="'gr' + t.v" :x="width - pad.r + 6" :y="t.y + 4" text-anchor="start"
            class="mlc-axis mlc-axis-r">{{ t.labelR }}</text>
        </g>

        <!-- 时间轴刻度 -->
        <g>
          <text v-for="t in xTicks" :key="'xt' + t.k" :x="t.x" :y="height - 6" text-anchor="middle"
            class="mlc-axis">{{ t.label }}</text>
        </g>

        <!-- 数据线：按连续有效段拆分，缺采样处断开而不是拉一条假直线 -->
        <g v-for="s in drawn" :key="s.name">
          <polyline v-for="(seg, i) in s.segments" :key="i" :points="seg" fill="none" :stroke="s.color"
            :stroke-width="s.width || 1.6" :stroke-dasharray="s.dash || null" stroke-linejoin="round"
            stroke-linecap="round" />
          <!-- 只有一个采样点时折线画不出来，退化成圆点，避免「有数据却空白」 -->
          <circle v-if="singlePoint" :cx="xOf(0)" :cy="yOf(s.data[sampled[0]], s.max)" r="3" :fill="s.color" />
        </g>

        <g v-if="hover >= 0">
          <line :x1="hoverX" :x2="hoverX" :y1="pad.t" :y2="height - pad.b" stroke="#c0c4cc"
            stroke-dasharray="3 3" />
          <circle v-for="s in drawn" :key="'hd' + s.name" :cx="hoverX" :cy="dotY(s)" r="3.4" :fill="s.color"
            stroke="#fff" stroke-width="1.4" />
        </g>
      </svg>

      <!-- 图例：点击可隐藏/显示该指标 -->
      <div class="mlc-legend">
        <span v-for="s in series" :key="s.name" class="mlc-lg" :class="{ off: hidden.has(s.name) }"
          @click="toggle(s.name)" :title="s.axis === 'right' ? '使用右侧坐标轴' : '使用左侧坐标轴'">
          <i :style="{ background: s.color }"></i>{{ s.name }}
          <em v-if="s.axis === 'right'">右轴</em>
        </span>
      </div>

      <div v-if="tip" class="mlc-tip" :style="tipStyle">
        <div class="mlc-tip-t">{{ tip.label }}</div>
        <div v-for="v in tip.values" :key="v.name" class="mlc-tip-r">
          <i :style="{ background: v.color }"></i>
          <span>{{ v.name }}</span>
          <b>{{ v.text }}</b>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { niceMax, downsample, axisLabel, buildSegments, hoverIndex } from '../chartScale'

// 零依赖 SVG 折线图。
// 之所以不引第三方图表库：面板前端会被打包进 Go 二进制、跑在 armv7 这类小设备上，
// 为一个趋势图拖进上百 KB 的运行时不划算；而这里的图形需求（多序列、双 Y 轴、
// 悬停读数）用 SVG 直写完全可控，也没有主题/版本适配的隐患。
const props = defineProps({
  // X 轴文本，长度与每条 series.data 一致（通常就是采样时间）
  labels: { type: Array, default: () => [] },
  // 数据序列：{ name, color, data:number[], axis?:'right', dash?, width?, fmt?(v)=>string }
  series: { type: Array, default: () => [] },
  height: { type: Number, default: 300 },
  // 左轴上限；百分比类指标传 100，否则按数据自适应取整
  yMax: { type: Number, default: null },
  // 渲染前抽稀上限：24h×30s 有 2880 点，逐点进 DOM 没有意义
  maxPoints: { type: Number, default: 600 }
})

const wrap = ref(null)
const width = ref(0)
const hidden = ref(new Set())
const hover = ref(-1) // 抽稀后的索引
let ro = null

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && wrap.value) {
    ro = new ResizeObserver((entries) => {
      const w = Math.floor(entries[0].contentRect.width)
      if (w > 0) width.value = w
    })
    ro.observe(wrap.value)
  }
  // 容器初始可能是 0 宽（对话框/折叠面板内），布局完成后再量一次。
  nextTick(measure)
})
onBeforeUnmount(() => ro && ro.disconnect())
function measure() {
  if (wrap.value && wrap.value.clientWidth > 0) width.value = wrap.value.clientWidth
}

const hasData = computed(() => props.labels.length > 0 && props.series.some((s) => finiteCount(s.data) > 0))
const visible = computed(() => props.series.filter((s) => !hidden.value.has(s.name)))
const hasRight = computed(() => visible.value.some((s) => s.axis === 'right'))
const pad = computed(() => ({ l: 46, r: hasRight.value ? 48 : 14, t: 12, b: 24 }))
const inner = computed(() => ({
  w: Math.max(1, width.value - pad.value.l - pad.value.r),
  h: Math.max(1, props.height - pad.value.t - pad.value.b)
}))
const singlePoint = computed(() => sampled.value.length === 1)

function finiteCount(arr) {
  if (!Array.isArray(arr)) return 0
  let n = 0
  for (const v of arr) if (Number.isFinite(v)) n++
  return n
}
function maxOf(axis) {
  let m = 0
  for (const s of visible.value) {
    if ((s.axis || 'left') !== axis) continue
    for (const v of s.data || []) if (Number.isFinite(v) && v > m) m = v
  }
  return m
}
const leftMax = computed(() => (props.yMax && props.yMax > 0 ? props.yMax : niceMax(maxOf('left'))))
const rightMax = computed(() => niceMax(maxOf('right')))

// 抽稀后保留的是**原始数据下标**，悬停读数才能取到真实样本
const sampled = computed(() => downsample(props.labels.length, props.maxPoints))

function xOf(k) {
  const n = sampled.value.length
  return n <= 1 ? pad.value.l + inner.value.w / 2 : pad.value.l + (k * inner.value.w) / (n - 1)
}
function yOf(v, max) {
  const x = Number.isFinite(v) ? Math.max(0, Math.min(v, max)) : 0
  return pad.value.t + inner.value.h * (1 - x / max)
}

const yTicks = computed(() =>
  [0, 0.25, 0.5, 0.75, 1].map((f) => ({
    v: f,
    y: yOf(f * leftMax.value, leftMax.value),
    label: axisLabel(f * leftMax.value, leftMax.value),
    labelR: hasRight.value ? axisLabel(f * rightMax.value, rightMax.value) : ''
  }))
)
const xTicks = computed(() => {
  const idx = sampled.value
  if (!idx.length) return []
  const count = Math.min(6, idx.length)
  const step = (idx.length - 1) / Math.max(1, count - 1)
  const out = []
  for (let i = 0; i < count; i++) {
    const k = Math.round(i * step)
    out.push({ k, x: xOf(k), label: props.labels[idx[k]] ?? '' })
  }
  return out
})

const drawn = computed(() =>
  visible.value.map((s) => {
    const max = s.axis === 'right' ? rightMax.value : leftMax.value
    return {
      ...s,
      max,
      segments: buildSegments(s.data || [], sampled.value, max, xOf, (v) => yOf(v, max))
    }
  })
)

const hoverX = computed(() => (hover.value < 0 ? 0 : xOf(hover.value)))
function dotY(s) {
  return yOf(s.data?.[sampled.value[hover.value]], s.max)
}
function fmtVal(s, v) {
  if (!Number.isFinite(v)) return '—'
  return s.fmt ? s.fmt(v) : v.toFixed(1)
}
const tip = computed(() => {
  if (hover.value < 0) return null
  const di = sampled.value[hover.value]
  return {
    label: props.labels[di] ?? '',
    values: drawn.value.map((s) => ({ name: s.name, color: s.color, text: fmtVal(s, s.data?.[di]) }))
  }
})
const tipStyle = computed(() => {
  const w = 150
  const left = hoverX.value + 14 + w > width.value ? Math.max(0, hoverX.value - w - 14) : hoverX.value + 14
  return { left: left + 'px', top: '14px' }
})

function onMove(e) {
  const rect = e.currentTarget.getBoundingClientRect()
  hover.value = hoverIndex(e.clientX - rect.left, pad.value.l, inner.value.w, sampled.value.length)
}
function toggle(name) {
  const next = new Set(hidden.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  // 不允许全部隐藏：全空图表比留一条线更让人困惑
  if (next.size >= props.series.length) return
  hidden.value = next
  hover.value = -1
}
</script>

<style scoped>
.mlc {
  position: relative;
  width: 100%;
}

.mlc-svg {
  display: block;
  cursor: crosshair;
}

.mlc-axis {
  font-size: 11px;
  fill: #909399;
}

.mlc-axis-r {
  fill: #b1b3b8;
}

.mlc-empty {
  padding: 40px 0;
  text-align: center;
}

.mlc-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 6px;
}

.mlc-lg {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: #606266;
  cursor: pointer;
  user-select: none;
}

.mlc-lg i {
  width: 10px;
  height: 3px;
  border-radius: 2px;
  display: inline-block;
}

.mlc-lg.off {
  opacity: 0.35;
  text-decoration: line-through;
}

.mlc-lg em {
  font-style: normal;
  font-size: 11px;
  color: #a8abb2;
  border: 1px solid #e4e7ed;
  border-radius: 3px;
  padding: 0 3px;
  line-height: 14px;
}

.mlc-tip {
  position: absolute;
  min-width: 130px;
  max-width: 150px;
  background: rgba(255, 255, 255, 0.97);
  border: 1px solid #e4e7ed;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.1);
  border-radius: 4px;
  padding: 6px 8px;
  font-size: 12px;
  pointer-events: none;
}

.mlc-tip-t {
  color: #909399;
  margin-bottom: 4px;
}

.mlc-tip-r {
  display: flex;
  align-items: center;
  gap: 5px;
  line-height: 18px;
}

.mlc-tip-r i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}

.mlc-tip-r span {
  color: #606266;
  flex: 1;
}

.mlc-tip-r b {
  color: #303133;
  font-variant-numeric: tabular-nums;
}
</style>
