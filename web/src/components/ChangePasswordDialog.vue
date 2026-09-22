<template>
  <el-dialog :model-value="modelValue" title="修改密码" width="420px" @update:model-value="v => emit('update:modelValue', v)">
    <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
      <el-form-item label="原密码" prop="old_password">
        <el-input v-model="form.old_password" type="password" show-password autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="新密码" prop="new_password">
        <el-input v-model="form.new_password" type="password" show-password autocomplete="new-password" />
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirm">
        <el-input v-model="form.confirm" type="password" show-password autocomplete="new-password" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="close">取消</el-button>
      <el-button type="primary" :loading="saving" @click="submit">确认修改</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { post, showErr } from '../api/http'

const props = defineProps({ modelValue: Boolean })
const emit = defineEmits(['update:modelValue', 'success'])

const formRef = ref(null)
const form = ref({ old_password: '', new_password: '', confirm: '' })
const saving = ref(false)

const validateConfirm = (rule, value, cb) => {
  if (value !== form.value.new_password) cb(new Error('两次输入的新密码不一致'))
  else cb()
}
const rules = {
  old_password: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  new_password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, max: 128, message: '新密码长度需为 8-128', trigger: 'blur' }
  ],
  confirm: [{ required: true, validator: validateConfirm, trigger: 'blur' }]
}

watch(
  () => props.modelValue,
  (v) => {
    if (v) form.value = { old_password: '', new_password: '', confirm: '' }
  }
)

function close() {
  emit('update:modelValue', false)
}
async function submit() {
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  saving.value = true
  try {
    await post('/api/auth/change-password', {
      old_password: form.value.old_password,
      new_password: form.value.new_password
    })
    ElMessage.success('密码修改成功')
    emit('success')
    close()
  } catch (e) {
    showErr(e, '密码修改失败')
  } finally {
    saving.value = false
  }
}
</script>
