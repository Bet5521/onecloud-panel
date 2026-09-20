<template>
  <el-dialog :model-value="modelValue" title="超级验证码" width="420px"
    :close-on-click-modal="false" @update:model-value="$emit('update:modelValue', $event)">
    <el-alert type="warning" :closable="false" show-icon style="margin-bottom: 14px">
      超级验证码可用于登录页自助重置密码，请立即复制保存。
      <strong>关闭后将不再显示</strong>。
    </el-alert>
    <div class="code-box">
      <span class="code-text">{{ code }}</span>
      <el-button type="primary" link :icon="CopyDocument" @click="copy">复制</el-button>
    </div>
    <template #footer>
      <el-button type="primary" @click="$emit('update:modelValue', false)">我已保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ElMessage } from 'element-plus'
import { CopyDocument } from '@element-plus/icons-vue'

const props = defineProps({
  modelValue: Boolean,
  code: { type: String, default: '' }
})
defineEmits(['update:modelValue'])

async function copy() {
  try {
    await navigator.clipboard.writeText(props.code)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.warning('复制失败，请手动选择复制')
  }
}
</script>

<style scoped>
.code-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #f5f7fa;
  border: 1px dashed #dcdfe6;
  border-radius: 6px;
  padding: 12px 16px;
}
.code-text {
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 20px;
  font-weight: 600;
  letter-spacing: 3px;
  user-select: all;
}
</style>
