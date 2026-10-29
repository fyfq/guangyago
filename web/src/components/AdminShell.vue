<script setup lang="ts">
import { computed, onMounted, ref, watch, type Component } from 'vue'
import {
  Cloud,
  Globe,
  Library,
  Settings as SettingsIcon,
  UsersRound,
  LogOut,
  Loader2,
  AlertCircle,
} from 'lucide-vue-next'
import LibraryView from '../views/LibraryView.vue'
import ImportView from '../views/ImportView.vue'
import ScrapeView from '../views/ScrapeView.vue'
import PushView from '../views/PushView.vue'
import SettingsView from '../views/SettingsView.vue'
import UsersView from '../views/UsersView.vue'
import SEOView from '../views/SEOView.vue'
import Card from './ui/Card.vue'
import Button from './ui/Button.vue'
import { api, ApiError, type User } from '../lib/api'
import { branding } from '../lib/branding'

type Tab = 'import' | 'scrape' | 'library' | 'push' | 'settings' | 'users' | 'seo'

const tab = ref<Tab>('library')
const stats = ref<Record<string, number>>({})
const initialStatus = ref('')
// 每次跳转换一个 key，保证重复点击同一入口也会重新挂载并刷新列表、
// 且能把筛选重置回该页的默认状态（例如从「未匹配」回到「待刮削」）。
const navSeq = ref(0)
const me = ref<User | null>(null)
const checking = ref(true)
const authError = ref('')

// 前三个是流水线步骤，资源库、设置与用户管理不占步骤号
const allTabs: { key: Tab; label: string; step?: number; icon?: Component; adminOnly?: boolean }[] = [
  { key: 'import', label: '录入解析', step: 1 },
  { key: 'scrape', label: '刮削队列', step: 2 },
  { key: 'push', label: '推送频道', step: 3 },
  { key: 'library', label: '资源库', icon: Library },
  { key: 'settings', label: '设置', icon: SettingsIcon, adminOnly: true },
  { key: 'users', label: '用户管理', icon: UsersRound, adminOnly: true },
  { key: 'seo', label: 'SEO', icon: Globe, adminOnly: true },
]

const tabs = computed(() => allTabs.filter((t) => !t.adminOnly || me.value?.role === 'admin'))
const isAdmin = computed(() => me.value?.role === 'admin')

function loginURL() {
  return `/login?next=${encodeURIComponent(location.pathname + location.search)}`
}

async function checkAuth() {
  checking.value = true
  authError.value = ''
  try {
    me.value = await api.me()
    await loadStats()
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      location.replace(loginURL())
      return
    }
    authError.value = e instanceof Error ? e.message : '登录状态校验失败'
  } finally {
    checking.value = false
  }
}

// 概览卡片：未刮削的跳刮削队列，待推送的跳推送频道，已推送的跳资源库
const overview = [
  {
    key: 'unresolved',
    label: '待刮削',
    target: 'scrape',
    status: 'pending_set',
    sum: ['pending', 'review', 'scraping', 'unmatched', 'failed'],
  },
  { key: 'unmatched', label: '未匹配', target: 'scrape', status: 'unmatched', sum: ['unmatched'] },
  { key: 'scraped', label: '待推送', target: 'push', status: '', sum: ['scraped'] },
  { key: 'pushed', label: '已推送', target: 'library', status: 'pushed', sum: ['pushed'] },
] as const

function count(o: { sum: readonly string[] }) {
  return o.sum.reduce((n, k) => n + (stats.value[k] ?? 0), 0)
}

async function loadStats() {
  try {
    stats.value = await api.stats()
  } catch {
    stats.value = {}
  }
}

function go(next: string, status = '') {
  initialStatus.value = status
  tab.value = next as Tab
  navSeq.value++
  loadStats()
}

function onImported() {
  loadStats()
}

async function logout() {
  try {
    await api.logout()
  } finally {
    location.replace('/login')
  }
}

function syncTitle() {
  document.title = `${branding.site_name} · 后台`
}

onMounted(() => {
  syncTitle()
  checkAuth()
})

// 品牌信息异步到位后补一次标题
watch(() => branding.site_name, syncTitle)
</script>

<template>
  <div v-if="checking" class="flex min-h-screen items-center justify-center text-[var(--color-muted)]">
    <Loader2 class="h-5 w-5 animate-spin" />
  </div>

  <div v-else-if="authError" class="flex min-h-screen items-center justify-center px-6">
    <div class="flex max-w-md flex-col items-center gap-3 text-center">
      <AlertCircle class="h-6 w-6 text-[var(--color-bad)]" />
      <p class="text-[13px] text-[var(--color-bad)]">{{ authError }}</p>
      <Button variant="outline" size="sm" @click="checkAuth">重试</Button>
    </div>
  </div>

  <div v-else class="min-h-screen">
    <header class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto max-w-7xl px-6 py-4">
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-3">
            <!-- branding.logo_url 已在 lib/branding 里限定成同源 /branding/ 路径 -->
            <img
              v-if="branding.logo_url"
              :src="branding.logo_url"
              :alt="branding.site_name"
              class="h-9 w-9 rounded-lg object-contain"
            />
            <div v-else class="flex h-9 w-9 items-center justify-center rounded-lg bg-[var(--color-brand)]">
              <Cloud class="h-5 w-5 text-white" />
            </div>
            <div>
              <h1 class="text-[15px] font-medium">{{ branding.site_name }}</h1>
              <p class="text-[13px] text-[var(--color-muted)]">后台 · 录入 → 刮削 → 推送频道</p>
            </div>
          </div>

          <div class="flex items-center gap-2 text-[13px]">
            <span class="hidden text-[var(--color-muted)] sm:inline">
              {{ me?.display_name || me?.username }}
            </span>
            <span
              class="rounded-full bg-[var(--color-surface-sunken)] px-2 py-0.5 text-xs text-[var(--color-muted)]"
            >
              {{ isAdmin ? '管理员' : '运营' }}
            </span>
            <a href="/" target="_blank" rel="noopener noreferrer" class="text-[var(--color-brand)] hover:underline">前台</a>
            <Button variant="ghost" size="sm" @click="logout">
              <LogOut class="h-4 w-4" />
              退出
            </Button>
          </div>
        </div>

        <nav class="mt-4 flex flex-wrap gap-1">
          <button
            v-for="t in tabs"
            :key="t.key"
            class="flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm transition-colors"
            :class="
              tab === t.key
                ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)] font-medium'
                : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]'
            "
            @click="go(t.key)"
          >
            <!-- 前三个标签带步骤号，其余只显示图标 -->
            <span
              v-if="t.step"
              class="flex h-4 w-4 items-center justify-center rounded-full border text-[10px] leading-none"
              :class="tab === t.key ? 'border-[var(--color-brand)]' : 'border-[var(--color-line-strong)]'"
            >
              {{ t.step }}
            </span>
            <component :is="t.icon" v-else class="h-4 w-4" />
            {{ t.label }}
          </button>
        </nav>
      </div>
    </header>

    <main class="mx-auto max-w-7xl px-6 py-6">
      <Card v-if="tab !== 'users'" class="mb-5">
        <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <button
            v-for="o in overview"
            :key="o.key"
            class="rounded-lg bg-[var(--color-surface-sunken)] p-3 text-left transition-colors hover:bg-[var(--color-surface-hover)]"
            :title="
              o.target === 'scrape'
                ? `到刮削队列处理${o.label}的记录`
                : o.target === 'push'
                  ? `到推送频道发${o.label}的记录`
                  : `在资源库查看${o.label}的记录`
            "
            @click="go(o.target, o.status)"
          >
            <p class="text-[13px] text-[var(--color-muted)]">{{ o.label }}</p>
            <p class="text-2xl font-medium">{{ count(o) }}</p>
          </button>
        </div>
      </Card>

      <ImportView v-if="tab === 'import'" @imported="onImported" @goto="go" />
      <ScrapeView
        v-else-if="tab === 'scrape'"
        :key="navSeq"
        :initial-status="initialStatus"
        @changed="loadStats"
        @goto="go"
      />
      <LibraryView
        v-else-if="tab === 'library'"
        :key="navSeq"
        :initial-status="initialStatus"
        @changed="loadStats"
        @goto="go"
      />
      <PushView
        v-else-if="tab === 'push'"
        :key="navSeq"
        :initial-status="initialStatus"
        @changed="loadStats"
        @goto="go"
      />
      <SettingsView v-else-if="tab === 'settings' && isAdmin" />
      <SEOView v-else-if="tab === 'seo' && isAdmin" />
      <UsersView v-else-if="tab === 'users' && me" :me="me" />
    </main>
  </div>
</template>
