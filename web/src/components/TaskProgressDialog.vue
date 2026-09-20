<template>
  <el-dialog :model-value="true" :title="title" width="760px"
    :close-on-click-modal="false" @close="onClose">
    <div class="task-head">
      <el-tag :type="statusTag">{{ statusText }}</el-tag>
      <span v-if="task?.error" class="task-error">{{ task.error }}</span>
    </div>
    <pre class="task-output">{{ task?.output || '等待任务输出…' }}</pre>
    <template #footer>
      <el-button @click="onClose">关闭</el-button>
      <el-button v-if="finished" type="primary" @click="$emit('finished'); onClose()">
        完成（刷新页面）
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, onUnmounted } from 'vue'
import { get } from '../api/http'

const props = defineProps({
  taskId: { type: Number, required: true },
  title: { type: String, default: '任务执行中' },
  endpoint: { type: String, default: '/api/tasks/' }
})
const emit = defineEmits(['close', 'finished'])

const task = ref(null)
let timer = null

const finished = computed(() => task.value && (task.value.status === 'success' || task.value.status === 'failure'))
const statusText = computed(() => {
  switch (task.value?.status) {
    case 'queued': return '排队中'
    case 'running': return '执行中'
    case 'success': return '执行成功'
    case 'failure': return '执行失败'
    default: return '加载中'
  }
})
const statusTag = computed(() => ({
  queued: 'info', running: 'warning', success: 'success', failure: 'danger'
}[task.value?.status] ?? 'info'))

async function poll() {
  try {
    task.value = await get(props.endpoint + props.taskId)
  } catch {
    /* 瞬时失败忽略 */
  }
  if (!finished.value) {
    timer = setTimeout(poll, 1500)
  }
}
poll()

function onClose() {
  clearTimeout(timer)
  emit('close')
}
onUnmounted(() => clearTimeout(timer))
</script>

<style scoped>
.task-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.task-error {
  color: #f56c6c;
  font-size: 13px;
}
.task-output {
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
  max-height: 420px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
</style>
