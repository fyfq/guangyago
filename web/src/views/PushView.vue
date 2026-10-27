<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { Send, Loader2, CheckCircle2, AlertCircle, KeyRound, Library, RotateCcw } from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import { api, statusMeta, mediaMeta, type TaskProgress, type Settings, type Link } from '../lib/api'

const pending = ref<Link[]>([])
const readyCount = ref(0)
const failedCount = ref(0)
const selectedId = ref<number | null>(null)
const progress = ref<TaskProgress | null>(null)
const settings = ref<Settings | null>(null)
const pushing = ref(false)
const error = ref('')
const captionHtml = ref('')
const captionPlain = ref('')
let timer: number | undefined

const emit = defineEmits<{ changed: []; goto: [tab: string] }>()

const current = computed(() => pending.value.find((l) => l.id === selectedId.value) ?? pending.value[0])

const percent = computed(() => {
  const p = progress.value
  if (!p || !p.total) return 0
  return Math.round(((p.done + p.failed) / p.total) * 100)
})

// 卡片文案由后端生成，保证预览与实际推送完全一致
async function loadCaption(id: number) {
  try {
    const res = await api.caption(id)
    captionHtml.value = res.html
    captionPlain.value = res.text
  } catch {
    captionHtml.value = ''
    captionPlain.value = ''
  }
}

watch(selectedId, (id) => {
  if (id) loadCaption(id)
})

watch(current, (l) => {
  if (l) loadCaption(l.id)
})

async function load() {
  try {
    const [candidates, ready, failed] = await Promise.all([
      api.list({ status: 'scraped,push_failed', limit: 200 }),
      api.list({ status: 'scraped', limit: 1 }),
      api.list({ status: 'push_failed', limit: 1 }),
    ])
    pending.value = candidates.items
    readyCount.value = ready.total
    failedCount.value = failed.total
    if (!selectedId.value && candidates.items.length) {
      selectedId.value = (candidates.items.find((l) => l.status === 'scraped') ?? candidates.items[0]).id
      await loadCaption(selectedId.value)
    }
  } catch {
    pending.value = []
  }
}

async function loadProgress() {
  try {
    progress.value = await api.tasks()
  } catch {
    progress.value = null
  }
}

function poll() {
  window.clearInterval(timer)
  timer = window.setInterval(async () => {
    await loadProgress()
    if (progress.value && !progress.value.running) {
      window.clearInterval(timer)
      pushing.value = false
      await load()
      emit('changed')
    }
  }, 1000)
}

async function run(fn: () => Promise<unknown>) {
  pushing.value = true
  error.value = ''
  try {
    await fn()
    await loadProgress()
    poll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '推送启动失败'
    pushing.value = false
  }
}

const pushReady = () => run(() => api.push([], true))
const retryFailed = () => run(() => api.push([], false, ['push_failed']))

function copyCaption() {
  if (captionPlain.value) navigator.clipboard?.writeText(captionPlain.value)
}

onMounted(async () => {
  await load()
  await loadProgress()
  if (progress.value?.running) {
    pushing.value = true
    poll()
  }
  try {
    settings.value = await api.settings()
  } catch {
    settings.value = null
  }
})

onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <div class="grid gap-5 lg:grid-cols-[1fr_1fr]">
    <Card title="推送频道" desc="刮削完成但还没发到频道的记录都在这里">
      <div
        v-if="settings && !settings.telegram_configured"
        class="mb-4 flex items-start gap-2 rounded-lg bg-[var(--color-warn-soft)] px-3 py-2.5 text-[13px] text-[var(--color-warn)]"
      >
        <KeyRound class="mt-0.5 h-4 w-4 shrink-0" />
        <span>还没有配置 Bot Token 或频道 ID，先去「设置」页填写。</span>
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div class="rounded-lg border border-[var(--color-line)] p-3">
          <p class="text-[13px] text-[var(--color-muted)]">待推送</p>
          <p class="text-2xl font-medium">{{ readyCount }}</p>
        </div>
        <div class="rounded-lg border border-[var(--color-line)] p-3">
          <p class="text-[13px] text-[var(--color-muted)]">推送失败</p>
          <p
            class="text-2xl font-medium"
            :class="failedCount ? 'text-[var(--color-warn)]' : ''"
          >
            {{ failedCount }}
          </p>
        </div>
      </div>

      <div class="mt-4 flex flex-wrap items-center gap-2">
        <Button :disabled="pushing || !readyCount" @click="pushReady">
          <Loader2 v-if="pushing" class="h-4 w-4 animate-spin" />
          <Send v-else class="h-4 w-4" />
          推送全部待推送（{{ readyCount }}）
        </Button>
        <Button variant="outline" :disabled="pushing || !failedCount" @click="retryFailed">
          <RotateCcw class="h-4 w-4" />
          重试推送失败（{{ failedCount }}）
        </Button>
      </div>

      <p v-if="error" class="mt-3 text-[13px] text-[var(--color-bad)]">{{ error }}</p>

      <div v-if="progress && progress.total" class="mt-5">
        <div class="flex items-center justify-between text-[13px]">
          <span class="truncate text-[var(--color-muted)]">{{ progress.current || '准备中' }}</span>
          <span class="font-medium">{{ progress.done + progress.failed }} / {{ progress.total }}</span>
        </div>
        <div class="mt-2 h-2 overflow-hidden rounded-full bg-stone-100">
          <div
            class="h-full rounded-full bg-[var(--color-brand)] transition-all duration-300"
            :style="{ width: percent + '%' }"
          />
        </div>
        <div class="mt-3 flex items-center gap-4 text-[13px]">
          <span class="flex items-center gap-1.5 text-[var(--color-ok)]">
            <CheckCircle2 class="h-4 w-4" />
            成功 {{ progress.done }}
          </span>
          <span class="flex items-center gap-1.5 text-[var(--color-bad)]">
            <AlertCircle class="h-4 w-4" />
            失败 {{ progress.failed }}
          </span>
          <Badge v-if="progress.running" class="bg-[var(--color-brand-soft)] text-[var(--color-brand)]">
            进行中
          </Badge>
        </div>
        <p v-if="progress.error" class="mt-2 text-[13px] text-[var(--color-bad)]">
          最近错误：{{ progress.error }}
        </p>
      </div>

      <div v-if="pending.length" class="mt-5">
        <p class="mb-2 text-[13px] text-[var(--color-muted)]">选择一条预览卡片样式</p>
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="l in pending.slice(0, 8)"
            :key="l.id"
            class="flex max-w-[180px] items-center gap-1 truncate rounded-full px-3 py-1 text-[13px] transition-colors"
            :class="
              selectedId === l.id
                ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
                : 'text-[var(--color-muted)] hover:bg-stone-100'
            "
            @click="selectedId = l.id"
          >
            <span class="truncate">{{ l.title }}</span>
            <span v-if="l.status === 'push_failed'" class="text-[var(--color-warn)]">· 失败</span>
          </button>
        </div>
        <button
          class="mt-3 flex items-center gap-1 text-[13px] text-[var(--color-brand)] hover:underline"
          @click="emit('goto', 'library')"
        >
          <Library class="h-3.5 w-3.5" />
          推送完成的去资源库回看
        </button>
      </div>
    </Card>

    <Card title="卡片预览" desc="实际推送到频道的效果">
      <div v-if="!current" class="flex h-40 items-center justify-center text-[13px] text-stone-400">
        暂无待推送记录
      </div>
      <div
        v-else
        class="overflow-hidden rounded-xl border border-[var(--color-line)] bg-[var(--color-surface)]"
      >
        <div
          class="flex h-[300px] items-center justify-center bg-stone-100 bg-cover bg-center"
          :style="current.poster_url ? { backgroundImage: `url(${current.poster_url})` } : {}"
        >
          <span v-if="!current.poster_url" class="text-[13px] text-stone-400">海报</span>
        </div>
        <div class="space-y-1 p-3 text-[13px] leading-relaxed">
          <div class="mb-1 flex flex-wrap items-center gap-2">
            <Badge :class="statusMeta[current.status]?.cls">
              {{ statusMeta[current.status]?.label ?? current.status }}
            </Badge>
            <Badge v-if="mediaMeta[current.media_type]" :class="mediaMeta[current.media_type].cls">
              {{ mediaMeta[current.media_type].label }}
            </Badge>
            <span v-if="current.error" class="truncate text-xs text-[var(--color-bad)]">
              {{ current.error }}
            </span>
          </div>
          <!-- 与 Telegram 实际收到的一致：标题 / 年份 / 简介 / 链接 / 提取码 / TMDB -->
          <div v-if="captionHtml" class="whitespace-pre-line" v-html="captionHtml" />
          <template v-else>
            <p class="font-medium">{{ current.title }}</p>
            <p class="text-xs text-stone-500">{{ current.overview }}</p>
          </template>
        </div>
        <div class="flex items-center justify-between border-t border-[var(--color-line)] px-3 py-2">
          <span class="text-xs text-stone-400">{{ captionPlain.length }} 字</span>
          <Button variant="ghost" size="sm" :disabled="!captionPlain" @click="copyCaption">复制文案</Button>
        </div>
      </div>
    </Card>
  </div>
</template>
