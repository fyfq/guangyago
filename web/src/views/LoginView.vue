<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Cloud, Loader2, AlertCircle } from 'lucide-vue-next'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import { api } from '../lib/api'

const username = ref('')
const password = ref('')
const submitting = ref(false)
const error = ref('')

function nextPath() {
  const next = new URLSearchParams(location.search).get('next') || ''
  return next.startsWith('/admin') ? next : '/admin'
}

async function submit() {
  if (submitting.value) return
  error.value = ''
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  submitting.value = true
  try {
    await api.login(username.value.trim(), password.value)
    location.replace(nextPath())
  } catch (e) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  document.title = '登录 · 光鸭 GO'
  // 已经登录过就直接进后台
  try {
    await api.me()
    location.replace(nextPath())
  } catch {
    // 未登录，停留在登录页
  }
})
</script>

<template>
  <div class="flex min-h-screen items-center justify-center px-6 py-10">
    <div class="w-full max-w-sm">
      <div class="mb-6 flex flex-col items-center text-center">
        <div class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--color-brand)]">
          <Cloud class="h-6 w-6 text-white" />
        </div>
        <h1 class="mt-3 text-xl">光鸭 GO</h1>
        <p class="mt-1 text-[13px] text-[var(--color-muted)]">登录后台管理</p>
      </div>

      <form
        class="rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] p-5 shadow-[0_1px_3px_rgba(20,20,20,0.05)]"
        @submit.prevent="submit"
      >
        <label class="mb-1.5 block text-[13px] font-medium" for="login-username">用户名</label>
        <Input id="login-username" v-model="username" placeholder="admin" autocomplete="username" />

        <label class="mb-1.5 mt-4 block text-[13px] font-medium" for="login-password">密码</label>
        <Input
          id="login-password"
          v-model="password"
          type="password"
          placeholder="请输入密码"
          autocomplete="current-password"
        />

        <div
          v-if="error"
          class="mt-4 flex items-start gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2 text-[13px] text-[var(--color-bad)]"
        >
          <AlertCircle class="mt-0.5 h-4 w-4 shrink-0" />
          <span class="min-w-0 flex-1">{{ error }}</span>
        </div>

        <Button class="mt-5 w-full" type="submit" :disabled="submitting">
          <Loader2 v-if="submitting" class="h-4 w-4 animate-spin" />
          登录
        </Button>
      </form>

      <p class="mt-4 text-center text-xs text-[var(--color-faint)]">
        账号由管理员在后台「用户管理」中创建
      </p>
    </div>
  </div>
</template>
