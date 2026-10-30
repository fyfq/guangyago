<script setup lang="ts">
import { onMounted } from 'vue'
import PortalView from './views/PortalView.vue'
import LoginView from './views/LoginView.vue'
import SetupView from './views/SetupView.vue'
import AdminShell from './components/AdminShell.vue'
import ToastHost from './components/ToastHost.vue'
import ConfirmHost from './components/ConfirmHost.vue'
import { loadBranding } from './lib/branding'

// 没有引入路由库：只有三个入口，按 pathname 直接分流即可
const pathname = window.location.pathname.replace(/\/+$/, '') || '/'
const isSetup = pathname === '/setup'
const isLogin = pathname === '/login'
const isAdmin = pathname === '/admin' || pathname.startsWith('/admin/')
// 服务端已对未知路径返回 404，这里同步给出页面不存在的提示
const isUnknown = pathname !== '/' && !isSetup && !isLogin && !isAdmin

onMounted(loadBranding)
</script>

<template>
  <SetupView v-if="isSetup" />
  <LoginView v-else-if="isLogin" />
  <AdminShell v-else-if="isAdmin" />
  <main v-else-if="isUnknown" class="flex min-h-screen items-center justify-center p-6">
    <div class="text-center">
      <h1 class="text-xl font-semibold text-slate-800">页面不存在</h1>
      <p class="mt-2 text-sm text-slate-500">你访问的地址可能已失效，或资源已被移除。</p>
      <a href="/" class="mt-5 inline-block rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white">
        返回资源库
      </a>
    </div>
  </main>
  <PortalView v-else />
  <ToastHost />
  <ConfirmHost />
</template>
