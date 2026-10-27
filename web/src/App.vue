<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  Cloud,
  Library,
  Plus,
  Sparkles,
  Send,
  Settings as SettingsIcon,
} from 'lucide-vue-next'
import LibraryView from './views/LibraryView.vue'
import ImportView from './views/ImportView.vue'
import ScrapeView from './views/ScrapeView.vue'
import PushView from './views/PushView.vue'
import SettingsView from './views/SettingsView.vue'
import Card from './components/ui/Card.vue'
import { api } from './lib/api'

type Tab = 'import' | 'scrape' | 'library' | 'push' | 'settings'
const tab = ref<Tab>('library')
const stats = ref<Record<string, number>>({})
const initialStatus = ref('')

// 导航按操作顺序排列：录入 → 刮削 → 推送 → 资源库（回看与归档）
const tabs = [
  { key: 'import', label: '录入解析', icon: Plus },
  { key: 'scrape', label: '刮削队列', icon: Sparkles },
  { key: 'push', label: '推送频道', icon: Send },
  { key: 'library', label: '资源库', icon: Library },
  { key: 'settings', label: '设置', icon: SettingsIcon },
] as const

const steps = [
  { key: 'import', label: '录入' },
  { key: 'scrape', label: '刮削' },
  { key: 'push', label: '推送' },
]

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

const stepIndex = computed(() => steps.findIndex((s) => s.key === tab.value))

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
  loadStats()
}

function onImported() {
  loadStats()
}

onMounted(loadStats)
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b border-[var(--color-line)] bg-[var(--color-surface)]">
      <div class="mx-auto max-w-7xl px-6 py-4">
        <div class="flex items-center gap-3">
          <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-[var(--color-brand)]">
            <Cloud class="h-5 w-5 text-white" />
          </div>
          <div>
            <h1 class="text-[15px] font-medium">光鸭 GO</h1>
            <p class="text-[13px] text-[var(--color-muted)]">录入 → 刮削 → 推送频道 → 资源库回看</p>
          </div>

          <div class="ml-auto hidden items-center gap-2 text-[13px] sm:flex">
            <template v-for="(s, i) in steps" :key="s.key">
              <button
                class="flex items-center gap-1.5 rounded-full px-2.5 py-1 transition-colors"
                :class="
                  stepIndex === i
                    ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)] font-medium'
                    : 'text-stone-400 hover:text-[var(--color-ink)]'
                "
                @click="go(s.key)"
              >
                <span
                  class="flex h-5 w-5 items-center justify-center rounded-full border text-[11px]"
                  :class="stepIndex === i ? 'border-[var(--color-brand)]' : 'border-stone-300'"
                >
                  {{ i + 1 }}
                </span>
                {{ s.label }}
              </button>
              <span v-if="i < steps.length - 1" class="text-stone-300">—</span>
            </template>
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
                : 'text-[var(--color-muted)] hover:bg-stone-100'
            "
            @click="go(t.key)"
          >
            <component :is="t.icon" class="h-4 w-4" />
            {{ t.label }}
          </button>
        </nav>
      </div>
    </header>

    <main class="mx-auto max-w-7xl px-6 py-6">
      <Card class="mb-5">
        <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <button
            v-for="o in overview"
            :key="o.key"
            class="rounded-lg bg-stone-50 p-3 text-left transition-colors hover:bg-stone-100"
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
        :initial-status="initialStatus"
        @changed="loadStats"
        @goto="go"
      />
      <LibraryView
        v-else-if="tab === 'library'"
        :initial-status="initialStatus"
        @changed="loadStats"
        @goto="go"
      />
      <PushView v-else-if="tab === 'push'" @changed="loadStats" @goto="go" />
      <SettingsView v-else />
    </main>
  </div>
</template>
