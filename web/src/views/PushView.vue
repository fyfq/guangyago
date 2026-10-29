<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  Send,
  Loader2,
  KeyRound,
  Library,
  RotateCcw,
  AlertCircle,
  Search,
  Inbox,
  Plus,
} from 'lucide-vue-next'
import Card from '../components/ui/Card.vue'
import Button from '../components/ui/Button.vue'
import Badge from '../components/ui/Badge.vue'
import Input from '../components/ui/Input.vue'
import TaskProgressBar from '../components/TaskProgress.vue'
import { api, statusMeta, mediaMeta, type TaskProgress, type Settings, type Link } from '../lib/api'
import { toast } from '../lib/toast'
import { confirmAction } from '../lib/confirm'

// 待推送页要看的全部状态：刮削完成待推送 + 推送失败待重试
const PUSH_SET = ['scraped', 'push_failed']
const pageSize = 20

const items = ref<Link[]>([])
const total = ref(0)
const loading = ref(false)
const loadError = ref('')
const keyword = ref('')
const status = ref('push_set')
const page = ref(0)
const selected = ref<number[]>([])
const selectedId = ref<number | null>(null)
const readyCount = ref(0)
const failedCount = ref(0)
const progress = ref<TaskProgress | null>(null)
const settings = ref<Settings | null>(null)
const pushing = ref(false)
const acting = ref<number | null>(null)
const captionHtml = ref('')
const captionPlain = ref('')
let timer: number | undefined
let loadTimer: number | undefined

const emit = defineEmits<{ changed: []; goto: [tab: string] }>()

/** 从概览卡片跳进来时带上的状态筛选 */
const props = defineProps<{ initialStatus?: string }>()

const filters = [
  { value: 'push_set', label: '全部待推送' },
  { value: 'scraped', label: '待推送' },
  { value: 'push_failed', label: '推送失败' },
]

const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const allChecked = computed(() => items.value.length > 0 && selected.value.length === items.value.length)
const running = computed(() => !!progress.value?.running)
const current = computed(
  () => items.value.find((l) => l.id === selectedId.value) ?? items.value[0] ?? null,
)

const emptyText = computed(() => {
  if (keyword.value.trim()) return '没有匹配的记录'
  if (status.value === 'push_set') return '没有待推送的记录'
  return status.value === 'push_failed' ? '没有推送失败的记录' : '没有待推送的记录'
})

/** 当前筛选对应的后端状态列表 */
function currentStatuses(): string[] {
  return status.value === 'push_set' ? PUSH_SET : [status.value]
}

// 输入、切页、切筛选可能同时触发请求，只让最后一次结果落地
let loadSeq = 0

async function loadList() {
  const seq = ++loadSeq
  loading.value = true
  loadError.value = ''
  try {
    const [res, stats] = await Promise.all([
      api.list({
        status: status.value,
        q: keyword.value,
        limit: pageSize,
        offset: page.value * pageSize,
      }),
      api.stats(),
    ])
    if (seq !== loadSeq) return
    items.value = res.items
    total.value = res.total
    readyCount.value = stats.scraped ?? 0
    failedCount.value = stats.push_failed ?? 0
    selected.value = []
    // 当前预览项不在新列表里时，优先落到第一条待推送
    if (!res.items.some((l) => l.id === selectedId.value)) {
      selectedId.value =
        (res.items.find((l) => l.status === 'scraped') ?? res.items[0])?.id ?? null
    }
  } catch (e) {
    if (seq !== loadSeq) return
    loadError.value = e instanceof Error ? e.message : '加载失败'
    items.value = []
    total.value = 0
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function scheduleLoad(delay = 150) {
  window.clearTimeout(loadTimer)
  loadTimer = window.setTimeout(loadList, delay)
}

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

// 选中项与列表刷新都可能换上不同的卡片，统一盯住当前条目的 ID
watch(
  () => current.value?.id,
  (id) => {
    if (id) loadCaption(id)
  },
)

async function loadProgress() {
  try {
    progress.value = await api.tasks('push')
  } catch {
    progress.value = null
  }
}

function poll() {
  window.clearInterval(timer)
  timer = window.setInterval(async () => {
    await loadProgress()
    if (!progress.value?.running) {
      window.clearInterval(timer)
      pushing.value = false
      await loadList()
      emit('changed')
    }
  }, 1000)
}

async function run(action: () => Promise<{ queued: number }>) {
  pushing.value = true
  try {
    const res = await action()
    toast.success(`已加入推送队列 ${res.queued} 条`)
    await loadProgress()
    poll()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '推送启动失败')
    pushing.value = false
  }
}

async function pushFiltered() {
  const ok = await confirmAction({
    title: `推送当前筛选的 ${total.value} 条到频道？`,
    message: '推送后消息会立即出现在 Telegram 频道，撤回需要手动操作。',
    confirmText: '开始推送',
  })
  if (ok) run(() => api.push([], false, currentStatuses()))
}

async function pushSelected() {
  const ok = await confirmAction({
    title: `推送选中的 ${selected.value.length} 条到频道？`,
    message: '推送后消息会立即出现在 Telegram 频道，撤回需要手动操作。',
    confirmText: '开始推送',
  })
  if (ok) run(() => api.push(selected.value.slice()))
}

async function retryFailed() {
  const ok = await confirmAction({
    title: `重试 ${failedCount.value} 条推送失败的记录？`,
    message: '会重新发送这些卡片到频道，之前失败的消息不会自动删除。',
    confirmText: '重试推送',
  })
  if (ok) run(() => api.push([], false, ['push_failed']))
}

async function pushOneItem(l: Link) {
  acting.value = l.id
  try {
    await api.pushOne(l.id)
    toast.info('已加入推送队列')
    await loadProgress()
    poll()
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '推送启动失败')
  } finally {
    acting.value = null
  }
}

async function copyCaption() {
  if (!captionPlain.value) return
  try {
    await navigator.clipboard.writeText(captionPlain.value)
    toast.success('文案已复制')
  } catch {
    toast.error('复制失败，浏览器可能未授权剪贴板')
  }
}

async function stopAll() {
  try {
    await api.cancelTasks('push')
    toast.info('已停止排队中的推送任务')
    await loadProgress()
    await loadList()
    emit('changed')
  } catch (e) {
    toast.error(e instanceof Error ? e.message : '停止失败')
  }
}

function toggleAll() {
  selected.value = allChecked.value ? [] : items.value.map((i) => i.id)
}

function toggleSelect(id: number) {
  selected.value = selected.value.includes(id)
    ? selected.value.filter((x) => x !== id)
    : [...selected.value, id]
}

function go(delta: number) {
  const next = page.value + delta
  if (next < 0 || next >= pageCount.value) return
  page.value = next
  loadList()
}

watch(keyword, () => {
  page.value = 0
  scheduleLoad(300)
})
watch(status, () => {
  page.value = 0
  scheduleLoad()
})

watch(
  () => props.initialStatus,
  (v) => {
    if (!v) return
    status.value = v
    page.value = 0
    scheduleLoad()
  },
)

onMounted(async () => {
  if (props.initialStatus) status.value = props.initialStatus
  await loadProgress()
  await loadList()
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

onUnmounted(() => {
  window.clearInterval(timer)
  window.clearTimeout(loadTimer)
})
</script>

<template>
  <div class="grid gap-5 lg:grid-cols-[1.1fr_1fr] lg:items-start">
    <div class="space-y-5">
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
            <p class="text-2xl font-medium" :class="failedCount ? 'text-[var(--color-warn)]' : ''">
              {{ failedCount }}
            </p>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap items-center gap-2">
          <Button :disabled="pushing || running || !total" @click="pushFiltered">
            <Loader2 v-if="pushing || running" class="h-4 w-4 animate-spin" />
            <Send v-else class="h-4 w-4" />
            推送当前筛选（{{ total }}）
          </Button>
          <Button
            variant="outline"
            :disabled="pushing || running || !selected.length"
            @click="pushSelected"
          >
            <Send class="h-4 w-4" />
            推送所选 {{ selected.length || '' }}
          </Button>
          <Button variant="outline" :disabled="pushing || running || !failedCount" @click="retryFailed">
            <RotateCcw class="h-4 w-4" />
            重试推送失败（{{ failedCount }}）
          </Button>
        </div>

        <div v-if="progress && progress.total" class="mt-5">
          <TaskProgressBar :progress="progress" cancelable @cancel="stopAll" />
        </div>
      </Card>

      <Card>
        <template #title>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-[15px] font-medium">待推送列表</h2>
              <span class="ml-2 text-[13px] font-normal text-[var(--color-muted)]">共 {{ total }} 条</span>
            </div>
            <button
              class="flex items-center gap-1 text-[13px] text-[var(--color-brand)] hover:underline"
              @click="emit('goto', 'library')"
            >
              <Library class="h-3.5 w-3.5" />
              推送完成的去资源库回看 →
            </button>
          </div>
        </template>

        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-full max-w-xs">
            <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--color-faint)]" />
            <Input v-model="keyword" class="pl-9" placeholder="搜索片名 / 链接" />
          </div>
          <div class="flex flex-wrap gap-1.5">
            <button
              v-for="f in filters"
              :key="f.value"
              class="rounded-full px-3 py-1 text-[13px] transition-colors"
              :class="
                status === f.value
                  ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)] font-medium'
                  : 'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)]'
              "
              @click="status = f.value"
            >
              {{ f.label }}
            </button>
          </div>
        </div>

        <div
          v-if="loadError"
          class="mt-4 flex items-center gap-2 rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]"
        >
          <AlertCircle class="h-4 w-4 shrink-0" />
          <span class="min-w-0 flex-1">列表加载失败：{{ loadError }}</span>
          <Button size="sm" variant="outline" @click="loadList">
            <RotateCcw class="h-3.5 w-3.5" />
            重试
          </Button>
        </div>

        <div v-if="loading" class="py-10 text-center text-[var(--color-muted)]">
          <Loader2 class="mx-auto h-5 w-5 animate-spin" />
        </div>

        <div v-else-if="!items.length" class="py-12 text-center">
          <Inbox class="mx-auto h-8 w-8 text-[var(--color-faint)]" />
          <p class="mt-3 text-[13px] text-[var(--color-muted)]">{{ emptyText }}</p>
          <Button class="mx-auto mt-4" size="sm" @click="emit('goto', 'scrape')">
            <Plus class="h-4 w-4" />
            去刮削
          </Button>
        </div>

        <template v-else>
          <div
            class="mt-4 flex items-center gap-2 border-b border-[var(--color-line)] pb-2 text-[13px] text-[var(--color-muted)]"
          >
            <input
              type="checkbox"
              :checked="allChecked"
              :indeterminate.prop="selected.length > 0 && !allChecked"
              @change="toggleAll"
            />
            <label class="cursor-pointer select-none" @click="toggleAll">全选本页</label>
            <span v-if="selected.length" class="text-[var(--color-brand)]">已选 {{ selected.length }} 条</span>
            <span class="ml-auto text-xs text-[var(--color-faint)]">点击条目预览卡片</span>
          </div>

          <div class="space-y-2 pt-2">
            <div
              v-for="l in items"
              :key="l.id"
              class="rounded-lg border p-3 transition-colors"
              :class="
                selectedId === l.id
                  ? 'border-[var(--color-brand)] bg-[var(--color-brand-soft)]/30'
                  : 'border-[var(--color-line)]'
              "
            >
              <div class="flex items-start gap-3">
                <input
                  type="checkbox"
                  class="mt-1"
                  :checked="selected.includes(l.id)"
                  @change="toggleSelect(l.id)"
                />
                <div class="min-w-0 flex-1 cursor-pointer" @click="selectedId = l.id">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="truncate text-[13px] font-medium">{{ l.title || l.raw_title || '—' }}</span>
                    <Badge :class="statusMeta[l.status]?.cls">{{ statusMeta[l.status]?.label ?? l.status }}</Badge>
                    <Badge v-if="mediaMeta[l.media_type]" :class="mediaMeta[l.media_type].cls">
                      {{ mediaMeta[l.media_type].label }}
                    </Badge>
                  </div>
                  <p class="mt-0.5 text-xs text-[var(--color-muted)]">
                    {{ [l.year || '', l.tmdb_id ? `TMDB ${l.tmdb_id}` : '', l.code ? `提取码 ${l.code}` : ''].filter(Boolean).join(' · ') }}
                  </p>
                  <p v-if="l.error" class="mt-1 text-xs text-[var(--color-bad)]">{{ l.error }}</p>
                </div>

                <div class="flex shrink-0 items-center gap-1">
                  <button
                    class="flex items-center gap-1 rounded-md px-2 py-1 text-[13px] text-[var(--color-brand)] hover:bg-[var(--color-surface-hover)] disabled:opacity-40"
                    :disabled="pushing || running || acting === l.id"
                    @click="pushOneItem(l)"
                  >
                    <Loader2 v-if="acting === l.id" class="h-3.5 w-3.5 animate-spin" />
                    <Send v-else class="h-3.5 w-3.5" />
                    推送
                  </button>
                </div>
              </div>
            </div>
          </div>
        </template>

        <div v-if="items.length" class="mt-4 flex items-center justify-between text-[13px] text-[var(--color-muted)]">
          <span>第 {{ page + 1 }} / {{ pageCount }} 页</span>
          <div class="flex gap-2">
            <Button variant="outline" size="sm" :disabled="page === 0" @click="go(-1)">上一页</Button>
            <Button variant="outline" size="sm" :disabled="page + 1 >= pageCount" @click="go(1)">下一页</Button>
          </div>
        </div>
      </Card>
    </div>

    <Card class="lg:sticky lg:top-5" title="卡片预览" desc="实际推送到频道的效果">
      <div v-if="!current" class="flex h-40 items-center justify-center text-[13px] text-[var(--color-faint)]">
        暂无待推送记录
      </div>
      <div
        v-else
        class="overflow-hidden rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)]"
      >
        <div
          class="flex h-[300px] items-center justify-center bg-[var(--color-surface-hover)] bg-cover bg-center"
          :style="current.poster_url ? { backgroundImage: `url(${current.poster_url})` } : {}"
        >
          <span v-if="!current.poster_url" class="text-[13px] text-[var(--color-faint)]">海报</span>
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
            <p class="text-xs text-[var(--color-muted)]">{{ current.overview }}</p>
          </template>
        </div>
        <div class="flex items-center justify-between border-t border-[var(--color-line)] px-3 py-2">
          <span class="text-xs text-[var(--color-faint)]">{{ captionPlain.length }} 字</span>
          <Button variant="ghost" size="sm" :disabled="!captionPlain" @click="copyCaption">复制文案</Button>
        </div>
      </div>
    </Card>
  </div>
</template>
