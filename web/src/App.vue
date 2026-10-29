<script setup lang="ts">
import PortalView from './views/PortalView.vue'
import LoginView from './views/LoginView.vue'
import AdminShell from './components/AdminShell.vue'
import ToastHost from './components/ToastHost.vue'
import ConfirmHost from './components/ConfirmHost.vue'

// 没有引入路由库：只有三个入口，按 pathname 直接分流即可
const pathname = window.location.pathname.replace(/\/+$/, '') || '/'
const isLogin = pathname === '/login'
const isAdmin = pathname === '/admin' || pathname.startsWith('/admin/')
</script>

<template>
  <LoginView v-if="isLogin" />
  <AdminShell v-else-if="isAdmin" />
  <PortalView v-else />
  <ToastHost />
  <ConfirmHost />
</template>
